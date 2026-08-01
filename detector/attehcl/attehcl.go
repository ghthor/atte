// Package attehcl detects test, codegen, and lint definitions in atte.hcl files.
package attehcl

import (
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
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
	Filename                              = "atte.hcl"
	Namespace                             = "attehcl"
	TestKind                              = Namespace + ":test"
	CodegenKind                           = Namespace + ":codegen"
	LintKind                              = Namespace + ":lint"
	SourceFileRelation graph.RelationKind = "source-file"
	ScriptRelation     graph.RelationKind = "script"
	DependsOnRelation  graph.RelationKind = "depends-on"

	DecodingPathPrefix = "attehcl-path:"
)

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

type hclFile struct {
	file    reference.Blob
	body    *hclsyntax.Body
	globals map[string]hcl.Expression
	locals  map[string]hcl.Expression
}

type hclScope struct {
	global cty.Value
	local  cty.Value
}

func objectValue(values map[string]cty.Value) cty.Value {
	if len(values) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(values)
}

func declarationBlocks(file reference.Blob, body *hclsyntax.Body) (map[string]hcl.Expression, map[string]hcl.Expression, error) {
	globals := make(map[string]hcl.Expression)
	locals := make(map[string]hcl.Expression)
	for _, block := range body.Blocks {
		var dst map[string]hcl.Expression
		switch block.Type {
		case "globals":
			dst = globals
		case "locals":
			dst = locals
		default:
			continue
		}
		for name, attr := range block.Body.Attributes {
			if _, exists := dst[name]; exists {
				return nil, nil, fmt.Errorf("decode HCL %q: duplicate %s attribute %q", file, block.Type, name)
			}
			dst[name] = attr.Expr
		}
	}
	return globals, locals, nil
}

