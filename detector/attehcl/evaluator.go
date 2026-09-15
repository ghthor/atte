package attehcl

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

type evaluator struct {
	ctx          context.Context
	repo         *attegit.Repo
	files        hclFiles
	scanner      targetEvaluationCapabilities
	functions    map[reference.Blob]map[string]function.Function
	kindSpecs    map[Kind]TargetKindSpec
	declarations declarationIndex
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
	Spec    TargetKindSpec
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

func newEvaluator(ctx context.Context, repo *attegit.Repo, scanner targetEvaluationCapabilities) (*evaluator, error) {
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
	return newEvaluatorWithFiles(ctx, repo, files, scanner), nil
}

func newEvaluatorForFile(
	ctx context.Context,
	repo *attegit.Repo,
	file reference.Blob,
	scanner targetEvaluationCapabilities,
) (*evaluator, error) {
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
	if _, ok := files[file]; !ok {
		files[file] = &hclFile{
			file:   file,
			body:   &hclsyntax.Body{},
			locals: make(map[string]hcl.Expression),
		}
	}
	return newEvaluatorWithFiles(ctx, repo, files, scanner), nil
}

func newEvaluatorWithFiles(
	ctx context.Context,
	repo *attegit.Repo,
	files hclFiles,
	scanner targetEvaluationCapabilities,
) *evaluator {
	return &evaluator{
		ctx:       ctx,
		repo:      repo,
		files:     files,
		scanner:   scanner,
		functions: make(map[reference.Blob]map[string]function.Function),
		kindSpecs: targetKinds(scanner),
	}
}

func (e *evaluator) forEachFile(only []reference.Blob, visit func(reference.Blob, *hclFile) error) error {
	files := e.files.sortedBlobs()
	if len(only) > 0 {
		files = only
	}
	for _, file := range files {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		config, ok := e.files[file]
		if !ok {
			continue
		}
		if err := visit(file, config); err != nil {
			return err
		}
	}
	return nil
}

func (e *evaluator) initializeDeclarations() error {
	targets, err := e.declaredTargets()
	if err != nil {
		return err
	}
	declarations := declarationIndex{
		byReference: make(map[targetReference]targetDeclaration, len(targets)),
		byID:        make(map[graph.EntityID]targetDeclaration, len(targets)),
	}
	for _, target := range targets {
		declaration := target
		declarations.byID[declaration.ID] = declaration
		if declaration.Name != "" {
			declarations.byReference[targetReference{
				file: declaration.File,
				kind: declaration.Kind,
				name: declaration.Name,
			}] = declaration
		}
	}
	e.declarations = declarations
	return nil
}

func (e *evaluator) hclFunctions(file reference.Blob) (map[string]function.Function, error) {
	if functions, ok := e.functions[file]; ok {
		return functions, nil
	}
	functions, err := mergedHCLFunctions(e.ctx, e.repo, file, e.scanner)
	if err != nil {
		return nil, err
	}
	if err := e.registerInternalFunctions(file, functions); err != nil {
		return nil, err
	}
	e.functions[file] = functions
	return functions, nil
}

func (e *evaluator) registerInternalFunctions(file reference.Blob, functions map[string]function.Function) error {
	target := targetHCLFunction(file, e.declarations)
	if _, exists := functions["atte::target"]; exists {
		return fmt.Errorf("HCL function %q is already registered", "atte::target")
	}
	functions["atte::target"] = target
	if _, exists := functions["target"]; !exists {
		functions["target"] = target
	}
	return nil
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
	identity := targetIdentityFor(file, item.kind, item.name, item.index)
	return targetDeclaration{
		ID:     identity.ID,
		Kind:   item.kind,
		File:   file,
		Name:   item.name,
		Index:  item.index,
		Source: item.block.Range(),
	}
}

func targetIDFromDeclaration(declaration targetDeclaration) graphtarget.ID {
	identity := targetIdentityFor(declaration.File, declaration.Kind, declaration.Name, declaration.Index)
	return graphtarget.ID{
		ID:        identity.ID,
		Namespace: graphtarget.Namespace(Namespace),
		Kind:      identity.Kind,
		Path:      identity.Selector.Path,
		Name:      identity.DisplayName,
		Index:     identity.Selector.Index,
		Aliases:   identity.Aliases,
	}
}

func (e *evaluator) declaredTargets(only ...reference.Blob) ([]targetDeclaration, error) {
	declarations := make([]targetDeclaration, 0)
	phase := declarationsPhase{evaluator: e}
	err := e.forEachFile(only, func(file reference.Blob, config *hclFile) error {
		normalized, err := phase.normalizeBlocks(config)
		if err != nil {
			return err
		}
		config.normalized = normalized
		for _, item := range normalized {
			if err := e.ctx.Err(); err != nil {
				return err
			}
			declarations = append(declarations, declarationFromBlock(file, item))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return declarations, nil
}

func (e *evaluator) evaluatedTargets(only ...reference.Blob) ([]evaluatedTarget, error) {
	if err := e.initializeDeclarations(); err != nil {
		return nil, err
	}
	blocks := make([]evaluatedTarget, 0)
	phase := targetsPhase{evaluator: e}
	err := e.forEachFile(only, func(_ reference.Blob, config *hclFile) error {
		fileBlocks, err := phase.evaluatedTargets(config)
		if err != nil {
			return err
		}
		blocks = append(blocks, fileBlocks...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

func (p targetsPhase) evaluatedTargets(file *hclFile) ([]evaluatedTarget, error) {
	normalized := file.normalized
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
			if diagnostic, ok := errors.AsType[targetDiagnosticsError](err); ok {
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
	scanner FunctionCapabilities,
) (map[string]function.Function, error) {
	functions := baseHCLFunctions(file)
	if scanner == nil {
		return functions, nil
	}
	extra, err := scanner.HCLFunctions(ctx, repo, file)
	if err != nil {
		return nil, err
	}
	for name, fn := range extra {
		if _, exists := functions[name]; exists {
			return nil, fmt.Errorf("HCL function %q is already registered", name)
		}
		functions[name] = fn
		namespaced := "atte::" + name
		if _, exists := functions[namespaced]; exists {
			return nil, fmt.Errorf("HCL function %q is already registered", namespaced)
		}
		functions[namespaced] = fn
	}
	return functions, nil
}
