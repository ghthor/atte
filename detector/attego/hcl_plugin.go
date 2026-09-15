package attego

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// HCLFunctions provides HCL functions for resolving Go packages in a repository.
func HCLFunctions(ctx context.Context, repo *attegit.Repo, file reference.Blob) (map[string]function.Function, error) {
	return map[string]function.Function{
		"gopkg":      packageHCLFunction(repo, file, PackageKind),
		"gopkg_test": packageHCLFunction(repo, file, PackageTestKind),
	}, nil
}

// HCLFunctions provides the HCL function factories contributed by the Go
// Sensor during sensor attachment.
func (Detector) HCLFunctions() map[string]func(context.Context, *attegit.Repo, reference.Blob) (function.Function, error) {
	return map[string]func(context.Context, *attegit.Repo, reference.Blob) (function.Function, error){
		"gopkg": func(ctx context.Context, repo *attegit.Repo, file reference.Blob) (function.Function, error) {
			return HCLFunction(ctx, repo, file, "gopkg")
		},
		"gopkg_test": func(ctx context.Context, repo *attegit.Repo, file reference.Blob) (function.Function, error) {
			return HCLFunction(ctx, repo, file, "gopkg_test")
		},
	}
}

func packageHCLFunction(repo *attegit.Repo, file reference.Blob, kind string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			importPath := args[0].AsString()
			if repo == nil {
				return cty.NilVal, fmt.Errorf("resolve %s(%q): repository is nil", kind, importPath)
			}
			id, err := resolvePackage(repo, file, args[0].AsString(), kind)
			if err != nil {
				return cty.NilVal, fmt.Errorf("resolve %q: %w", importPath, err)
			}
			return cty.StringVal(string(id)), nil
		},
	})
}

var errNotFound = errors.New("not found")

func moduleForTree(repo *attegit.Repo, tree reference.Tree) (*module, error) {
	startTree := tree
	for {
		modPath := reference.Blob(path.Join(string(tree), "go.mod"))
		if obj, ok := repo.Obj[modPath]; ok && obj.Kind == attegit.Blob {
			contents, err := repo.Show(modPath)
			if err != nil {
				return nil, fmt.Errorf("read module file %q for tree %q: %w", modPath, tree, err)
			}
			mod, err := parseModule(tree.String(), modPath.String(), contents)
			if err != nil {
				return nil, fmt.Errorf("parse module file %q for tree %q: %w", modPath, tree, err)
			}
			return mod, nil
		}

		if tree == reference.Root {
			break
		}
		tree = tree.Parent()
	}
	return nil, fmt.Errorf("no go.mod found for tree %q: %w", startTree, errNotFound)
}

func resolveTreePath(repo *attegit.Repo, rawPath string) (reference.Tree, bool) {
	tree := reference.Tree(rawPath)
	if _, ok := repo.Tree[tree]; ok {
		return tree, true
	}
	return "", false
}

func resolveTryPath(repo *attegit.Repo, srcFile reference.Blob, path string) (reference.Tree, *module, error) {
	tree, ok := resolveTreePath(repo, path)
	if !ok {
		return "", nil, fmt.Errorf("repository path %q does not exist as a tree or file: %w", path, errNotFound)
	}

	mod, err := moduleForTree(repo, tree)
	if err != nil {
		return "", nil, fmt.Errorf("resolve repository path %q: %w", path, err)
	}
	return tree, mod, nil
}

func resolveTryRelPath(repo *attegit.Repo, srcFile reference.Blob, path string) (reference.Tree, *module, error) {
	srcTree := srcFile.Tree()
	relPath := reference.SomePath(path)

	target := filepath.Clean(filepath.Join(string(srcTree), string(relPath)))

	targetTree, ok := resolveTreePath(repo, target)
	if !ok {
		return "", nil, fmt.Errorf(
			"path %q relative to source file %q resolves to repository path %q, which does not exist as a tree or file: %w",
			path,
			srcFile,
			target,
			errNotFound,
		)
	}
	mod, err := moduleForTree(repo, targetTree)
	if err != nil {
		return "", nil, fmt.Errorf("resolve path %q relative to %q: %w", path, srcFile, err)
	}
	return targetTree, mod, nil
}

var errAmbiguousModMatch = errors.New("ambiguous module match")

