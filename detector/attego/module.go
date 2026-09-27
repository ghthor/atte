// Package attego detects Go modules, packages, and package-test dependencies.
//
// All Go files are parsed conservatively without evaluating build constraints.
// Files belong to the deepest enclosing go.mod. Imports that cannot be resolved
// to a package in the repository are scoped to the importing module.
package attego

import (
	"context"
	"encoding/base64"
	"fmt"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"golang.org/x/mod/modfile"
)

// Namespace prefixes entity IDs produced by this package. The package-kind
// values distinguish repository packages, package tests, and unresolved imports.
const (
	Namespace = "attego"

	PackageKind         = Namespace + ":package"
	PackageTestKind     = Namespace + ":package-test"
	PackageStdlibKind   = Namespace + ":package-stdlib"
	PackageExternalKind = Namespace + ":package-external"

	// ImportsRelation relates a package to an imported package entity.
	ImportsRelation    graph.RelationKind = "imports"
	SourceFileRelation graph.RelationKind = "source-file"
)

type module struct {
	dir      reference.Tree
	name     string
	replaces []replace
}
type replace struct{ old, target string }

type goFile struct {
	dir, name string
	imports   []string
	test      bool
}
type packageInfo struct {
	module               *module
	dir, importPath      string
	imports, testImports map[string]struct{}
	goFiles, testFiles   []reference.Blob
	hasTests             bool
}

// EntityID encodes kind, moduleDir, and importPath into an attego graph.EntityID.
// It is the inverse of DecodeEntityID.
func EntityID(kind string, moduleDir reference.Tree, importPath string) graph.EntityID {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	goModPath := path.Join(string(moduleDir), "go.mod")
	return graph.EntityID(fmt.Sprintf("%s:%s:%s:%s", Namespace, kind[len(Namespace)+1:], goModPath, enc(importPath)))
}

// DecodeEntityID returns the kind, module directory, and import path encoded in an attego entity ID.
func DecodeEntityID(id graph.EntityID) (string, reference.Tree, string, error) {
	parts := strings.Split(string(id), ":")
	if len(parts) != 4 || parts[0] != Namespace {
		return "", "", "", fmt.Errorf("invalid attego entity ID %q", id)
	}
	goModPath := parts[2]
	if goModPath != "go.mod" && !strings.HasSuffix(goModPath, "/go.mod") {
		return "", "", "", fmt.Errorf("invalid attego module path %q", goModPath)
	}
	moduleDir := strings.TrimSuffix(strings.TrimSuffix(goModPath, "go.mod"), "/")

	decoded, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", "", "", fmt.Errorf("decode %q: %w", parts[3], err)
	}
	importPath := string(decoded)

	kind := Namespace + ":" + parts[1]
	if kind != PackageKind && kind != PackageTestKind && kind != PackageStdlibKind && kind != PackageExternalKind {
		return "", "", "", fmt.Errorf("invalid attego entity kind %q", kind)
	}
	tree, err := reference.ParseTree(moduleDir)
	if err != nil && moduleDir != "" {
		return "", "", "", fmt.Errorf("invalid attego module directory %q: %w", moduleDir, err)
	}
	return kind, tree, importPath, nil
}

// Detector adapts the Go Sensor to the shared detector capabilities.
type Detector struct {
	scanner detector.Scanner
}

// NewDetector creates a Go Sensor that can receive a compiled Scanner.
func NewDetector() *Detector {
	return &Detector{}
}

// AttachScanner returns a Go Sensor bound to the compiled Scanner.
func (Detector) AttachScanner(scanner detector.Scanner) (detector.Sensor, error) {
	return &Detector{scanner: scanner}, nil
}

func (Detector) Namespace() string { return Namespace }

// DecodeID converts an attego entity ID into the shared graph representation.
func (Detector) DecodeID(id graph.EntityID) (graph.Entity, error) {
	kind, _, _, err := DecodeEntityID(id)
	if err != nil {
		return graph.Entity{}, err
	}
	return graph.Entity{ID: id, Kind: graph.EntityKind(kind)}, nil
}

