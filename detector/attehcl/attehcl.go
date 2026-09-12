// Package attehcl detects test, codegen, and lint definitions in atte.hcl files.
package attehcl

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
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

type dependency struct {
	value     string
	entity    graph.EntityID
	traversal hcl.Traversal
}

type targetReference struct {
	file reference.Blob
	kind Kind
	name string
}

type declarationIndex struct {
	byReference map[targetReference]targetDeclaration
	byID        map[graph.EntityID]targetDeclaration
}

type hclFile struct {
	file   reference.Blob
	body   *hclsyntax.Body
	locals map[string]hcl.Expression
}

type hclScope struct {
	local cty.Value
}

func objectValue(values map[string]cty.Value) cty.Value {
	if len(values) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(values)
}

func declarationBlocks(file reference.Blob, body *hclsyntax.Body) (map[string]hcl.Expression, error) {
	locals := make(map[string]hcl.Expression)
	for _, block := range body.Blocks {
		if block.Type == "globals" {
			return nil, fmt.Errorf("decode HCL %q: globals are not supported; targets are file-local", file)
		}
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

type hclFiles map[reference.Blob]*hclFile

func (files hclFiles) sortedBlobs() []reference.Blob {
	return slices.Sorted(maps.Keys(files))
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

func localContext(values map[string]cty.Value, functions map[string]function.Function) *hcl.EvalContext {
	return &hcl.EvalContext{Variables: map[string]cty.Value{"local": objectValue(values)}, Functions: functions}
}

func evaluateDeclaration(expression hcl.Expression, context *hcl.EvalContext) (cty.Value, hcl.Diagnostics) {
	return expression.Value(context)
}

// evaluateLocals resolves file-local declarations without consulting another
// atte.hcl file. Cross-file target dependencies are a later graph phase.
func evaluateLocals(file *hclFile, expressions map[string]hcl.Expression, functions map[string]function.Function) (map[string]cty.Value, error) {
	values := make(map[string]cty.Value, len(expressions))
	pending := make(map[string]hcl.Expression, len(expressions))
	maps.Copy(pending, expressions)
	for len(pending) > 0 {
		progress := false
		var lastName string
		var lastDiags hcl.Diagnostics
		for name, expr := range pending {
			lastName = name
			ctx := localContext(values, functions)
			value, diags := evaluateDeclaration(expr, ctx)
			if diags.HasErrors() {
				lastDiags = diags
				continue
			}
			if !value.IsKnown() {
				return nil, fmt.Errorf("decode HCL %q: local.%s must be known", file.file, name)
			}
			values[name] = value
			delete(pending, name)
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("decode HCL %q local.%s: %s", file.file, lastName, lastDiags.Error())
		}
	}
	return values, nil
}

type evaluator struct {
	ctx       context.Context
	repo      *attegit.Repo
	files     hclFiles
	provider  graphset.FunctionProvider
	functions map[reference.Blob]map[string]function.Function
	kindSpecs map[Kind]targetKindSpec
}

type normalizedBlock struct {
	block *hclsyntax.Block
	kind  Kind
	name  string
	index int
}

// targetDeclaration describes the identity and source location of a target
// without evaluating its body.
type targetDeclaration struct {
	ID     graph.EntityID
	Kind   Kind
	File   reference.Blob
	Name   string
	Index  int
	Source hcl.Range
}

type evaluatedTarget struct {
	Kind    Kind
	File    reference.Blob
	Name    string
	Label   string
	Index   int
	Decoded any
	Source  hcl.Range
}

type declarationsPhase struct {
	evaluator *evaluator
}

type localsPhase struct {
	evaluator *evaluator
}

type targetsPhase struct {
	evaluator *evaluator
}

func newEvaluator(ctx context.Context, repo *attegit.Repo, provider graphset.FunctionProvider) (*evaluator, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, err := readHCLFiles(ctx, repo)
	if err != nil {
		return nil, err
	}
	return &evaluator{
		ctx:       ctx,
		repo:      repo,
		files:     files,
		provider:  provider,
		functions: make(map[reference.Blob]map[string]function.Function),
		kindSpecs: targetRegistrySnapshot(),
	}, nil
}

func newEvaluatorForFile(ctx context.Context, repo *attegit.Repo, file reference.Blob, provider graphset.FunctionProvider) (*evaluator, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parsed := &hclFile{
		file:   file,
		body:   &hclsyntax.Body{},
		locals: make(map[string]hcl.Expression),
	}
	if object, ok := repo.Obj[file]; ok && object.Kind == attegit.Blob {
		var err error
		parsed, err = readHCLFile(repo, file)
		if err != nil {
			return nil, err
		}
	}
	return &evaluator{
		ctx:       ctx,
		repo:      repo,
		files:     hclFiles{file: parsed},
		provider:  provider,
		functions: make(map[reference.Blob]map[string]function.Function),
		kindSpecs: targetRegistrySnapshot(),
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

func (p localsPhase) scopeFor(file *hclFile) (hclScope, error) {
	functions, err := p.evaluator.hclFunctions(file.file)
	if err != nil {
		return hclScope{}, err
	}
	locals, err := evaluateLocals(file, file.locals, functions)
	if err != nil {
		return hclScope{}, err
	}
	return hclScope{local: objectValue(locals)}, nil
}

func (e *evaluator) evalContext(file reference.Blob, scope hclScope) (*hcl.EvalContext, error) {
	functions, err := e.hclFunctions(file)
	if err != nil {
		return nil, err
	}
	return &hcl.EvalContext{Variables: map[string]cty.Value{"local": scope.local}, Functions: functions}, nil
}

func declarationFromBlock(file reference.Blob, item normalizedBlock) targetDeclaration {
	return targetDeclaration{
		ID:     EntityID(Namespace+":"+string(item.kind), file, displayName(item.name, item.index)),
		Kind:   item.kind,
		File:   file,
		Name:   item.name,
		Index:  item.index,
		Source: item.block.Range(),
	}
}

func declarationFromEvaluated(target evaluatedTarget) targetDeclaration {
	return targetDeclaration{
		ID:     EntityID(Namespace+":"+string(target.Kind), target.File, displayName(target.Name, target.Index)),
		Kind:   target.Kind,
		File:   target.File,
		Name:   target.Name,
		Index:  target.Index,
		Source: target.Source,
	}
}

func targetIDFromDeclaration(declaration targetDeclaration) graphtarget.ID {
	display := displayName(declaration.Name, declaration.Index)
	return graphtarget.ID{
		ID:        declaration.ID,
		Namespace: graphtarget.Namespace(Namespace),
		Kind:      Namespace + ":" + string(declaration.Kind),
		Path:      declaration.File.String(),
		Name:      display,
		Index:     declaration.Index,
		Aliases: selector.Aliases(selector.Target{
			Path:  declaration.File.String(),
			Kind:  string(declaration.Kind),
			Name:  display,
			Index: declaration.Index,
		}),
	}
}

func (e *evaluator) declaredTargets(only ...reference.Blob) ([]targetDeclaration, error) {
	files := e.files.sortedBlobs()
	if len(only) > 0 {
		files = only
	}
	declarations := make([]targetDeclaration, 0)
	phase := declarationsPhase{evaluator: e}
	for _, file := range files {
		if err := e.ctx.Err(); err != nil {
			return nil, err
		}
		config, ok := e.files[file]
		if !ok {
			continue
		}
		normalized, err := phase.normalizeBlocks(config)
		if err != nil {
			return nil, err
		}
		for _, item := range normalized {
			if err := e.ctx.Err(); err != nil {
				return nil, err
			}
			declarations = append(declarations, declarationFromBlock(file, item))
		}
	}
	return declarations, nil
}

func (e *evaluator) evaluatedTargets(only ...reference.Blob) ([]evaluatedTarget, error) {
	files := e.files.sortedBlobs()
	if len(only) > 0 {
		files = only
	}
	blocks := make([]evaluatedTarget, 0)
	phase := targetsPhase{evaluator: e}
	for _, file := range files {
		if err := e.ctx.Err(); err != nil {
			return nil, err
		}
		config, ok := e.files[file]
		if !ok {
			continue
		}
		fileBlocks, err := phase.evaluatedTargets(config)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, fileBlocks...)
	}
	return blocks, nil
}

func (p targetsPhase) evaluatedTargets(file *hclFile) ([]evaluatedTarget, error) {
	normalized, err := declarationsPhase(p).normalizeBlocks(file)
	if err != nil {
		return nil, err
	}
	scope, err := localsPhase(p).scopeFor(file)
	if err != nil {
		return nil, err
	}
	ctx, err := p.evaluator.evalContext(file.file, scope)
	if err != nil {
		return nil, err
	}
	targets := make([]evaluatedTarget, 0, len(normalized))
	for _, item := range normalized {
		if err := p.evaluator.ctx.Err(); err != nil {
			return nil, err
		}
		kind, name, body, index := item.kind, item.name, item.block.Body, item.index
		spec := p.evaluator.kindSpecs[kind]
		content, diagnostics := body.Content(&spec.schema)
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("decode HCL %q target %s.%s at %s: %w", file.file, kind, displayName(name, index), item.block.Range().String(), hclDiagnosticError(p.evaluator.repo, file.file, diagnostics))
		}
		decoded, err := spec.decoder(content, ctx)
		if err != nil {
			var diagnostic targetDiagnosticsError
			if errors.As(err, &diagnostic) {
				return nil, fmt.Errorf("decode HCL %q target %s.%s at %s: %w", file.file, kind, displayName(name, index), item.block.Range().String(), hclDiagnosticError(p.evaluator.repo, file.file, diagnostic.diagnostics))
			}
			return nil, fmt.Errorf("decode HCL %q target %s.%s at %s: %w", file.file, kind, displayName(name, index), item.block.Range().String(), err)
		}
		if err := validateDecoderResult(kind, decoded); err != nil {
			return nil, fmt.Errorf("decode HCL %q target %s.%s at %s: %w", file.file, kind, displayName(name, index), item.block.Range().String(), err)
		}
		targets = append(targets, evaluatedTarget{
			Kind:    kind,
			File:    file.file,
			Name:    name,
			Label:   name,
			Index:   index,
			Decoded: decoded,
			Source:  item.block.Range(),
		})
	}
	return targets, nil
}

func targetsFromEvaluated(repo *attegit.Repo, evaluated []evaluatedTarget) (map[Kind][]Target, error) {
	targets := make(map[Kind][]Target, len(evaluated))
	for _, item := range evaluated {
		script, inline, err := targetScript(repo, item.File, item.Decoded)
		if err != nil {
			return nil, err
		}
		kind := string(item.Kind)
		display := displayName(item.Name, item.Index)
		aliases := selector.Aliases(selector.Target{Path: item.File.String(), Kind: kind, Name: display, Index: item.Index})
		targets[item.Kind] = append(targets[item.Kind], Target{
			ID:      EntityID(Namespace+":"+kind, item.File, display),
			Kind:    Namespace + ":" + kind,
			File:    item.File,
			Name:    item.Name,
			Label:   item.Label,
			Index:   item.Index,
			Aliases: aliases,
			Script:  script,
			Inline:  inline,
			Decoded: item.Decoded,
			Source:  item.Source,
		})
	}
	return targets, nil
}

func targetScript(repo *attegit.Repo, file reference.Blob, decoded any) (reference.Blob, string, error) {
	target, ok := decoded.(decodedTarget)
	if !ok {
		return "", "", nil
	}
	if script, ok := strings.CutPrefix(target.Script, DecodingPathPrefix); ok {
		return reference.Blob(script), "", nil
	}
	if target.Script == "" {
		return "", "", nil
	}
	trimmed := strings.TrimSpace(target.Script)
	if object, ok := repo.Obj[reference.Blob(trimmed)]; ok {
		return "", "", fmt.Errorf("decode HCL %q: script string resolves to repository %q %q; use path(%q) for an external script", file, object.Kind, trimmed, trimmed)
	}
	return "", target.Script, nil
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
	if _, registered := targetRegistrySnapshot()[Kind(parts[1])]; !registered {
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	file, err := reference.ParseBlob(parts[2])
	if err != nil {
		return "", "", "", fmt.Errorf("invalid HCL file path %q: %w", parts[2], err)
	}
	return kind, file, parts[3], nil
}

// Target describes one target declared by a registered kind in an atte.hcl
// file. Targets are returned in repository order. Named targets retain their
// source label in Name; anonymous targets retain an empty Name and use Index
// for their kind-local source position. Script is a repository-relative path
// resolved from an explicit path expression.
//
// Script is the repository-relative path resolved from an explicit path
// expression; Inline contains ordinary string script content. Both are empty
// only when the corresponding value is not present. ID is the corresponding graph entity
// identifier, and Selector converts the target to the canonical CLI selector.
//
// Kind identifies the registered target kind. File identifies the declaring
// atte.hcl file. Label preserves the HCL block label, when present, while Index
// records the block's zero-based position within its kind. Decoded contains the
// value returned by the kind's registered decoder.
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
	Decoded any
	Source  hcl.Range
}

// DisplayName returns the stable display identifier component for the target.
// Anonymous targets use their kind-local source index.
func (target Target) DisplayName() string {
	return displayName(target.Name, target.Index)
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

// Selector converts a target to its selector-facing identity.
func Selector(target Target) selector.Target {
	return selector.Target{
		Path:    target.File.String(),
		Kind:    strings.TrimPrefix(target.Kind, Namespace+":"),
		Name:    target.DisplayName(),
		Index:   target.Index,
		Aliases: target.Aliases,
	}
}

// Targets evaluates all atte.hcl files independently and groups declarations by kind.
func Targets(ctx context.Context, repo *attegit.Repo, provider graphset.FunctionProvider) (map[Kind][]Target, error) {
	return targetsWithProvider(ctx, repo, provider)
}

// DeclaredTargets returns target identities without evaluating target bodies.
// Results are ordered by repository-relative file path and source order.
func DeclaredTargets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	evaluator, err := newEvaluator(ctx, repo, nil)
	if err != nil {
		return nil, err
	}
	declarations, err := evaluator.declaredTargets()
	if err != nil {
		return nil, err
	}
	targets := make([]graphtarget.ID, 0, len(declarations))
	for i := range declarations {
		targets = append(targets, targetIDFromDeclaration(declarations[i]))
	}
	return targets, nil
}

// SortedTargets returns the targets grouped by kind in deterministic source order.
func SortedTargets(grouped map[Kind][]Target) []Target {
	kinds := make([]Kind, 0, len(grouped))
	for kind := range grouped {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	targets := make([]Target, 0)
	for _, kind := range kinds {
		targets = append(targets, grouped[kind]...)
	}
	return targets
}

func targetsWithProvider(ctx context.Context, repo *attegit.Repo, provider graphset.FunctionProvider) (map[Kind][]Target, error) {
	evaluator, err := newEvaluator(ctx, repo, provider)
	if err != nil {
		return nil, err
	}
	blocks, err := evaluator.evaluatedTargets()
	if err != nil {
		return nil, err
	}
	return targetsFromEvaluated(repo, blocks)
}

// Config is the evaluated target configuration for a repository directory.
type Config struct {
	Targets map[Kind][]Target
}

// ConfigFor evaluates the file-local target configuration for a repository-relative directory.
func ConfigFor(ctx context.Context, repo *attegit.Repo, relativePath string, provider graphset.FunctionProvider) (Config, error) {
	return configForWithProvider(ctx, repo, relativePath, provider)
}

func configForWithProvider(ctx context.Context, repo *attegit.Repo, relativePath string, provider graphset.FunctionProvider) (Config, error) {
	if repo == nil {
		return Config{}, fmt.Errorf("repository is nil")
	}
	tree, err := reference.ParseTree(relativePath)
	if err != nil {
		return Config{}, fmt.Errorf("invalid repository directory %q: %w", relativePath, err)
	}
	currentBlob, err := tree.Blob(Filename)
	if err != nil {
		return Config{}, err
	}
	evaluator, err := newEvaluatorForFile(ctx, repo, currentBlob, provider)
	if err != nil {
		return Config{}, err
	}
	blocks, err := evaluator.evaluatedTargets(currentBlob)
	if err != nil {
		return Config{}, err
	}
	allTargets, err := targetsFromEvaluated(repo, blocks)
	if err != nil {
		return Config{}, err
	}
	targets := make(map[Kind][]Target, len(allTargets))
	for kind, kindTargets := range allTargets {
		for _, target := range kindTargets {
			if target.File.Tree() == tree {
				targets[kind] = append(targets[kind], target)
			}
		}
	}
	return Config{Targets: targets}, nil
}

// WithFunctions adds provider-supplied HCL functions.
func WithFunctions(provider graphset.FunctionProvider) graphset.Option {
	return func(options *graphset.Options) { options.Functions = provider }
}

// Graph builds the HCL detector graph using the supplied options.
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
		return nil, fmt.Errorf("repository is nil")
	}
	evaluator, err := newEvaluator(ctx, repo, options.Functions)
	if err != nil {
		return nil, err
	}
	blocks, err := evaluator.evaluatedTargets()
	if err != nil {
		return nil, err
	}
	entities := make([]graph.Entity, 0)
	seenEntities := make(map[graph.EntityID]struct{})
	relations := make([]graph.Relationship, 0)
	declarations := declarationIndex{
		byReference: make(map[targetReference]targetDeclaration, len(blocks)),
		byID:        make(map[graph.EntityID]targetDeclaration, len(blocks)),
	}
	for i := range blocks {
		declaration := declarationFromEvaluated(blocks[i])
		declarations.byID[declaration.ID] = declaration
		if declaration.Name != "" {
			declarations.byReference[targetReference{file: declaration.File, kind: declaration.Kind, name: declaration.Name}] = declaration
		}
	}
	addEntity := func(e graph.Entity) {
		if _, exists := seenEntities[e.ID]; exists {
			return
		}
		seenEntities[e.ID] = struct{}{}
		entities = append(entities, e)
	}
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := addEvaluatedTargetGraph(ctx, repo, block, declarations, addEntity, options.AttachToTree)
		if err != nil {
			return nil, err
		}
		relations = append(relations, r...)
	}
	return graph.New(entities, relations)
}

// addEvaluatedTargetGraph builds graph entities and relations for one decoded block.
func addEvaluatedTargetGraph(ctx context.Context, repo *attegit.Repo, target evaluatedTarget, declarations declarationIndex, addEntity func(graph.Entity), containment bool) ([]graph.Relationship, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	relations := make([]graph.Relationship, 0)
	id := EntityID(Namespace+":"+string(target.Kind), target.File, displayName(target.Name, target.Index))
	addEntity(graph.Entity{ID: id, Kind: Namespace + ":" + string(target.Kind)})
	if containment {
		addEntity(graph.Entity{ID: attegit.EntityID(target.File.Tree()), Kind: attegit.TreeKind})
		addEntity(graph.Entity{ID: attegit.EntityID(target.File), Kind: attegit.BlobKind})
		relations = append(relations,
			graph.Relationship{From: id, To: attegit.EntityID(target.File), Kind: SourceFileRelation},
			graph.Relationship{From: attegit.EntityID(target.File.Tree()), To: id, Kind: attegit.ContainsRelation},
		)
	}
	decoded, ok := target.Decoded.(decodedTarget)
	if !ok {
		return relations, nil
	}
	if decoded.Script == "" {
		return nil, fmt.Errorf("target %q has no script", target.Name)
	}
	relations = make([]graph.Relationship, 0, 2+len(decoded.Deps)*2)
	if containment {
		relations = append(relations,
			graph.Relationship{From: id, To: attegit.EntityID(target.File), Kind: SourceFileRelation},
			graph.Relationship{From: attegit.EntityID(target.File.Tree()), To: id, Kind: attegit.ContainsRelation},
		)
	}
	script := strings.TrimPrefix(decoded.Script, DecodingPathPrefix)
	if script != "" {
		targetBlob, err := reference.ResolveBlobFromBlob(target.File, reference.SomePath(script))
		if err != nil {
			return nil, fmt.Errorf("%q: %w", target.File, err)
		}
		if obj, ok := repo.Obj[targetBlob]; !ok || obj.Kind != attegit.Blob {
			return nil, fmt.Errorf("%q: script %q not found", target.File, targetBlob)
		}
		addEntity(graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
		relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(targetBlob), Kind: ScriptRelation})
	}
	for _, dep := range decoded.Deps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if dep.traversal != nil {
			declaration, err := resolveTargetTraversal(target.File, dep.traversal, declarations)
			if err != nil {
				return nil, err
			}
			addEntity(graph.Entity{ID: declaration.ID, Kind: Namespace + ":" + string(declaration.Kind)})
			relations = append(relations, graph.Relationship{From: id, To: declaration.ID, Kind: DependsOnRelation})
			continue
		}
		if dep.entity != "" {
			kind, err := entityDependencyKind(dep.entity)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(string(dep.entity), Namespace+":") {
				if _, ok := declarations.byID[dep.entity]; !ok {
					return nil, fmt.Errorf("dependency target %q not declared", dep.entity)
				}
			}
			addEntity(graph.Entity{ID: dep.entity, Kind: kind})
			relations = append(relations, graph.Relationship{From: id, To: dep.entity, Kind: DependsOnRelation})
			continue
		}
		value := strings.TrimPrefix(dep.value, DecodingPathPrefix)
		targetBlob, err := reference.ResolveBlobFromBlob(target.File, reference.SomePath(value))
		if err != nil {
			return nil, fmt.Errorf("%q: %w", target.File, err)
		}
		obj, ok := repo.Obj[targetBlob]
		if !ok || obj.Kind != attegit.Blob {
			return nil, fmt.Errorf("%q: dependency %q not found", target.File, targetBlob)
		}
		addEntity(graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
		relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(targetBlob), Kind: DependsOnRelation})
	}
	return relations, nil
}

