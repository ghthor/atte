package attehcl

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

const (
	localVariable = "local"
)

type hclFile struct {
	file   reference.Blob
	body   *hclsyntax.Body
	locals map[string]hcl.Expression
}

type hclFiles map[reference.Blob]*hclFile

func (files hclFiles) sortedBlobs() []reference.Blob {
	return slices.Sorted(maps.Keys(files))
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
		for name, attribute := range block.Body.Attributes {
			if _, exists := locals[name]; exists {
				return nil, fmt.Errorf("decode HCL %q: duplicate locals attribute %q", file, name)
			}
			locals[name] = attribute.Expr
		}
	}
	return locals, nil
}

func readHCLFiles(repo *attegit.Repo) (hclFiles, error) {
	files := make(hclFiles)
	for _, objectPath := range repo.ObjKeys {
		if repo.Obj[objectPath].Kind != attegit.Blob || path.Base(objectPath.String()) != Filename {
			continue
		}
		file, err := reference.ParseBlob(objectPath.String())
		if err != nil {
			return nil, fmt.Errorf("invalid HCL file path %q: %w", objectPath, err)
		}
		parsed, err := readHCLFile(repo, file)
		if err != nil {
			return nil, err
		}
		files[file] = parsed
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
	parsed, diagnostics := hclparse.NewParser().ParseHCL(contents, file.String())
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("parse HCL %q: %s", file, diagnostics.Error())
	}
	body, ok := parsed.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse HCL %q: unsupported body type %T", file, parsed.Body)
	}
	return body, nil
}

type declaration struct {
	TargetDeclaration
	block *hclsyntax.Block
}

// normalizeBlocks is the declaration phase. It only validates the block
// header and assigns identity metadata; target body attributes are untouched.
func (e *evaluator) normalizeBlocks(file *hclFile) ([]declaration, error) {
	blocks := make([]declaration, 0, len(file.body.Blocks))
	kindIndexes := make(map[Kind]int, len(e.kindSpecs))
	named := make(map[Kind]map[string]struct{}, len(e.kindSpecs))
	for _, block := range file.body.Blocks {
		if err := e.checkContext(); err != nil {
			return nil, err
		}
		if block.Type == "locals" {
			continue
		}
		kind, name, err := e.normalizeBlock(block)
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
		id := EntityID(Namespace+":"+string(kind), file.file, displayName(name, index))
		blocks = append(blocks, declaration{
			TargetDeclaration: TargetDeclaration{ID: id, Kind: kind, File: file.file, Name: name, Index: index, Source: block.Range()},
			block:             block,
		})
	}
	return blocks, nil
}

func (e *evaluator) normalizeBlock(block *hclsyntax.Block) (Kind, string, error) {
	kindName := block.Type
	labelOffset := 0
	if block.Type == "target" {
		if len(block.Labels) < 1 || len(block.Labels) > 2 {
			return "", "", fmt.Errorf("target block must have one or two labels")
		}
		kindName = block.Labels[0]
		labelOffset = 1
	}
	kind := Kind(kindName)
	if _, registered := e.kindSpecs[kind]; !registered {
		return "", "", fmt.Errorf("unknown target kind %q", kindName)
	}
	if len(block.Labels)-labelOffset > 1 {
		return "", "", fmt.Errorf("target %s has too many labels", kindName)
	}
	name := ""
	if len(block.Labels) > labelOffset {
		name = block.Labels[labelOffset]
		if isNumericName(name) {
			return "", "", fmt.Errorf("target %s name %q must not be numeric", kindName, name)
		}
	}
	return kind, name, nil
}

type evaluator struct {
	ctx       context.Context
	repo      *attegit.Repo
	files     hclFiles
	provider  graphset.FunctionProvider
	functions map[reference.Blob]map[string]function.Function
	kindSpecs map[Kind]targetKindSpec
}

func newEvaluator(ctx context.Context, repo *attegit.Repo, provider graphset.FunctionProvider) (*evaluator, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, err := readHCLFiles(repo)
	if err != nil {
		return nil, err
	}
	return &evaluator{ctx: ctx, repo: repo, files: files, provider: provider, functions: make(map[reference.Blob]map[string]function.Function), kindSpecs: targetRegistrySnapshot()}, nil
}

func newEvaluatorForFile(ctx context.Context, repo *attegit.Repo, file reference.Blob, provider graphset.FunctionProvider) (*evaluator, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parsed := &hclFile{file: file, body: &hclsyntax.Body{}, locals: make(map[string]hcl.Expression)}
	if object, ok := repo.Obj[file]; ok && object.Kind == attegit.Blob {
		var err error
		parsed, err = readHCLFile(repo, file)
		if err != nil {
			return nil, err
		}
	}
	return &evaluator{ctx: ctx, repo: repo, files: hclFiles{file: parsed}, provider: provider, functions: make(map[reference.Blob]map[string]function.Function), kindSpecs: targetRegistrySnapshot()}, nil
}

func (e *evaluator) checkContext() error {
	if e.ctx == nil {
		return nil
	}
	return e.ctx.Err()
}

func (e *evaluator) hclFunctions(file reference.Blob) (map[string]function.Function, error) {
	if functions, ok := e.functions[file]; ok {
		return functions, nil
	}
	if err := e.checkContext(); err != nil {
		return nil, err
	}
	functions := make(map[string]function.Function)
	if e.provider != nil {
		extra, err := e.provider(e.ctx, e.repo, file)
		if err != nil {
			return nil, err
		}
		for name, fn := range extra {
			if _, exists := functions[name]; exists {
				return nil, fmt.Errorf("HCL function %q is already registered", name)
			}
			functions[name] = fn
		}
	}
	e.functions[file] = functions
	return functions, nil
}

