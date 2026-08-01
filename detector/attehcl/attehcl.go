// Package attehcl detects test, codegen, and lint definitions in atte.hcl files.
package attehcl

import (
	"fmt"
	"path"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/graph"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

const (
	Namespace                             = "attehcl"
	TestKind                              = Namespace + ":test"
	CodegenKind                           = Namespace + ":codegen"
	LintKind                              = Namespace + ":lint"
	SourceFileRelation graph.RelationKind = "source-file"
	ScriptRelation     graph.RelationKind = "script"
	DependsOnRelation  graph.RelationKind = "depends-on"
)

// testConfig documents the supported HCL schema for consumers that use gohcl.
type testConfig struct {
	Label       string   `hcl:",label"`
	Script      string   `hcl:"script"`
	DependsOn   []string `hcl:"depends_on,optional"`
	TriggeredBy []string `hcl:"triggered_by,optional"`
}

// codegenConfig documents the supported HCL schema for consumers that use gohcl.
type codegenConfig struct {
	Label       string   `hcl:",label"`
	Script      string   `hcl:"script"`
	DependsOn   []string `hcl:"depends_on,optional"`
	TriggeredBy []string `hcl:"triggered_by,optional"`
}

// lintConfig documents the supported HCL schema for consumers that use gohcl.
type lintConfig struct {
	Label       string   `hcl:",label"`
	Script      string   `hcl:"script"`
	DependsOn   []string `hcl:"depends_on,optional"`
	TriggeredBy []string `hcl:"triggered_by,optional"`
}

// rawBlock is a parsed HCL block body with its labels, before any
// kind-specific attribute decoding. It carries no schema information, so it
// is shared across every block kind.
type rawBlock struct {
	body   *hclsyntax.Body
	labels []string
}

type dependency struct {
	value  string
	entity graph.EntityID
}

func EntityID(kind string, file reference.Blob, name string) graph.EntityID {
	return graph.EntityID(fmt.Sprintf("%s:%s:%s", kind, file, name))
}
func DecodeEntityID(id graph.EntityID) (string, reference.Blob, string, error) {
	parts := strings.SplitN(string(id), ":", 4)
	if len(parts) != 4 || parts[0] != Namespace {
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	kind := parts[0] + ":" + parts[1]
	switch kind {
	case TestKind, CodegenKind, LintKind:
	default:
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	file, err := reference.ParseBlob(parts[2])
	if err != nil {
		return "", "", "", fmt.Errorf("invalid HCL file path %q: %w", parts[2], err)
	}
	return kind, file, parts[3], nil
}

// Target describes an executable HCL block and its resolved script.
type Target struct {
	ID     graph.EntityID
	Kind   string
	File   reference.Blob
	Name   string
	Label  string
	Index  int
	Script reference.Blob
}

// Targets returns executable test, codegen, and lint blocks in repository order.
// Selector returns the canonical selector for an HCL runnable target.
func Selector(target Target) selector.Target {
	return selector.Target{
		Path:  target.File.String(),
		Kind:  strings.TrimPrefix(target.Kind, Namespace+":"),
		Name:  target.Name,
		Index: target.Index,
	}
}

func Targets(repo *attegit.Repo) ([]Target, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	targets := make([]Target, 0)
	for _, file := range repo.ObjKeys {
		obj := repo.Obj[file]
		if obj.Kind != attegit.Blob || path.Base(file.String()) != "atte.hcl" {
			continue
		}
		contents, err := repo.Show(file)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", file, err)
		}
		fileBlob, err := reference.ParseBlob(file.String())
		if err != nil {
			return nil, fmt.Errorf("invalid HCL file path %q: %w", file, err)
		}
		body, err := parseFile(fileBlob, contents)
		if err != nil {
			return nil, err
		}
		for _, spec := range []struct{ blockType, kind string }{{"test", TestKind}, {"codegen", CodegenKind}, {"lint", LintKind}} {
			blocks, err := blocksOfType(fileBlob, body, spec.blockType)
			if err != nil {
				return nil, err
			}
			labels := make(map[string]struct{}, len(blocks))
			for index, block := range blocks {
				name := fmt.Sprintf("%d", index)
				label := ""
				if len(block.labels) == 1 {
					label = block.labels[0]
					name = label
					if _, exists := labels[name]; exists {
						return nil, fmt.Errorf("parse HCL %q: duplicate %s label %q", file, spec.blockType, name)
					}
					labels[name] = struct{}{}
				}
				script, _, err := decodeScriptAndDeps(repo, fileBlob, block.body)
				if err != nil {
					return nil, err
				}
				scriptBlob := reference.Blob("")
				if script != "" {
					scriptBlob, err = reference.ResolveBlobFromBlob(fileBlob, reference.SomePath(script))
					if err != nil {
						return nil, fmt.Errorf("%q: %w", file, err)
					}
				}
				targets = append(targets, Target{ID: EntityID(spec.kind, fileBlob, name), Kind: spec.kind, File: fileBlob, Name: name, Label: label, Index: index, Script: scriptBlob})
			}
		}
	}
	return targets, nil
}

func Graph(repo *attegit.Repo) (*graph.Graph, error)                { return graphFor(repo, false) }
func GraphWithContainment(repo *attegit.Repo) (*graph.Graph, error) { return graphFor(repo, true) }

func graphFor(repo *attegit.Repo, containment bool) (*graph.Graph, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	entities := make([]graph.Entity, 0)
	relations := make([]graph.Relationship, 0)
	addEntity := func(e graph.Entity) {
		for _, x := range entities {
			if x.ID == e.ID {
				return
			}
		}
		entities = append(entities, e)
	}
	for _, file := range repo.ObjKeys {
		obj := repo.Obj[file]
		if obj.Kind != attegit.Blob || path.Base(file.String()) != "atte.hcl" {
			continue
		}
		contents, err := repo.Show(file)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", file, err)
		}
		fileBlob, err := reference.ParseBlob(file.String())
		if err != nil {
			return nil, fmt.Errorf("invalid HCL file path %q: %w", file, err)
		}
		body, err := parseFile(fileBlob, contents)
		if err != nil {
			return nil, err
		}

		testBlocks, err := blocksOfType(fileBlob, body, "test")
		if err != nil {
			return nil, err
		}
		testRelations, err := addBlockGraph(repo, fileBlob, TestKind, testBlocks, func(b rawBlock) (string, []dependency, error) {
			cfg, deps, err := decodeTestBlock(repo, fileBlob, b)
			return cfg.Script, deps, err
		}, addEntity, containment)
		if err != nil {
			return nil, err
		}
		relations = append(relations, testRelations...)

		codegenBlocks, err := blocksOfType(fileBlob, body, "codegen")
		if err != nil {
			return nil, err
		}
		codegenRelations, err := addBlockGraph(repo, fileBlob, CodegenKind, codegenBlocks, func(b rawBlock) (string, []dependency, error) {
			cfg, deps, err := decodeCodegenBlock(repo, fileBlob, b)
			return cfg.Script, deps, err
		}, addEntity, containment)
		if err != nil {
			return nil, err
		}
		relations = append(relations, codegenRelations...)

		lintBlocks, err := blocksOfType(fileBlob, body, "lint")
		if err != nil {
			return nil, err
		}
		lintRelations, err := addBlockGraph(repo, fileBlob, LintKind, lintBlocks, func(b rawBlock) (string, []dependency, error) {
			cfg, deps, err := decodeLintBlock(repo, fileBlob, b)
			return cfg.Script, deps, err
		}, addEntity, containment)
		if err != nil {
			return nil, err
		}
		relations = append(relations, lintRelations...)
	}
	return graph.New(entities, relations)
}

// addBlockGraph builds the entities and relations for one block kind's raw
// blocks: per-kind label/index bookkeeping, containment, script resolution,
// and dependency resolution. It is schema-agnostic — each kind supplies its
// own decode function to extract the script and dependencies it declares.
func addBlockGraph(repo *attegit.Repo, file reference.Blob, kind string, blocks []rawBlock, decode func(rawBlock) (string, []dependency, error), addEntity func(graph.Entity), containment bool) ([]graph.Relationship, error) {
	relations := make([]graph.Relationship, 0, len(blocks))
	labels := map[string]struct{}{}
	for index, block := range blocks {
		name := fmt.Sprintf("%d", index)
		if len(block.labels) == 1 {
			name = block.labels[0]
			if _, ok := labels[name]; ok {
				return nil, fmt.Errorf("parse HCL %q: duplicate %s label %q", file, strings.TrimPrefix(kind, Namespace+":"), name)
			}
			labels[name] = struct{}{}
		}
		id := EntityID(kind, file, name)
		addEntity(graph.Entity{ID: id, Kind: kind})
		if containment {
			addEntity(graph.Entity{ID: attegit.EntityID(file.Tree()), Kind: attegit.TreeKind})
			addEntity(graph.Entity{ID: attegit.EntityID(file), Kind: attegit.BlobKind})
			relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(file), Kind: SourceFileRelation})
			relations = append(relations, graph.Relationship{From: attegit.EntityID(file.Tree()), To: id, Kind: attegit.ContainsRelation})
		}
		script, deps, err := decode(block)
		if err != nil {
			return nil, err
		}
		if script != "" {
			targetBlob, err := reference.ResolveBlobFromBlob(file, reference.SomePath(script))
			if err != nil {
				return nil, fmt.Errorf("%q: %w", file, err)
			}
			if obj, ok := repo.Obj[targetBlob]; !ok || obj.Kind != attegit.Blob {
				return nil, fmt.Errorf("%q: script %q not found", file, targetBlob)
			}
			addEntity(graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
			relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(targetBlob), Kind: ScriptRelation})
		}
		for _, dep := range deps {
			if dep.entity != "" {
				addEntity(graph.Entity{ID: dep.entity, Kind: dependencyKind(repo, dep.entity)})
				relations = append(relations, graph.Relationship{From: id, To: dep.entity, Kind: DependsOnRelation})
				continue
			}
			targetBlob, err := reference.ResolveBlobFromBlob(file, reference.SomePath(dep.value))
			if err != nil {
				return nil, fmt.Errorf("%q: %w", file, err)
			}
			obj, ok := repo.Obj[targetBlob]
			if !ok || obj.Kind != attegit.Blob {
				return nil, fmt.Errorf("%q: dependency %q not found", file, targetBlob)
			}
			addEntity(graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
			relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(targetBlob), Kind: DependsOnRelation})
		}
	}
	return relations, nil
}
func dependencyKind(repo *attegit.Repo, id graph.EntityID) string {
	g, err := attego.Graph(repo)
	if err == nil {
		if e, ok := g.Entities[id]; ok {
			return e.Kind
		}
	}
	return attego.PackageTestKind
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

// blocksOfType extracts the top-level blocks of the given HCL block type
// (e.g. "test", "codegen", "lint") from an already-parsed file body. It
// carries no schema knowledge, so it works unmodified for any block kind.
func blocksOfType(file reference.Blob, body *hclsyntax.Body, blockType string) ([]rawBlock, error) {
	blocks := make([]rawBlock, 0)
	for _, block := range body.Blocks {
		if block.Type != blockType {
			continue
		}
		if len(block.Labels) > 1 {
			return nil, fmt.Errorf("parse HCL %q: %s has too many labels", file, blockType)
		}
		blocks = append(blocks, rawBlock{body: block.Body, labels: block.Labels})
	}
	return blocks, nil
}

// decodeScriptAndDeps decodes the script/depends_on/triggered_by attributes
// shared by every attehcl block kind today. A kind whose schema diverges
// calls this for the attributes it still has in common and layers its own
// decoding on top for the rest.
func decodeScriptAndDeps(repo *attegit.Repo, file reference.Blob, body *hclsyntax.Body) (string, []dependency, error) {
	deps := make([]dependency, 0)
	ctx := &hcl.EvalContext{Variables: map[string]cty.Value{}, Functions: map[string]function.Function{
		"gopkg_test": function.New(&function.Spec{Params: []function.Parameter{{Name: "path", Type: cty.String}}, Type: function.StaticReturnType(cty.String), Impl: func(args []cty.Value, ret cty.Type) (cty.Value, error) {
			g, err := attego.Graph(repo)
			if err != nil {
				return cty.NilVal, err
			}
			for id, entity := range g.Entities {
				if entity.Kind == attego.PackageTestKind {
					_, _, importPath, err := attego.DecodeEntityID(id)
					if err == nil && importPath == args[0].AsString() {
						return cty.StringVal("attehcl-id:" + string(id)), nil
					}
				}
			}
			return cty.NilVal, fmt.Errorf("go package-test %q not found", args[0].AsString())
		}}),
	}}
	content, diags := body.Content(&hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "script", Required: true}, {Name: "depends_on"}, {Name: "triggered_by"}}})
	if diags.HasErrors() {
		return "", nil, fmt.Errorf("decode HCL %q: %s", file, diags.Error())
	}
	script := ""
	if value, ok := content.Attributes["script"]; ok {
		v, d := value.Expr.Value(ctx)
		if d.HasErrors() {
			return "", nil, fmt.Errorf("decode HCL %q: %s", file, d.Error())
		}
		if !v.IsKnown() || v.Type() != cty.String {
			return "", nil, fmt.Errorf("decode HCL %q: script must be a string", file)
		}
		script = v.AsString()
	}
	for _, name := range []string{"depends_on", "triggered_by"} {
		if attr, ok := content.Attributes[name]; ok {
			values, d := attr.Expr.Value(ctx)
			if d.HasErrors() {
				return "", nil, fmt.Errorf("decode HCL %q: %s", file, d.Error())
			}
			if !values.IsKnown() || !values.CanIterateElements() {
				return "", nil, fmt.Errorf("decode HCL %q: %s must be a list", file, name)
			}
			it := values.ElementIterator()
			for it.Next() {
				_, v := it.Element()
				if v.Type() != cty.String {
					return "", nil, fmt.Errorf("decode HCL %q: %s values must be strings", file, name)
				}
				raw := v.AsString()
				if strings.HasPrefix(raw, "attehcl-id:") {
					deps = append(deps, dependency{entity: graph.EntityID(strings.TrimPrefix(raw, "attehcl-id:"))})
				} else {
					deps = append(deps, dependency{value: raw})
				}
			}
		}
	}
	return script, deps, nil
}

func decodeTestBlock(repo *attegit.Repo, file reference.Blob, block rawBlock) (testConfig, []dependency, error) {
	cfg := testConfig{}
	script, deps, err := decodeScriptAndDeps(repo, file, block.body)
	cfg.Script = script
	return cfg, deps, err
}

func decodeCodegenBlock(repo *attegit.Repo, file reference.Blob, block rawBlock) (codegenConfig, []dependency, error) {
	cfg := codegenConfig{}
	script, deps, err := decodeScriptAndDeps(repo, file, block.body)
	cfg.Script = script
	return cfg, deps, err
}

func decodeLintBlock(repo *attegit.Repo, file reference.Blob, block rawBlock) (lintConfig, []dependency, error) {
	cfg := lintConfig{}
	script, deps, err := decodeScriptAndDeps(repo, file, block.body)
	cfg.Script = script
	return cfg, deps, err
}