func hclFiles(repo *attegit.Repo) (map[reference.Blob]*hclFile, error) {
	files := make(map[reference.Blob]*hclFile)
	for _, file := range repo.ObjKeys {
		if repo.Obj[file].Kind != attegit.Blob || path.Base(file.String()) != Filename {
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
		globals, locals, err := declarationBlocks(fileBlob, body)
		if err != nil {
			return nil, err
		}
		files[fileBlob] = &hclFile{file: fileBlob, body: body, globals: globals, locals: locals}
	}
	return files, nil
}

func evaluateDeclarations(repo *attegit.Repo, file *hclFile, expressions map[string]hcl.Expression, inherited map[string]cty.Value, namespace string) (map[string]cty.Value, error) {
	values := make(map[string]cty.Value, len(expressions))
	pending := make(map[string]hcl.Expression, len(expressions))
	for name, expr := range expressions {
		pending[name] = expr
	}
	for len(pending) > 0 {
		progress := false
		var lastName string
		var lastDiags hcl.Diagnostics
		for name, expr := range pending {
			lastName = name
			ctxValues := make(map[string]cty.Value, 2)
			global := inherited
			if namespace == "global" {
				global = make(map[string]cty.Value, len(inherited)+len(values))
				for key, value := range inherited {
					global[key] = value
				}
				for key, value := range values {
					global[key] = value
				}
			}
			ctxValues["global"] = objectValue(global)
			if namespace == "local" {
				ctxValues["local"] = objectValue(values)
			}
			value, diags := expr.Value(&hcl.EvalContext{Variables: ctxValues, Functions: hclFunctions(repo, file.file)})
			if diags.HasErrors() {
				lastDiags = diags
				continue
			}
			if !value.IsKnown() {
				return nil, fmt.Errorf("decode HCL %q: %s.%s must be known", file.file, namespace, name)
			}
			values[name] = value
			delete(pending, name)
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("decode HCL %q %s.%s: %s", file.file, namespace, lastName, lastDiags.Error())
		}
	}
	return values, nil
}

func scopeFor(repo *attegit.Repo, files map[reference.Blob]*hclFile, current *hclFile) (hclScope, error) {
	ancestors := make([]*hclFile, 0)
	dir := current.file.Tree()
	for {
		candidate, err := dir.Blob(Filename)
		if err != nil {
			return hclScope{}, err
		}
		if file, ok := files[candidate]; ok {
			ancestors = append(ancestors, file)
		}
		if dir == reference.Root {
			break
		}
		dir = dir.Parent()
	}
	globals := make(map[string]cty.Value)
	for index := len(ancestors) - 1; index >= 0; index-- {
		file := ancestors[index]
		values, err := evaluateDeclarations(repo, file, file.globals, globals, "global")
		if err != nil {
			return hclScope{}, err
		}
		for name, value := range values {
			globals[name] = value
		}
	}
	locals, err := evaluateDeclarations(repo, current, current.locals, globals, "local")
	if err != nil {
		return hclScope{}, err
	}
	return hclScope{global: objectValue(globals), local: objectValue(locals)}, nil
}

func evalContext(repo *attegit.Repo, file reference.Blob, scope hclScope) *hcl.EvalContext {
	return &hcl.EvalContext{Variables: map[string]cty.Value{"global": scope.global, "local": scope.local}, Functions: hclFunctions(repo, file)}
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

// Target describes one executable target declared by a test, codegen, or lint
// block in an atte.hcl file. Targets are returned in repository order, with
// unlabeled blocks assigned a zero-based numeric Name within their block kind
// and labeled blocks using the label as Name. Script is a repository-relative
// path resolved from an explicit path expression.
//
// Script is the repository-relative path resolved from an explicit path
// expression; Inline contains ordinary string script content. Both are empty
// only when the corresponding value is not present. ID is the corresponding graph entity
// identifier, and Selector converts the target to the canonical CLI selector.
//
// Kind is one of TestKind, CodegenKind, or LintKind. File identifies the
// declaring atte.hcl file. Label preserves the HCL block label, when present,
// while Index records the block's zero-based position within its kind.
type Target struct {
	ID     graph.EntityID
	Kind   string
	File   reference.Blob
	Name   string
	Label  string
	Index  int
	Script reference.Blob
	Inline string
}

// Command constructs the command used to execute the target from a repository root.
func (target Target) Command(root string) (*exec.Cmd, error) {
	if target.Script != "" {
		return exec.Command("/usr/bin/env", "bash", filepath.Join(root, filepath.FromSlash(target.Script.String()))), nil
	}
	if target.Inline != "" {
		return exec.Command("/usr/bin/env", "bash", "-c", target.Inline), nil
	}
	return nil, fmt.Errorf("target %q has no script", target.ID)
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
	files, err := hclFiles(repo)
	if err != nil {
		return nil, err
	}
	for _, file := range repo.ObjKeys {
		obj := repo.Obj[file]
		if obj.Kind != attegit.Blob || path.Base(file.String()) != Filename {
			continue
		}
		fileBlob, err := reference.ParseBlob(file.String())
		if err != nil {
			return nil, fmt.Errorf("invalid HCL file path %q: %w", file, err)
		}
		hclConfig := files[fileBlob]
		scope, err := scopeFor(repo, files, hclConfig)
		if err != nil {
			return nil, err
		}
		body := hclConfig.body
		ctx := evalContext(repo, fileBlob, scope)
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
				script, _, err := decodeScriptAndDeps(repo, fileBlob, block.body, ctx)
				if err != nil {
					return nil, err
				}
				scriptBlob := reference.Blob("")
				inline := ""
				if strings.HasPrefix(script, DecodingPathPrefix) {
					scriptBlob = reference.Blob(strings.TrimPrefix(script, DecodingPathPrefix))
				} else if script != "" {
					trimmed := strings.TrimSpace(script)
					if object, ok := repo.Obj[reference.Blob(trimmed)]; ok && (object.Kind == attegit.Blob || object.Kind == attegit.Tree) {
						return nil, fmt.Errorf("decode HCL %q: script string resolves to repository %q %q; use path(%q) for an external script", file, object.Kind, trimmed, trimmed)
					}
					inline = script
				}
				targets = append(targets, Target{ID: EntityID(spec.kind, fileBlob, name), Kind: spec.kind, File: fileBlob, Name: name, Label: label, Index: index, Script: scriptBlob, Inline: inline})
			}
		}
	}
	return targets, nil
}

// Config is the evaluated attehcl configuration for a repository directory.
type Config struct {
	Global  map[string]cty.Value
	Local   map[string]cty.Value
	Targets []Target
}

// ConfigFor evaluates the attehcl configuration for a repository-relative directory.
// Global values are inherited from repository ancestors; local values and targets
// are scoped to the requested directory.
func ConfigFor(repo *attegit.Repo, relativePath string) (Config, error) {
	if repo == nil {
		return Config{}, fmt.Errorf("repository is nil")
	}
	tree, err := reference.ParseTree(relativePath)
	if err != nil {
		return Config{}, fmt.Errorf("invalid repository directory %q: %w", relativePath, err)
	}
	files, err := hclFiles(repo)
	if err != nil {
		return Config{}, err
	}
	currentBlob, err := tree.Blob(Filename)
	if err != nil {
		return Config{}, err
	}
	current, ok := files[currentBlob]
	if !ok {
		current = &hclFile{file: currentBlob, globals: make(map[string]hcl.Expression), locals: make(map[string]hcl.Expression)}
	}
	scope, err := scopeFor(repo, files, current)
	if err != nil {
		return Config{}, err
	}
	allTargets, err := Targets(repo)
	if err != nil {
		return Config{}, err
	}
	targets := make([]Target, 0)
	for _, target := range allTargets {
		if target.File.Tree() == tree {
			targets = append(targets, target)
		}
	}
	global := make(map[string]cty.Value)
	for key, value := range scope.global.AsValueMap() {
		global[key] = value
	}
	local := make(map[string]cty.Value)
	for key, value := range scope.local.AsValueMap() {
		local[key] = value
	}
	return Config{Global: global, Local: local, Targets: targets}, nil
}