func (Detector) TargetSelector(target graphtarget.ID) selector.Target {
	result := Selector(Target{PackageDir: reference.Tree(target.Path)})
	result.Aliases = target.Aliases
	return result
}

func (Detector) ExecuteTarget(_ context.Context, _ *attegit.Repo, root string, target graphtarget.ID) (graphtarget.Execution, error) {
	return graphtarget.Execution{
		Dir:  filepath.Join(root, filepath.FromSlash(target.Path)),
		Args: []string{"go", "test", "-v"},
	}, nil
}

func (Detector) Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
	return Graph(ctx, repo, options...)
}

func (Detector) Targets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	found, err := Targets(ctx, repo)
	if err != nil {
		return nil, err
	}
	result := make([]graphtarget.ID, 0, len(found))
	for _, target := range found {
		result = append(result, graphtarget.ID{
			ID:        target.ID,
			Namespace: Namespace,
			Kind:      target.Kind,
			Path:      target.PackageDir.String(),
			Name:      "go_test",
			Aliases:   selector.Aliases(Selector(target)),
		})
	}
	return result, nil
}

// Target describes a runnable Go package test.
type Target struct {
	ID         graph.EntityID
	Kind       string
	ModuleDir  reference.Tree
	PackageDir reference.Tree
	ImportPath string
}

// Targets returns runnable Go package tests with their repository directories.
// Selector returns the canonical selector for a Go package-test target.
func Selector(target Target) selector.Target {
	return selector.Target{Path: target.PackageDir.String(), Kind: "go_test"}
}

func Targets(ctx context.Context, repo *attegit.Repo) ([]Target, error) {
	g, err := Graph(ctx, repo, graphset.WithAttachToTree())
	if err != nil {
		return nil, err
	}
	targets := make([]Target, 0)
	for _, id := range g.EntityKeys {
		entity := g.Entities[id]
		if entity.Kind != PackageTestKind {
			continue
		}
		_, moduleDir, importPath, err := DecodeEntityID(id)
		if err != nil {
			return nil, err
		}
		var packageDir reference.Tree
		for _, parentID := range g.EntityKeys {
			for _, relation := range g.Out(parentID) {
				if relation.Kind != attegit.ContainsRelation || relation.To != id {
					continue
				}
				if g.Entities[parentID].Kind != attegit.TreeKind {
					continue
				}
				decoded, err := attegit.EntityPath(parentID)
				if err != nil {
					return nil, err
				}
				packageDir, err = reference.ParseTree(decoded.String())
				if err != nil {
					return nil, err
				}
			}
		}
		if packageDir == "" {
			continue
		}
		targets = append(targets, Target{ID: id, Kind: string(entity.Kind), ModuleDir: moduleDir, PackageDir: packageDir, ImportPath: importPath})
	}
	return targets, nil
}

// Graph builds the dependency graph of Go packages and package tests found in
// repo, related by ImportsRelation. Pass graphset.WithAttachToTree to relate
// package entities to the repository's filesystem tree and source files.
func Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config := graphset.Options{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return graphFor(ctx, repo, config)
}

