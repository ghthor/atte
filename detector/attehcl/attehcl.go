// Package attehcl detects test, codegen, and lint definitions in atte.hcl files.
package attehcl

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ghthor/atte/detector"
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

var blockKinds = []struct {
	hclType string
	kind    string
}{
	{hclType: "test", kind: TestKind},
	{hclType: "codegen", kind: CodegenKind},
	{hclType: "lint", kind: LintKind},
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

type hclFiles map[reference.Blob]*hclFile

func (files hclFiles) sortedBlobs() []reference.Blob {
	return slices.Sorted(maps.Keys(files))
}

func readHCLFiles(repo *attegit.Repo) (hclFiles, error) {
	files := make(hclFiles)
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

func declarationContext(namespace string, inherited, values map[string]cty.Value, functions map[string]function.Function) *hcl.EvalContext {
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
	variables := map[string]cty.Value{"global": objectValue(global)}
	if namespace == "local" {
		variables["local"] = objectValue(values)
	}
	return &hcl.EvalContext{Variables: variables, Functions: functions}
}

func evaluateDeclaration(expression hcl.Expression, context *hcl.EvalContext) (cty.Value, hcl.Diagnostics) {
	return expression.Value(context)
}

// evaluateDeclarations resolves declarations within one HCL file. Cross-file
// target dependencies will need a repository-wide target index or dependency
// resolution phase rather than extending this file-local retry loop.
func evaluateDeclarations(file *hclFile, expressions map[string]hcl.Expression, inherited map[string]cty.Value, namespace string, functions map[string]function.Function) (map[string]cty.Value, error) {
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
			ctx := declarationContext(namespace, inherited, values, functions)
			value, diags := evaluateDeclaration(expr, ctx)
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

type evaluator struct {
	ctx       context.Context
	repo      *attegit.Repo
	files     hclFiles
	provider  detector.FunctionProvider
	functions map[reference.Blob]map[string]function.Function
}

type decodedBlock struct {
	kind   string
	file   reference.Blob
	name   string
	label  string
	index  int
	script string
	deps   []dependency
}

type globalPhase struct {
	evaluator *evaluator
	scopes    map[reference.Tree]map[string]cty.Value
}

type evaluationPhase struct {
	globals globalPhase
}

func newEvaluator(ctx context.Context, repo *attegit.Repo, provider detector.FunctionProvider) (*evaluator, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	files, err := readHCLFiles(repo)
	if err != nil {
		return nil, err
	}
	return &evaluator{
		ctx:       ctx,
		repo:      repo,
		files:     files,
		provider:  provider,
		functions: make(map[reference.Blob]map[string]function.Function),
	}, nil
}

func (e *evaluator) hclFunctions(file reference.Blob) (map[string]function.Function, error) {
	if functions, ok := e.functions[file]; ok {
		return functions, nil
	}
	functions, err := mergedHCLFunctions(e.ctx, e.repo, file, e.provider)
	if err != nil {
		return nil, err
	}
	e.functions[file] = functions
	return functions, nil
}

func (e *evaluator) resolveGlobals() (globalPhase, error) {
	phase := globalPhase{
		evaluator: e,
		scopes:    make(map[reference.Tree]map[string]cty.Value),
	}

	var visit func(reference.Tree) error
	visit = func(dir reference.Tree) error {
		inherited := phase.scopes[dir.Parent()]
		globals := make(map[string]cty.Value, len(inherited))
		for name, value := range inherited {
			globals[name] = value
		}
		candidate, err := dir.Blob(Filename)
		if err != nil {
			return err
		}
		if file, ok := e.files[candidate]; ok {
			functions, err := e.hclFunctions(file.file)
			if err != nil {
				return err
			}
			values, err := evaluateDeclarations(file, file.globals, globals, "global", functions)
			if err != nil {
				return err
			}
			for name, value := range values {
				globals[name] = value
			}
		}
		phase.scopes[dir] = globals

		for _, child := range e.repo.DirTree[dir] {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(reference.Root); err != nil {
		return globalPhase{}, err
	}
	return phase, nil
}

func (p globalPhase) scopeFor(file *hclFile) (hclScope, error) {
	globals := p.scopes[file.file.Tree()]
	functions, err := p.evaluator.hclFunctions(file.file)
	if err != nil {
		return hclScope{}, err
	}
	locals, err := evaluateDeclarations(file, file.locals, globals, "local", functions)
	if err != nil {
		return hclScope{}, err
	}
	return hclScope{global: objectValue(globals), local: objectValue(locals)}, nil
}

func (p evaluationPhase) scopeFor(file *hclFile) (hclScope, error) {
	return p.globals.scopeFor(file)
}

func (e *evaluator) evalContext(file reference.Blob, scope hclScope) (*hcl.EvalContext, error) {
	functions, err := e.hclFunctions(file)
	if err != nil {
		return nil, err
	}
	return &hcl.EvalContext{Variables: map[string]cty.Value{"global": scope.global, "local": scope.local}, Functions: functions}, nil
}

func (e *evaluator) decodedBlocks(globals globalPhase) ([]decodedBlock, error) {
	blocks := make([]decodedBlock, 0)
	phase := evaluationPhase{globals: globals}
	for _, file := range e.files.sortedBlobs() {
		config := e.files[file]
		scope, err := phase.scopeFor(config)
		if err != nil {
			return nil, err
		}
		ctx, err := e.evalContext(file, scope)
		if err != nil {
			return nil, err
		}
		for _, spec := range blockKinds {
			raw, err := blocksOfType(file, config.body, spec.hclType)
			if err != nil {
				return nil, err
			}
			labels := make(map[string]struct{}, len(raw))
			for index, block := range raw {
				name := fmt.Sprintf("%d", index)
				label := ""
				if len(block.labels) == 1 {
					label = block.labels[0]
					name = label
					if _, exists := labels[name]; exists {
						return nil, fmt.Errorf("parse HCL %q: duplicate %s label %q", file, spec.hclType, name)
					}
					labels[name] = struct{}{}
				}
				script, deps, err := decodeTargetBlock(e.repo, file, block.body, ctx)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, decodedBlock{kind: spec.kind, file: file, name: name, label: label, index: index, script: script, deps: deps})
			}
		}
	}
	return blocks, nil
}

func targetsFromDecoded(repo *attegit.Repo, blocks []decodedBlock) ([]Target, error) {
	targets := make([]Target, 0, len(blocks))
	for _, block := range blocks {
		scriptBlob := reference.Blob("")
		inline := ""
		if strings.HasPrefix(block.script, DecodingPathPrefix) {
			scriptBlob = reference.Blob(strings.TrimPrefix(block.script, DecodingPathPrefix))
		} else if block.script != "" {
			trimmed := strings.TrimSpace(block.script)
			if object, ok := repo.Obj[reference.Blob(trimmed)]; ok {
				return nil, fmt.Errorf("decode HCL %q: script string resolves to repository %q %q; use path(%q) for an external script", block.file, object.Kind, trimmed, trimmed)
			}
			inline = block.script
		}
		kind := strings.TrimPrefix(block.kind, Namespace+":")
		aliases := selector.Aliases(selector.Target{Path: block.file.String(), Kind: kind, Name: block.name, Index: block.index})
		targets = append(targets, Target{
			ID:      EntityID(block.kind, block.file, block.name),
			Kind:    block.kind,
			File:    block.file,
			Name:    block.name,
			Label:   block.label,
			Index:   block.index,
			Aliases: aliases,
			Script:  scriptBlob,
			Inline:  inline,
		})
	}
	return targets, nil
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
	ID      graph.EntityID
	Kind    string
	File    reference.Blob
	Name    string
	Label   string
	Index   int
	Aliases []string
	Script  reference.Blob
	Inline  string
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
		Path:    target.File.String(),
		Kind:    strings.TrimPrefix(target.Kind, Namespace+":"),
		Name:    target.Name,
		Index:   target.Index,
		Aliases: target.Aliases,
	}
}

func Targets(ctx context.Context, repo *attegit.Repo, provider detector.FunctionProvider) ([]Target, error) {
	return targetsWithProvider(ctx, repo, provider)
}

func targetsWithProvider(ctx context.Context, repo *attegit.Repo, provider detector.FunctionProvider) ([]Target, error) {
	evaluator, err := newEvaluator(ctx, repo, provider)
	if err != nil {
		return nil, err
	}
	globals, err := evaluator.resolveGlobals()
	if err != nil {
		return nil, err
	}
	blocks, err := evaluator.decodedBlocks(globals)
	if err != nil {
		return nil, err
	}
	return targetsFromDecoded(repo, blocks)
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
func ConfigFor(ctx context.Context, repo *attegit.Repo, relativePath string, provider detector.FunctionProvider) (Config, error) {
	return configForWithProvider(ctx, repo, relativePath, provider)
}

func configForWithProvider(ctx context.Context, repo *attegit.Repo, relativePath string, provider detector.FunctionProvider) (Config, error) {
	if repo == nil {
		return Config{}, fmt.Errorf("repository is nil")
	}
	tree, err := reference.ParseTree(relativePath)
	if err != nil {
		return Config{}, fmt.Errorf("invalid repository directory %q: %w", relativePath, err)
	}
	evaluator, err := newEvaluator(ctx, repo, provider)
	if err != nil {
		return Config{}, err
	}
	globals, err := evaluator.resolveGlobals()
	if err != nil {
		return Config{}, err
	}
	currentBlob, err := tree.Blob(Filename)
	if err != nil {
		return Config{}, err
	}
	current, ok := evaluator.files[currentBlob]
	if !ok {
		current = &hclFile{file: currentBlob, globals: make(map[string]hcl.Expression), locals: make(map[string]hcl.Expression)}
	}
	phase := evaluationPhase{globals: globals}
	scope, err := phase.scopeFor(current)
	if err != nil {
		return Config{}, err
	}
	blocks, err := evaluator.decodedBlocks(globals)
	if err != nil {
		return Config{}, err
	}
	allTargets, err := targetsFromDecoded(repo, blocks)
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

// WithFunctions adds provider-supplied HCL functions.
func WithFunctions(provider detector.FunctionProvider) detector.GraphOption {
	return func(options *detector.GraphOptions) { options.Functions = provider }
}

// Graph builds the HCL detector graph using the supplied options.
func Graph(ctx context.Context, repo *attegit.Repo, options ...detector.GraphOption) (*graph.Graph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config := detector.GraphOptions{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return graphFor(ctx, repo, config)
}

func graphFor(ctx context.Context, repo *attegit.Repo, options detector.GraphOptions) (*graph.Graph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	evaluator, err := newEvaluator(ctx, repo, options.Functions)
	if err != nil {
		return nil, err
	}
	globals, err := evaluator.resolveGlobals()
	if err != nil {
		return nil, err
	}
	blocks, err := evaluator.decodedBlocks(globals)
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
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := addDecodedBlockGraph(ctx, repo, block, addEntity, options.AttachToTree)
		if err != nil {
			return nil, err
		}
		relations = append(relations, r...)
	}
	return graph.New(entities, relations)
}

// addDecodedBlockGraph builds graph entities and relations for one decoded block.
func addDecodedBlockGraph(ctx context.Context, repo *attegit.Repo, block decodedBlock, addEntity func(graph.Entity), containment bool) ([]graph.Relationship, error) {
	relations := make([]graph.Relationship, 0, 1+len(block.deps)*2)
	id := EntityID(block.kind, block.file, block.name)
	addEntity(graph.Entity{ID: id, Kind: block.kind})
	if containment {
		addEntity(graph.Entity{ID: attegit.EntityID(block.file.Tree()), Kind: attegit.TreeKind})
		addEntity(graph.Entity{ID: attegit.EntityID(block.file), Kind: attegit.BlobKind})
		relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(block.file), Kind: SourceFileRelation})
		relations = append(relations, graph.Relationship{From: attegit.EntityID(block.file.Tree()), To: id, Kind: attegit.ContainsRelation})
	}
	script := strings.TrimPrefix(block.script, DecodingPathPrefix)
	if script != "" {
		targetBlob, err := reference.ResolveBlobFromBlob(block.file, reference.SomePath(script))
		if err != nil {
			return nil, fmt.Errorf("%q: %w", block.file, err)
		}
		if obj, ok := repo.Obj[targetBlob]; !ok || obj.Kind != attegit.Blob {
			return nil, fmt.Errorf("%q: script %q not found", block.file, targetBlob)
		}
		addEntity(graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
		relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(targetBlob), Kind: ScriptRelation})
	}
	for _, dep := range block.deps {
		if dep.entity != "" {
			addEntity(graph.Entity{ID: dep.entity, Kind: dependencyKind(ctx, repo, dep.entity)})
			relations = append(relations, graph.Relationship{From: id, To: dep.entity, Kind: DependsOnRelation})
			continue
		}
		value := dep.value
		targetBlob, err := reference.ResolveBlobFromBlob(block.file, reference.SomePath(value))
		if err != nil {
			return nil, fmt.Errorf("%q: %w", block.file, err)
		}
		obj, ok := repo.Obj[targetBlob]
		if !ok || obj.Kind != attegit.Blob {
			return nil, fmt.Errorf("%q: dependency %q not found", block.file, targetBlob)
		}
		addEntity(graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
		relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(targetBlob), Kind: DependsOnRelation})
	}
	return relations, nil
}

func dependencyKind(ctx context.Context, repo *attegit.Repo, id graph.EntityID) string {
	g, err := attego.Graph(ctx, repo)
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

func mergedHCLFunctions(ctx context.Context, repo *attegit.Repo, file reference.Blob, provider detector.FunctionProvider) (map[string]function.Function, error) {
	functions := map[string]function.Function{
		"gopkg_test": gopkgTestFunction(ctx, repo),
	}
	if provider == nil {
		return functions, nil
	}
	extra, err := provider(ctx, repo, file)
	if err != nil {
		return nil, err
	}
	for name, fn := range extra {
		if _, exists := functions[name]; exists {
			return nil, fmt.Errorf("HCL function %q is already registered", name)
		}
		functions[name] = fn
	}
	return functions, nil
}

func gopkgTestFunction(ctx context.Context, repo *attegit.Repo) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, ret cty.Type) (cty.Value, error) {
			g, err := attego.Graph(ctx, repo)
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

func decodeTargetBlock(repo *attegit.Repo, file reference.Blob, body *hclsyntax.Body, ctx *hcl.EvalContext) (string, []dependency, error) {
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
		if v.Type() == attegit.RepositoryPathType {
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
				if v.Type() == attegit.RepositoryPathType {
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
