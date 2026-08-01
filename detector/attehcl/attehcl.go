// Package attehcl detects test definitions in atte.hcl files.
package attehcl

import (
	"fmt"
	"path"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/graph"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

const (
	Namespace                             = "attehcl"
	TestKind                              = Namespace + ":test"
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

type testBlock struct {
	body   *hclsyntax.Body
	labels []string
}
type dependency struct {
	value  string
	entity graph.EntityID
}

func EntityID(file reference.Blob, name string) graph.EntityID {
	return graph.EntityID(fmt.Sprintf("%s:test:%s:%s", Namespace, file, name))
}
func DecodeEntityID(id graph.EntityID) (reference.Blob, string, error) {
	parts := strings.SplitN(string(id), ":", 4)
	if len(parts) != 4 || parts[0] != Namespace || parts[1] != "test" {
		return "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	file, err := reference.ParseBlob(parts[2])
	if err != nil {
		return "", "", fmt.Errorf("invalid HCL file path %q: %w", parts[2], err)
	}
	return file, parts[3], nil
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
		blocks, err := parse(fileBlob, contents)
		if err != nil {
			return nil, err
		}
		labels := map[string]struct{}{}
		for index, block := range blocks {
			name := fmt.Sprintf("%d", index)
			if len(block.labels) == 1 {
				name = block.labels[0]
				if _, ok := labels[name]; ok {
					return nil, fmt.Errorf("parse HCL %q: duplicate test label %q", file, name)
				}
				labels[name] = struct{}{}
			}
			id := EntityID(fileBlob, name)
			addEntity(graph.Entity{ID: id, Kind: TestKind})
			if containment {
				fileBlob, err := reference.ParseBlob(file.String())
				if err != nil {
					return nil, fmt.Errorf("invalid HCL file path %q: %w", file, err)
				}
				addEntity(graph.Entity{ID: attegit.EntityID(fileBlob.Tree()), Kind: attegit.TreeKind})
				addEntity(graph.Entity{ID: attegit.EntityID(fileBlob), Kind: attegit.BlobKind})
				relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(fileBlob), Kind: SourceFileRelation})
				relations = append(relations, graph.Relationship{From: attegit.EntityID(fileBlob.Tree()), To: id, Kind: attegit.ContainsRelation})
			}
			cfg, deps, err := decodeTest(repo, fileBlob, block)
			if err != nil {
				return nil, err
			}
			if cfg.Script != "" {
				source, err := reference.ParseBlob(file.String())
				targetBlob, typedErr := reference.ResolveBlobFromBlob(source, reference.SomePath(cfg.Script))
				target := string(targetBlob)
				if err == nil {
					err = typedErr
				}
				if err != nil {
					return nil, fmt.Errorf("%q: %w", file, err)
				}
				if obj, ok := repo.Obj[targetBlob]; !ok || obj.Kind != attegit.Blob {
					return nil, fmt.Errorf("%q: script %q not found", file, target)
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
				source, err := reference.ParseBlob(file.String())
				targetBlob, typedErr := reference.ResolveBlobFromBlob(source, reference.SomePath(dep.value))
				target := string(targetBlob)
				if err == nil {
					err = typedErr
				}
				if err != nil {
					return nil, fmt.Errorf("%q: %w", file, err)
				}
				obj, ok := repo.Obj[targetBlob]
				if !ok || obj.Kind != attegit.Blob {
					return nil, fmt.Errorf("%q: dependency %q not found", file, target)
				}
				addEntity(graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
				relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(targetBlob), Kind: DependsOnRelation})
			}
		}
	}
	return graph.New(entities, relations)
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

func parse(file reference.Blob, contents []byte) ([]testBlock, error) {
	f, diags := hclparse.NewParser().ParseHCL(contents, file.String())
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse HCL %q: %s", file, diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse HCL %q: unsupported body type %T", file, f.Body)
	}
	blocks := make([]testBlock, 0)
	for _, block := range body.Blocks {
		if block.Type != "test" {
			continue
		}
		if len(block.Labels) > 1 {
			return nil, fmt.Errorf("parse HCL %q: test has too many labels", file)
		}
		blocks = append(blocks, testBlock{body: block.Body, labels: block.Labels})
	}
	return blocks, nil
}
func decodeTest(repo *attegit.Repo, file reference.Blob, block testBlock) (testConfig, []dependency, error) {
	cfg := testConfig{}
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
	content, diags := block.body.Content(&hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "script", Required: true}, {Name: "depends_on"}, {Name: "triggered_by"}}})
	if diags.HasErrors() {
		return cfg, nil, fmt.Errorf("decode HCL %q: %s", file, diags.Error())
	}
	if value, ok := content.Attributes["script"]; ok {
		v, d := value.Expr.Value(ctx)
		if d.HasErrors() {
			return cfg, nil, fmt.Errorf("decode HCL %q: %s", file, d.Error())
		}
		if !v.IsKnown() || v.Type() != cty.String {
			return cfg, nil, fmt.Errorf("decode HCL %q: script must be a string", file)
		}
		cfg.Script = v.AsString()
	}
	for _, name := range []string{"depends_on", "triggered_by"} {
		if attr, ok := content.Attributes[name]; ok {
			values, d := attr.Expr.Value(ctx)
			if d.HasErrors() {
				return cfg, nil, fmt.Errorf("decode HCL %q: %s", file, d.Error())
			}
			if !values.IsKnown() || !values.CanIterateElements() {
				return cfg, nil, fmt.Errorf("decode HCL %q: %s must be a list", file, name)
			}
			it := values.ElementIterator()
			for it.Next() {
				_, v := it.Element()
				if v.Type() != cty.String {
					return cfg, nil, fmt.Errorf("decode HCL %q: %s values must be strings", file, name)
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
	return cfg, deps, nil
}