func graphFor(ctx context.Context, repo *attegit.Repo, options graphset.Options) (*graph.Graph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, fmt.Errorf("nil repository")
	}
	mods, files, err := scan(ctx, repo)
	if err != nil {
		return nil, err
	}
	packages := make(map[string]*packageInfo)
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m := owner(mods, f.dir)
		if m == nil {
			continue
		}
		key := string(m.dir) + "\x00" + f.dir
		p := packages[key]
		if p == nil {
			rel := strings.TrimPrefix(f.dir, string(m.dir))
			rel = strings.TrimPrefix(rel, "/")
			imp := m.name
			if rel != "" && rel != "." {
				imp += "/" + rel
			}
			p = &packageInfo{module: m, dir: f.dir, importPath: imp, imports: map[string]struct{}{}, testImports: map[string]struct{}{}}
			packages[key] = p
		}
		if f.test {
			p.hasTests = true
			file, err := reference.ParseBlob(f.name)
			if err != nil {
				return nil, err
			}
			p.testFiles = append(p.testFiles, file)
			for _, imp := range f.imports {
				p.testImports[imp] = struct{}{}
			}
		} else {
			file, err := reference.ParseBlob(f.name)
			if err != nil {
				return nil, err
			}
			p.goFiles = append(p.goFiles, file)
			for _, imp := range f.imports {
				p.imports[imp] = struct{}{}
			}
		}
	}
	byModulePath := make(map[string][]*module)
	for _, m := range mods {
		byModulePath[m.name] = append(byModulePath[m.name], m)
	}
	entities := map[graph.EntityID]graph.Entity{}
	relations := map[graph.Relationship]struct{}{}
	for _, p := range packages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid := EntityID(PackageKind, p.module.dir, p.importPath)
		entities[pid] = graph.Entity{ID: pid, Kind: PackageKind}
		var treeID graph.EntityID
		if options.AttachToTree {
			tree, err := reference.ParseTree(p.dir)
			if err != nil {
				return nil, err
			}
			treeID = attegit.EntityID(tree)
			entities[treeID] = graph.Entity{ID: treeID, Kind: attegit.TreeKind}
			relations[graph.Relationship{From: treeID, To: pid, Kind: attegit.ContainsRelation}] = struct{}{}
			addFileLinks := func(from graph.EntityID, files []reference.Blob) {
				for _, file := range files {
					fileID := attegit.EntityID(file)
					obj, ok := repo.Obj[file]
					if !ok || obj.Kind != attegit.Blob {
						continue
					}
					entities[fileID] = graph.Entity{ID: fileID, Kind: attegit.BlobKind}
					relations[graph.Relationship{From: from, To: fileID, Kind: SourceFileRelation}] = struct{}{}
				}
			}
			addFileLinks(pid, p.goFiles)
		}
		tid := EntityID(PackageTestKind, p.module.dir, p.importPath)
		if p.hasTests {
			entities[tid] = graph.Entity{ID: tid, Kind: PackageTestKind}
			if options.AttachToTree {
				relations[graph.Relationship{From: treeID, To: tid, Kind: attegit.ContainsRelation}] = struct{}{}
				relations[graph.Relationship{From: tid, To: pid, Kind: attegit.ContainsRelation}] = struct{}{}
				for _, file := range p.testFiles {
					fileID := attegit.EntityID(file)
					obj, ok := repo.Obj[file]
					if !ok || obj.Kind != attegit.Blob {
						continue
					}
					entities[fileID] = graph.Entity{ID: fileID, Kind: attegit.BlobKind}
					relations[graph.Relationship{From: tid, To: fileID, Kind: SourceFileRelation}] = struct{}{}
				}
			}
		}
		for _, imp := range sortedSet(p.imports) {
			addImport(p, pid, imp, packages, byModulePath, entities, relations)
		}
		for _, imp := range sortedSet(p.testImports) {
			addImport(p, tid, imp, packages, byModulePath, entities, relations)
		}
	}
	el := make([]graph.Entity, 0, len(entities))
	for _, e := range entities {
		el = append(el, e)
	}
	rl := make([]graph.Relationship, 0, len(relations))
	for r := range relations {
		rl = append(rl, r)
	}
	return graph.New(el, rl)
}

func parseModule(dir, name string, contents []byte) (*module, error) {
	f, err := modfile.Parse(name, contents, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.mod %q: %w", name, err)
	}
	var moduleDir reference.Tree
	if dir != "" {
		moduleDir, err = reference.ParseTree(dir)
		if err != nil {
			return nil, fmt.Errorf("invalid Go module directory %q: %w", dir, err)
		}
	}
	m := &module{dir: moduleDir, name: f.Module.Mod.Path}
	for _, r := range f.Replace {
		if r.New.Version == "" {
			m.replaces = append(m.replaces, replace{old: r.Old.Path, target: path.Clean(path.Join(dir, r.New.Path))})
		}
	}
	return m, nil
}