func resolveTargetTraversal(file reference.Blob, traversal hcl.Traversal, declarations declarationIndex) (targetDeclaration, error) {
	if len(traversal) != 2 {
		return targetDeclaration{}, fmt.Errorf("%q: target dependency must be a kind.name traversal", file)
	}
	attribute, ok := traversal[1].(hcl.TraverseAttr)
	if !ok {
		return targetDeclaration{}, fmt.Errorf("%q: target dependency must be a kind.name traversal", file)
	}
	kind := Kind(traversal.RootName())
	declaration, ok := declarations.byReference[targetReference{file: file, kind: kind, name: attribute.Name}]
	if !ok {
		return targetDeclaration{}, fmt.Errorf("%q: target dependency %s.%s not found in the same file", file, kind, attribute.Name)
	}
	return declaration, nil
}

func entityDependencyKind(id graph.EntityID) (string, error) {
	value := string(id)
	if strings.HasPrefix(value, "attego:") {
		kind, _, _, err := attego.DecodeEntityID(id)
		if err != nil {
			return "", fmt.Errorf("decode Go dependency entity %q: %w", id, err)
		}
		return kind, nil
	}
	if strings.HasPrefix(value, Namespace+":") {
		kind, _, _, err := DecodeEntityID(id)
		if err != nil {
			return "", fmt.Errorf("decode HCL dependency entity %q: %w", id, err)
		}
		return kind, nil
	}
	return "", fmt.Errorf("dependency entity %q has unknown namespace", id)
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

// normalizeBlocks discovers target declarations in source order and assigns
// kind-local indices before any target body is decoded.
func (p declarationsPhase) normalizeBlocks(file *hclFile) ([]normalizedBlock, error) {
	blocks := make([]normalizedBlock, 0, len(file.body.Blocks))
	kindIndexes := make(map[Kind]int, len(p.evaluator.kindSpecs))
	named := make(map[Kind]map[string]struct{}, len(p.evaluator.kindSpecs))
	for _, block := range file.body.Blocks {
		if err := p.evaluator.ctx.Err(); err != nil {
			return nil, err
		}
		if block.Type == "locals" {
			continue
		}
		kind, name, _, err := p.normalizeBlock(block)
		if err != nil {
			return nil, fmt.Errorf("parse HCL %q target at %s: %w", file.file, block.Range().String(), err)
		}
		index := kindIndexes[kind]
		kindIndexes[kind]++
		if name != "" {
			if named[kind] == nil {
				named[kind] = make(map[string]struct{})
			}
			if _, exists := named[kind][name]; exists {
				return nil, fmt.Errorf("parse HCL %q: duplicate %s label %q at %s", file.file, kind, name, block.Range().String())
			}
			named[kind][name] = struct{}{}
		}
		blocks = append(blocks, normalizedBlock{block: block, kind: kind, name: name, index: index})
	}
	return blocks, nil
}

func (p declarationsPhase) normalizeBlock(block *hclsyntax.Block) (Kind, string, *hclsyntax.Body, error) {
	kindName := block.Type
	labelOffset := 0
	if block.Type == "target" {
		if len(block.Labels) < 1 || len(block.Labels) > 2 {
			return "", "", nil, fmt.Errorf("target block must have one or two labels")
		}
		kindName = block.Labels[0]
		labelOffset = 1
	}
	kind := Kind(kindName)
	if _, registered := p.evaluator.kindSpecs[kind]; !registered {
		return "", "", nil, fmt.Errorf("unknown target kind %q", kindName)
	}
	if len(block.Labels)-labelOffset > 1 {
		return "", "", nil, fmt.Errorf("target %s has too many labels", kindName)
	}
	name := ""
	if len(block.Labels) > labelOffset {
		name = block.Labels[labelOffset]
		if isNumericName(name) {
			return "", "", nil, fmt.Errorf("target %s name %q must not be numeric", kindName, name)
		}
	}
	return kind, name, block.Body, nil
}

func displayName(name string, index int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("%d", index)
}

func isNumericName(name string) bool {
	return name != "" && strings.Trim(name, "0123456789") == ""
}

func mergedHCLFunctions(ctx context.Context, repo *attegit.Repo, file reference.Blob, provider graphset.FunctionProvider) (map[string]function.Function, error) {
	functions := make(map[string]function.Function)
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

func hclDiagnosticError(repo *attegit.Repo, file reference.Blob, diags hcl.Diagnostics) error {
	if len(diags) == 0 {
		return fmt.Errorf("decode HCL %q: no diagnostics", file)
	}

	parts := make([]string, 0, len(diags))
	for _, diag := range diags {
		if diag == nil {
			continue
		}
		location := file.String()
		context := ""
		if diag.Subject != nil {
			location = diag.Subject.String()
			context = hclDiagnosticContext(repo, file, *diag.Subject)
		}
		message := diag.Summary
		if diag.Detail != "" {
			message += ": " + diag.Detail
		}
		if context != "" {
			message = context + "\n" + message
		}
		parts = append(parts, fmt.Sprintf("decode HCL %q: %s:\n%s", file, location, message))
	}
	if len(parts) == 0 {
		return fmt.Errorf("decode HCL %q: no diagnostics", file)
	}
	return errors.New(strings.Join(parts, "\n"))
}

func hclDiagnosticContext(repo *attegit.Repo, file reference.Blob, subject hcl.Range) string {
	if repo == nil || subject.Start.Line < 1 {
		return ""
	}
	contents, err := repo.Show(file)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(contents), "\n")
	lineIndex := subject.Start.Line - 1
	if lineIndex >= len(lines) {
		return ""
	}
	line := lines[lineIndex]
	start := max(subject.Start.Column-1, 0)
	end := subject.End.Column - 1
	if end <= start {
		end = start + 1
	}
	marker := strings.Repeat(" ", start) + strings.Repeat("^", end-start)
	return fmt.Sprintf("  %d | %s\n    | %s", subject.Start.Line, line, marker)
}