func Graph(repo *attegit.Repo) (*graph.Graph, error)                { return graphFor(repo, false) }
func GraphWithContainment(repo *attegit.Repo) (*graph.Graph, error) { return graphFor(repo, true) }

func graphFor(repo *attegit.Repo, containment bool) (*graph.Graph, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	files, err := hclFiles(repo)
	if err != nil {
		return nil, err
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
		if obj.Kind != attegit.Blob || path.Base(file.String()) != Filename {
			continue
		}
		fileBlob, err := reference.ParseBlob(file.String())
		if err != nil {
			return nil, fmt.Errorf("invalid HCL file path %q: %w", file, err)
		}
		config := files[fileBlob]
		scope, err := scopeFor(repo, files, config)
		if err != nil {
			return nil, err
		}
		ctx := evalContext(repo, fileBlob, scope)
		for _, spec := range []struct{ typ, kind string }{{"test", TestKind}, {"codegen", CodegenKind}, {"lint", LintKind}} {
			blocks, err := blocksOfType(fileBlob, config.body, spec.typ)
			if err != nil {
				return nil, err
			}
			r, err := addBlockGraph(repo, fileBlob, spec.kind, blocks, func(b rawBlock) (string, []dependency, error) {
				script, deps, err := decodeScriptAndDeps(repo, fileBlob, b.body, ctx)
				if strings.HasPrefix(script, DecodingPathPrefix) {
					script = strings.TrimPrefix(script, DecodingPathPrefix)
				}
				return script, deps, err
			}, addEntity, containment)
			if err != nil {
				return nil, err
			}
			relations = append(relations, r...)
		}
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
// hclFunctions returns the functions available to atte.hcl expressions.
// Keep all registrations here so adding a function does not require changing
// every expression decoder.
var RepositoryPathType = cty.CapsuleWithOps(
	"atte.repository_path",
	reflect.TypeOf(reference.Blob("")),
	&cty.CapsuleOps{
		GoString:  func(value interface{}) string { return fmt.Sprintf("path(%q)", value.(reference.Blob)) },
		RawEquals: func(a, b interface{}) bool { return a.(reference.Blob) == b.(reference.Blob) },
	},
)

func hclFunctions(repo *attegit.Repo, file reference.Blob) map[string]function.Function {
	return map[string]function.Function{
		"gopkg_test": gopkgTestFunction(repo),
		"path": function.New(&function.Spec{
			Params: []function.Parameter{{Name: "path", Type: cty.String}},
			Type:   function.StaticReturnType(RepositoryPathType),
			Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
				resolved, err := reference.ResolveBlobFromBlob(file, reference.SomePath(args[0].AsString()))
				if err != nil {
					return cty.NilVal, fmt.Errorf("resolve path %q from %q: %w", args[0].AsString(), file, err)
				}
				return cty.CapsuleVal(RepositoryPathType, &resolved), nil
			},
		}),
	}
}

func gopkgTestFunction(repo *attegit.Repo) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, ret cty.Type) (cty.Value, error) {
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
		},
	})
}

func decodeScriptAndDeps(repo *attegit.Repo, file reference.Blob, body *hclsyntax.Body, ctx *hcl.EvalContext) (string, []dependency, error) {
	deps := make([]dependency, 0)
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
		if !v.IsKnown() {
			return "", nil, fmt.Errorf("decode HCL %q: script must be known", file)
		}
		if v.Type() == RepositoryPathType {
			blob := *v.EncapsulatedValue().(*reference.Blob)
			script = DecodingPathPrefix + blob.String()
		} else if v.Type() == cty.String {
			script = v.AsString()
		} else {
			return "", nil, fmt.Errorf("decode HCL %q: script must be a string or path", file)
		}
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
				raw := ""
				if v.Type() == RepositoryPathType {
					blob := *v.EncapsulatedValue().(*reference.Blob)
					raw = DecodingPathPrefix + blob.String()
				} else if v.Type() == cty.String {
					raw = v.AsString()
				} else {
					return "", nil, fmt.Errorf("decode HCL %q: %s values must be strings or paths", file, name)
				}
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