func scan(ctx context.Context, repo *attegit.Repo) ([]*module, []goFile, error) {
	mods := make([]*module, 0, len(repo.ObjKeys))
	files := make([]goFile, 0, len(repo.ObjKeys))
	for _, p := range repo.ObjKeys {
		if repo.Obj[p].Kind != attegit.Blob {
			continue
		}
		name := p.String()
		if path.Base(name) != "go.mod" && !strings.HasSuffix(name, ".go") {
			continue
		}
		contents, err := repo.ShowContext(ctx, p)
		if err != nil {
			return nil, nil, fmt.Errorf("read %q: %w", p, err)
		}
		dir := path.Dir(name)
		if dir == "." {
			dir = ""
		}
		if path.Base(name) == "go.mod" {
			m, err := parseModule(dir, name, contents)
			if err != nil {
				return nil, nil, err
			}
			mods = append(mods, m)
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, contents, parser.ImportsOnly)
		if err != nil {
			return nil, nil, fmt.Errorf("parse Go file %q: %w", p, err)
		}
		imports := make([]string, 0, len(f.Imports))
		for _, imp := range f.Imports {
			imports = append(imports, strings.Trim(imp.Path.Value, `"`))
		}
		files = append(files, goFile{dir: dir, name: name, imports: imports, test: strings.HasSuffix(name, "_test.go")})
	}
	sort.Slice(mods, func(i, j int) bool { return len(mods[i].dir) > len(mods[j].dir) })
	return mods, files, nil
}

func owner(mods []*module, dir string) *module {
	for _, m := range mods {
		if dir == string(m.dir) || strings.HasPrefix(dir, string(m.dir)+"/") || (m.dir == "" && dir != "") {
			return m
		}
	}
	return nil
}

func sortedSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func addImport(
	p *packageInfo,
	from graph.EntityID,
	imp string,
	packages map[string]*packageInfo,
	byPath map[string][]*module,
	entities map[graph.EntityID]graph.Entity,
	relations map[graph.Relationship]struct{},
) {
	var target graph.EntityID
	if q := resolveLocal(p, imp, packages); q != nil {
		target = EntityID(PackageKind, q.module.dir, q.importPath)
	} else if q := resolveModuleImport(imp, byPath, packages); q != nil {
		target = EntityID(PackageKind, q.module.dir, q.importPath)
	} else {
		kind := PackageExternalKind
		if isStdlibImport(imp) {
			kind = PackageStdlibKind
		}
		target = EntityID(kind, p.module.dir, imp)
		entities[target] = graph.Entity{ID: target, Kind: graph.EntityKind(kind)}
	}
	relations[graph.Relationship{From: from, To: target, Kind: ImportsRelation}] = struct{}{}
}

func isStdlibImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}

func resolveLocal(p *packageInfo, imp string, packages map[string]*packageInfo) *packageInfo {
	for _, r := range p.module.replaces {
		if imp == r.old || strings.HasPrefix(imp, r.old+"/") {
			suffix := strings.TrimPrefix(imp, r.old)
			if q := findPackageDir(path.Join(r.target, suffix), packages); q != nil {
				return q
			}
		}
	}
	if imp == p.module.name || strings.HasPrefix(imp, p.module.name+"/") {
		dir := strings.TrimPrefix(strings.TrimPrefix(imp, p.module.name), "/")
		return findPackage(p.module.dir, path.Join(string(p.module.dir), dir), packages)
	}
	return nil
}

func findPackage(modDir reference.Tree, dir string, packages map[string]*packageInfo) *packageInfo {
	if dir == "." {
		dir = ""
	}
	return packages[string(modDir)+"\x00"+dir]
}

func findPackageDir(dir string, packages map[string]*packageInfo) *packageInfo {
	for _, q := range packages {
		if string(q.dir) == dir {
			return q
		}
	}
	return nil
}

func resolveModuleImport(imp string, byPath map[string][]*module, packages map[string]*packageInfo) *packageInfo {
	var best *module
	bestLen := -1
	for name, ms := range byPath {
		if imp == name || strings.HasPrefix(imp, name+"/") {
			if len(name) > bestLen {
				bestLen = len(name)
				best = ms[0]
			}
		}
	}
	if best == nil {
		return nil
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(imp, best.name), "/")
	return findPackage(best.dir, path.Join(string(best.dir), rel), packages)
}