func localContext(values map[string]cty.Value, functions map[string]function.Function) *hcl.EvalContext {
	return &hcl.EvalContext{Variables: map[string]cty.Value{localVariable: objectValue(values)}, Functions: functions}
}

func evaluateLocals(file *hclFile, expressions map[string]hcl.Expression, functions map[string]function.Function) (map[string]cty.Value, error) {
	values := make(map[string]cty.Value, len(expressions))
	pending := make(map[string]hcl.Expression, len(expressions))
	for name, expression := range expressions {
		pending[name] = expression
	}
	for len(pending) > 0 {
		progress := false
		var lastName string
		var lastDiagnostics hcl.Diagnostics
		for name, expression := range pending {
			lastName = name
			value, diagnostics := expression.Value(localContext(values, functions))
			if diagnostics.HasErrors() {
				lastDiagnostics = diagnostics
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
			return nil, fmt.Errorf("decode HCL %q local.%s: %s", file.file, lastName, lastDiagnostics.Error())
		}
	}
	return values, nil
}

func (e *evaluator) localsFor(file *hclFile) (map[string]cty.Value, error) {
	functions, err := e.hclFunctions(file.file)
	if err != nil {
		return nil, err
	}
	return evaluateLocals(file, file.locals, functions)
}

func (e *evaluator) decodeTargets(file *hclFile) ([]evaluatedTarget, error) {
	declarations, err := e.normalizeBlocks(file)
	if err != nil {
		return nil, err
	}
	locals, err := e.localsFor(file)
	if err != nil {
		return nil, err
	}
	functions, err := e.hclFunctions(file.file)
	if err != nil {
		return nil, err
	}
	ctx := localContext(locals, functions)
	targets := make([]evaluatedTarget, 0, len(declarations))
	for _, item := range declarations {
		if err := e.checkContext(); err != nil {
			return nil, err
		}
		spec := e.kindSpecs[item.Kind]
		content, diagnostics := item.block.Body.Content(&spec.schema)
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("decode HCL %q target %s.%s at %s: %w", file.file, item.Kind, displayName(item.Name, item.Index), item.block.Range().String(), hclDiagnosticError(e.repo, file.file, diagnostics))
		}
		decoded, err := spec.decoder(content, ctx)
		if err != nil {
			var diagnostic targetDiagnosticsError
			if errors.As(err, &diagnostic) {
				return nil, fmt.Errorf("decode HCL %q target %s.%s at %s: %w", file.file, item.Kind, displayName(item.Name, item.Index), item.block.Range().String(), hclDiagnosticError(e.repo, file.file, diagnostic.diagnostics))
			}
			return nil, fmt.Errorf("decode HCL %q target %s.%s at %s: %w", file.file, item.Kind, displayName(item.Name, item.Index), item.block.Range().String(), err)
		}
		if err := validateDecoderResult(item.Kind, decoded); err != nil {
			return nil, fmt.Errorf("decode HCL %q target %s.%s at %s: %w", file.file, item.Kind, displayName(item.Name, item.Index), item.block.Range().String(), err)
		}
		targets = append(targets, evaluatedTarget{declaration: item, Decoded: decoded})
	}
	return targets, nil
}

type evaluatedTarget struct {
	declaration
	Decoded any
}

func (e *evaluator) evaluatedTargets(only ...reference.Blob) ([]evaluatedTarget, error) {
	files := e.files.sortedBlobs()
	if len(only) > 0 {
		files = only
	}
	result := make([]evaluatedTarget, 0)
	for _, file := range files {
		if err := e.checkContext(); err != nil {
			return nil, err
		}
		config, ok := e.files[file]
		if !ok {
			continue
		}
		decoded, err := e.decodeTargets(config)
		if err != nil {
			return nil, err
		}
		result = append(result, decoded...)
	}
	return result, nil
}

// DeclaredTargets performs only parsing and target declaration discovery.
func DeclaredTargets(ctx context.Context, repo *attegit.Repo) ([]TargetDeclaration, error) {
	evaluator, err := newEvaluator(ctx, repo, nil)
	if err != nil {
		return nil, err
	}
	result := make([]TargetDeclaration, 0)
	for _, file := range evaluator.files.sortedBlobs() {
		if err := evaluator.checkContext(); err != nil {
			return nil, err
		}
		declarations, err := evaluator.normalizeBlocks(evaluator.files[file])
		if err != nil {
			return nil, err
		}
		for _, declaration := range declarations {
			result = append(result, declaration.TargetDeclaration)
		}
	}
	return result, nil
}

func hclDiagnosticError(repo *attegit.Repo, file reference.Blob, diagnostics hcl.Diagnostics) error {
	if len(diagnostics) == 0 {
		return fmt.Errorf("decode HCL %q: no diagnostics", file)
	}
	parts := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic == nil {
			continue
		}
		location := file.String()
		context := ""
		if diagnostic.Subject != nil {
			location = diagnostic.Subject.String()
			context = hclDiagnosticContext(repo, file, *diagnostic.Subject)
		}
		message := diagnostic.Summary
		if diagnostic.Detail != "" {
			message += ": " + diagnostic.Detail
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
