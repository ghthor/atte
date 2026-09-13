package attehcl

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

type hclFile struct {
	file       reference.Blob
	body       *hclsyntax.Body
	locals     map[string]hcl.Expression
	normalized []normalizedBlock
}

type hclFiles map[reference.Blob]*hclFile

func (files hclFiles) sortedBlobs() []reference.Blob {
	return slices.Sorted(maps.Keys(files))
}

func declarationBlocks(file reference.Blob, body *hclsyntax.Body) (map[string]hcl.Expression, error) {
	locals := make(map[string]hcl.Expression)
	for _, block := range body.Blocks {
		if block.Type != "locals" {
			continue
		}
		for name, attr := range block.Body.Attributes {
			if _, exists := locals[name]; exists {
				return nil, fmt.Errorf("decode HCL %q: duplicate locals attribute %q", file, name)
			}
			locals[name] = attr.Expr
		}
	}
	return locals, nil
}

func readHCLFiles(ctx context.Context, repo *attegit.Repo) (hclFiles, error) {
	files := make(hclFiles)
	for _, file := range repo.ObjKeys {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if repo.Obj[file].Kind != attegit.Blob || path.Base(file.String()) != Filename {
			continue
		}
		fileBlob, err := reference.ParseBlob(file.String())
		if err != nil {
			return nil, fmt.Errorf("invalid HCL file path %q: %w", file, err)
		}
		parsed, err := readHCLFile(repo, fileBlob)
		if err != nil {
			return nil, err
		}
		files[fileBlob] = parsed
	}
	return files, nil
}

func readHCLFile(repo *attegit.Repo, file reference.Blob) (*hclFile, error) {
	contents, err := repo.Show(file)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", file, err)
	}
	body, err := parseFile(file, contents)
	if err != nil {
		return nil, err
	}
	locals, err := declarationBlocks(file, body)
	if err != nil {
		return nil, err
	}
	return &hclFile{file: file, body: body, locals: locals}, nil
}

func parseFile(file reference.Blob, contents []byte) (*hclsyntax.Body, error) {
	f, diags := hclparse.NewParser().ParseHCL(contents, file.String())
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse HCL %q: %s", file, diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse HCL %q: unsupported body type %T", file, f.Body)
	}
	return body, nil
}