func resolveImportPath(repo *attegit.Repo, srcFile reference.Blob, importPath string) (reference.Tree, *module, error) {
	blobs := repo.BlobsNamed("go.mod")

	var best *module
	for _, b := range blobs {
		contents, err := repo.Show(b)
		if err != nil {
			return "", nil, fmt.Errorf("read module file %q while resolving import path %q: %w", b, importPath, err)
		}

		mod, err := parseModule(b.Tree().String(), b.String(), contents)
		if err != nil {
			return "", nil, fmt.Errorf("parse module file %q while resolving import path %q: %w", b, importPath, err)
		}

		if importPath != mod.name && !strings.HasPrefix(importPath, mod.name+"/") {
			continue
		}
		if best != nil && len(mod.name) == len(best.name) {
			return "", nil, fmt.Errorf("import path %q matches modules %q and %q: %w", importPath, best.name, mod.name, errAmbiguousModMatch)
		}
		if best == nil || len(mod.name) > len(best.name) {
			best = mod
		}
	}

	if best == nil {
		return "", nil, fmt.Errorf("module for import path %q from source file %q not found", importPath, srcFile)
	}

	rel := strings.TrimPrefix(importPath, best.name)
	rel = strings.TrimPrefix(rel, "/")
	dir := path.Join(best.dir.String(), rel)
	if dir == "." {
		dir = ""
	}

	tree := reference.Tree(dir)
	if _, ok := repo.Tree[tree]; !ok {
		return "", nil, fmt.Errorf("package for import path %q from source file %q not found in repository tree %q", importPath, srcFile, tree)
	}

	return tree, best, nil
}

func resolveParamToTree(repo *attegit.Repo, srcFile reference.Blob, importPath string) (tree reference.Tree, mod *module, err error) {
	if tree, mod, err = resolveTryPath(repo, srcFile, importPath); err == nil {
		return tree, mod, nil
	}
	pathErr := err

	if tree, mod, err = resolveTryRelPath(repo, srcFile, importPath); err == nil {
		return tree, mod, nil
	}
	relPathErr := err

	if tree, mod, err = resolveImportPath(repo, srcFile, importPath); err == nil {
		return tree, mod, nil
	}
	if !errors.Is(err, errNotFound) {
		return "", nil, err
	}

	return "", nil, fmt.Errorf(
		"resolve %q as repository path, relative path, or Go import path: repository path: %w; relative path: %v; import path: %v",
		importPath,
		pathErr,
		relPathErr,
		err,
	)
}

func resolvePackage(repo *attegit.Repo, file reference.Blob, value, kind string) (graph.EntityID, error) {
	if kind != PackageKind && kind != PackageTestKind {
		return "", fmt.Errorf("unsupported Go package kind %q", kind)
	}
	if filepath.Ext(value) == ".go" {
		return "", fmt.Errorf("path %q names a Go file; expected a package directory or import path", value)
	}

	tree, mod, err := resolveParamToTree(repo, file, value)
	if err != nil {
		return "", err
	}

	for _, b := range repo.Tree[tree] {
		if b.Kind != attegit.Blob {
			continue
		}

		name := filepath.Base(b.Path.String())
		ext := filepath.Ext(name)
		if ext != ".go" {
			continue
		}

		switch kind {
		case PackageTestKind:
			if !strings.HasSuffix(name, "_test.go") {
				continue
			}
			return packageForTree(repo, mod, tree, PackageTestKind), nil
		case PackageKind:
			return packageForTree(repo, mod, tree, PackageKind), nil
		}
	}

	if kind == PackageTestKind {
		return "", fmt.Errorf("package-test %q in repository tree %q has no Go test files", value, tree)
	}
	return "", fmt.Errorf("package %q in repository tree %q has no Go source files", value, tree)
}

func packageForTree(repo *attegit.Repo, ownerModule *module, tree reference.Tree, kind string) graph.EntityID {
	importPath := ownerModule.name
	rel := strings.TrimPrefix(string(tree), string(ownerModule.dir))
	rel = strings.TrimPrefix(rel, "/")
	if rel != "" {
		importPath += "/" + rel
	}
	return EntityID(kind, ownerModule.dir, importPath)
}

// HCLFunction returns one of the Go package HCL functions by name.
func HCLFunction(ctx context.Context, repo *attegit.Repo, file reference.Blob, name string) (function.Function, error) {
	functions, err := HCLFunctions(ctx, repo, file)
	if err != nil {
		return function.Function{}, err
	}
	fn, ok := functions[strings.TrimSpace(name)]
	if !ok {
		return function.Function{}, fmt.Errorf("unknown attego HCL function %q", name)
	}
	return fn, nil
}
