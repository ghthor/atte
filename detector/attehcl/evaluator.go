package attehcl

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

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
	Spec    targetKindSpec
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

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	return ctx.Err()
}

func newEvaluator(ctx context.Context, repo *attegit.Repo, provider graphset.FunctionProvider) (*evaluator, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	if err := checkContext(ctx); err != nil {
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
	if err := checkContext(ctx); err != nil {
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

func (e *evaluator) evalContext(file reference.Blob, scope hclScope) (*hcl.EvalContext, error) {
	functions, err := e.hclFunctions(file)
	if err != nil {
		return nil, err
	}
	variables := make(map[string]cty.Value, len(scope.targets)+1)
	variables["local"] = scope.local
	maps.Copy(variables, scope.targets)
	return &hcl.EvalContext{Variables: variables, Functions: functions}, nil
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
	scope, err := localsPhase(p).scopeFor(file, normalized)
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
		content, diagnostics := body.Content(spec.Schema)
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf(
				"decode HCL %q target %s.%s at %s: %w",
				file.file,
				kind,
				displayName(name, index),
				item.block.Range().String(),
				hclDiagnosticError(p.evaluator.repo, file.file, diagnostics),
			)
		}
		decoded, err := spec.Decoder(content, ctx)
		if err != nil {
			var diagnostic targetDiagnosticsError
			if errors.As(err, &diagnostic) {
				return nil, fmt.Errorf(
					"decode HCL %q target %s.%s at %s: %w",
					file.file,
					kind,
					displayName(name, index),
					item.block.Range().String(),
					hclDiagnosticError(p.evaluator.repo, file.file, diagnostic.diagnostics),
				)
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
			Spec:    spec,
			Source:  item.block.Range(),
		})
	}
	return targets, nil
}

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

func mergedHCLFunctions(
	ctx context.Context,
	repo *attegit.Repo,
	file reference.Blob,
	provider graphset.FunctionProvider,
) (map[string]function.Function, error) {
	functions := baseHCLFunctions(file)
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
