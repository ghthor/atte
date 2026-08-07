// full demonstrates the proposed cross-file HCL evaluation pipeline.
//
// The experiment has two phases:
//
//   - the global phase evaluates globals in repository-tree order, applying
//     file-local overrides to inherited values;
//   - the local phase evaluates locals and target attributes with the
//     effective globals and a complete repository_paths context.
//
// Repository target declarations are discovered from syntax before either
// phase evaluates expressions. Their possible values are therefore known
// without a queued cross-file evaluation: a reference points to a declaration
// whose file, kind, label, and stable identity are already known.
package main

import (
	"context"
	"fmt"
	"log"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

const (
	rootFile  = "atte.hcl"
	goFile    = "src/go/atte.hcl"
	pyFile    = "src/py/atte.hcl"
	zigFile   = "src/zig/atte.hcl"
	basicFile = "src/basic/atte.hcl"
	testAll   = "src/test_all/atte.hcl"
)

var repository = map[string]string{
	rootFile: `globals {
  release = "root"
  version = "1"
}`,
	goFile: `globals {
  version = "${global.version}.go"
}

test "go" {}`,
	pyFile: `test "py" {
  depends_on = [
    "//src/go".test.go,
    "//src/zig".test.zig,
  ]
}`,
	zigFile: `test "zig" {
  depends_on = [
    "//src/go".test.go,
  ]
}`,
	basicFile: `test {}

test "labeled" {
  depends_on = []
}`,
	testAll: `locals {
  depends_on = [
    "//src/go".test.go,
    "//src/py".test.py,
  ]
}

test {
  depends_on = local.depends_on
}

test "two" {
  depends_on = local.depends_on
}`,
}

type hclFile struct {
	name    string
	body    *hclsyntax.Body
	globals map[string]hcl.Expression
	locals  map[string]hcl.Expression
}

type target struct {
	file     string
	kind     string
	label    string
	address  string
	entityID string
}

type pathNamespace struct {
	path      string
	labeled   map[string]target
	unlabeled *target
	ctyValue  cty.Value
}

type evaluator struct {
	files      map[string]*hclFile
	globals    map[string]map[string]cty.Value
	locals     map[string]map[string]cty.Value
	namespaces map[string]*pathNamespace
	context    context.Context
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	evaluator, err := newEvaluator(ctx, repository)
	if err != nil {
		log.Fatal(err)
	}
	if err := evaluator.evaluateGlobals(); err != nil {
		log.Fatal(err)
	}
	if err := evaluator.evaluateLocals(); err != nil {
		log.Fatal(err)
	}
	results, err := evaluator.evaluateTargets()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("global scopes:")
	for _, file := range sortedKeys(evaluator.globals) {
		fmt.Printf("  %s: %s\n", file, formatValues(evaluator.globals[file]))
	}
	fmt.Println()

	fmt.Println("repository_paths:")
	for _, rooted := range sortedKeys(evaluator.namespaces) {
		namespace := evaluator.namespaces[rooted]
		fmt.Printf("  %s: %s\n", rooted, namespaceDescription(namespace))
	}
	fmt.Println()

	fmt.Println("local scopes:")
	for _, file := range sortedKeys(evaluator.locals) {
		fmt.Printf("  %s: %s\n", file, formatValues(evaluator.locals[file]))
	}
	fmt.Println()

	fmt.Println("target evaluations:")
	for _, result := range results {
		fmt.Printf("  %s %s: depends_on = [%s]\n", result.file, targetName(result), strings.Join(result.dependencies, ", "))
	}
}

type targetResult struct {
	target
	dependencies []string
}

func newEvaluator(ctx context.Context, repository map[string]string) (*evaluator, error) {
	files := make(map[string]*hclFile, len(repository))
	for name, source := range repository {
		parsed, diagnostics := hclparse.NewParser().ParseHCL([]byte(source), name)
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("parse %q: %s", name, diagnostics.Error())
		}
		body, ok := parsed.Body.(*hclsyntax.Body)
		if !ok {
			return nil, fmt.Errorf("parse %q: expected *hclsyntax.Body, got %T", name, parsed.Body)
		}
		globals, locals, err := declarations(body)
		if err != nil {
			return nil, fmt.Errorf("discover declarations in %q: %w", name, err)
		}
		files[name] = &hclFile{name: name, body: body, globals: globals, locals: locals}
	}

	namespaces, err := discoverNamespaces(files)
	if err != nil {
		return nil, err
	}
	return &evaluator{
		context:    ctx,
		files:      files,
		globals:    make(map[string]map[string]cty.Value, len(files)),
		locals:     make(map[string]map[string]cty.Value, len(files)),
		namespaces: namespaces,
	}, nil
}

func declarations(body *hclsyntax.Body) (map[string]hcl.Expression, map[string]hcl.Expression, error) {
	globals := make(map[string]hcl.Expression)
	locals := make(map[string]hcl.Expression)
	for _, block := range body.Blocks {
		var destination map[string]hcl.Expression
		switch block.Type {
		case "globals":
			destination = globals
		case "locals":
			destination = locals
		default:
			continue
		}
		for name, attribute := range block.Body.Attributes {
			if _, exists := destination[name]; exists {
				return nil, nil, fmt.Errorf("duplicate %s attribute %q", block.Type, name)
			}
			destination[name] = attribute.Expr
		}
	}
	return globals, locals, nil
}

func discoverNamespaces(files map[string]*hclFile) (map[string]*pathNamespace, error) {
	namespaces := make(map[string]*pathNamespace, len(files))
	for _, file := range files {
		rooted := rootedPath(file.name)
		namespace := &pathNamespace{path: rooted, labeled: make(map[string]target)}
		index := 0
		for _, block := range file.body.Blocks {
			if block.Type != "test" {
				continue
			}
			if len(block.Labels) > 1 {
				return nil, fmt.Errorf("discover %q: test block has too many labels", file.name)
			}
			target := target{
				file:     file.name,
				kind:     "test",
				address:  rooted + ".test",
				entityID: fmt.Sprintf("test:%s:%d", file.name, index),
			}
			if len(block.Labels) == 1 {
				target.label = block.Labels[0]
				target.address += "." + target.label
				if _, exists := namespace.labeled[target.label]; exists {
					return nil, fmt.Errorf("discover %q: duplicate test label %q", file.name, target.label)
				}
				namespace.labeled[target.label] = target
			} else {
				if namespace.unlabeled != nil {
					return nil, fmt.Errorf("discover %q: duplicate unlabeled test", file.name)
				}
				target.address = rooted + ".test"
				namespace.unlabeled = &target
			}
			index++
		}
		namespace.ctyValue = namespaceValue(namespace)
		namespaces[rooted] = namespace
	}
	return namespaces, nil
}

func namespaceValue(namespace *pathNamespace) cty.Value {
	// An unlabeled test and labeled tests share the `test` prefix but have
	// incompatible cty shapes: `test` is a terminal in one case and an object
	// in the other. The application-specific resolver handles that overlap;
	// repository_paths still exposes the complete labeled object shape.
	labels := make(map[string]cty.Value, len(namespace.labeled))
	for label, target := range namespace.labeled {
		labels[label] = cty.StringVal(target.entityID)
	}
	if len(labels) == 0 && namespace.unlabeled != nil {
		return cty.StringVal(namespace.unlabeled.entityID)
	}
	return cty.ObjectVal(map[string]cty.Value{"test": cty.ObjectVal(labels)})
}

func (e *evaluator) repositoryPathsValue() cty.Value {
	paths := make(map[string]cty.Value, len(e.namespaces))
	for rooted, namespace := range e.namespaces {
		paths[rooted] = namespace.ctyValue
	}
	return cty.ObjectVal(paths)
}

func (e *evaluator) globalContext(values map[string]cty.Value) *hcl.EvalContext {
	return &hcl.EvalContext{Variables: map[string]cty.Value{
		"global": objectValue(values),
	}}
}

func (e *evaluator) fileContext(file string, global, local map[string]cty.Value) *hcl.EvalContext {
	return &hcl.EvalContext{Variables: map[string]cty.Value{
		"global":           objectValue(global),
		"local":            objectValue(local),
		"repository_paths": e.repositoryPathsValue(),
	}}
}

func (e *evaluator) evaluateGlobals() error {
	// Evaluate every directory in repository-tree order, including directories
	// that have no atte.hcl. This preserves inheritance across missing
	// intermediate configuration files.
	directories := make(map[string]struct{})
	directories[""] = struct{}{}
	for _, file := range e.files {
		dir := dirOf(file.name)
		for {
			directories[dir] = struct{}{}
			if dir == "" {
				break
			}
			dir = parentDir(dir)
		}
	}
	ordered := sortedKeys(directories)
	sort.SliceStable(ordered, func(i, j int) bool {
		leftDepth := strings.Count(ordered[i], "/")
		rightDepth := strings.Count(ordered[j], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return ordered[i] < ordered[j]
	})

	for _, dir := range ordered {
		parent := parentDir(dir)
		inherited := cloneValues(e.globals[parent])
		name := rootFile
		if dir != "" {
			name = dir + "/" + rootFile
		}
		file, exists := e.files[name]
		if exists {
			values, err := evaluateDeclarations(e.context, file.globals, inherited, func(current map[string]cty.Value) *hcl.EvalContext {
				merged := cloneValues(inherited)
				for key, value := range current {
					merged[key] = value
				}
				return e.globalContext(merged)
			})
			if err != nil {
				return fmt.Errorf("evaluate globals in %q: %w", name, err)
			}
			for key, value := range values {
				inherited[key] = value
			}
		}
		e.globals[dir] = inherited
		if exists {
			e.globals[name] = inherited
		}
	}
	return nil
}

func (e *evaluator) evaluateLocals() error {
	for _, name := range sortedKeys(e.files) {
		file := e.files[name]
		global := e.globals[name]
		values, err := e.evaluateLocalDeclarations(file.locals, func(current map[string]cty.Value) *hcl.EvalContext {
			return e.fileContext(name, global, current)
		})
		if err != nil {
			return fmt.Errorf("evaluate locals in %q: %w", name, err)
		}
		e.locals[name] = values
	}
	return nil
}

func (e *evaluator) evaluateTargets() ([]targetResult, error) {
	results := make([]targetResult, 0)
	for _, file := range sortedKeys(e.files) {
		hclFile := e.files[file]
		for _, block := range hclFile.body.Blocks {
			if block.Type != "test" {
				continue
			}
			if len(block.Labels) > 1 {
				return nil, fmt.Errorf("evaluate %q: test block has too many labels", file)
			}
			name := ""
			address := rootedPath(file) + ".test"
			if len(block.Labels) == 1 {
				name = block.Labels[0]
				address += "." + name
			}
			result := targetResult{target: target{file: file, kind: "test", label: name, address: address}}
			attribute, ok := block.Body.Attributes["depends_on"]
			if !ok {
				results = append(results, result)
				continue
			}
			context := e.fileContext(file, e.globals[file], e.locals[file])
			value, _, diagnostics := e.evaluateExpression(attribute.Expr, context)
			if diagnostics.HasErrors() {
				return nil, fmt.Errorf("evaluate %q: %s", file, diagnostics.Error())
			}
			if !value.IsWhollyKnown() {
				return nil, fmt.Errorf("evaluate %q: depends_on is not wholly known", file)
			}
			dependencies, err := e.dependencyValues(value)
			if err != nil {
				return nil, fmt.Errorf("decode %q depends_on: %w", file, err)
			}
			result.dependencies = dependencies
			results = append(results, result)
		}
	}
	return results, nil
}

func (e *evaluator) evaluateExpression(expression hclsyntax.Expression, context *hcl.EvalContext) (cty.Value, []string, hcl.Diagnostics) {
	switch expression := expression.(type) {
	case *hclsyntax.RelativeTraversalExpr:
		pathValue, diagnostics := expression.Source.Value(context)
		if diagnostics.HasErrors() {
			return cty.DynamicVal, nil, diagnostics
		}
		if pathValue.Type() != cty.String {
			return cty.DynamicVal, nil, diagnostic("invalid rooted HCL address", "the path portion must evaluate to a string")
		}
		rooted := pathValue.AsString()
		namespace, ok := e.namespaces[rooted]
		if !ok {
			return cty.DynamicVal, nil, diagnostic("unknown rooted HCL address", fmt.Sprintf("no repository file is known for %q", rooted))
		}
		value, dependency, diagnostics := resolveTraversal(namespace, expression.Traversal)
		if diagnostics.HasErrors() {
			return cty.DynamicVal, nil, diagnostics
		}
		return value, []string{dependency}, nil

	case *hclsyntax.TupleConsExpr:
		values := make([]cty.Value, 0, len(expression.Exprs))
		dependencies := make([]string, 0)
		for _, child := range expression.Exprs {
			value, childDependencies, diagnostics := e.evaluateExpression(child, context)
			if diagnostics.HasErrors() {
				return cty.DynamicVal, nil, diagnostics
			}
			values = append(values, value)
			dependencies = append(dependencies, childDependencies...)
		}
		return cty.TupleVal(values), dependencies, nil

	default:
		value, diagnostics := expression.Value(context)
		return value, nil, diagnostics
	}
}

func (e *evaluator) dependencyValues(value cty.Value) ([]string, error) {
	if !value.IsWhollyKnown() {
		return nil, fmt.Errorf("dependency value is unknown")
	}
	if value.CanIterateElements() {
		dependencies := make([]string, 0)
		iterator := value.ElementIterator()
		for iterator.Next() {
			_, element := iterator.Element()
			child, err := e.dependencyValues(element)
			if err != nil {
				return nil, err
			}
			dependencies = append(dependencies, child...)
		}
		return dependencies, nil
	}
	if value.Type() != cty.String {
		return nil, fmt.Errorf("dependency value must be a string or collection, got %s", value.Type().FriendlyName())
	}
	entityID := value.AsString()
	for _, namespace := range e.namespaces {
		if namespace.unlabeled != nil && namespace.unlabeled.entityID == entityID {
			return []string{namespace.unlabeled.address}, nil
		}
		for _, target := range namespace.labeled {
			if target.entityID == entityID {
				return []string{target.address}, nil
			}
		}
	}
	// Non-reference strings are retained so this experiment can represent the
	// existing string/path dependency forms without inventing an entity.
	return []string{entityID}, nil
}

func resolveTraversal(namespace *pathNamespace, traversal hcl.Traversal) (cty.Value, string, hcl.Diagnostics) {
	attributes := make([]string, 0, len(traversal))
	for _, traverser := range traversal {
		attribute, ok := traverser.(hcl.TraverseAttr)
		if !ok {
			return cty.DynamicVal, "", diagnostic("invalid HCL declaration address", "only attribute traversal steps are supported")
		}
		attributes = append(attributes, attribute.Name)
	}
	if len(attributes) == 1 && attributes[0] == "test" {
		if namespace.unlabeled == nil {
			return cty.DynamicVal, "", diagnostic("unknown HCL declaration", fmt.Sprintf("no unlabeled test is declared at %q", namespace.path))
		}
		return cty.StringVal(namespace.unlabeled.entityID), namespace.unlabeled.address, nil
	}
	if len(attributes) != 2 || attributes[0] != "test" {
		return cty.DynamicVal, "", diagnostic("unknown HCL declaration", fmt.Sprintf("unsupported declaration at %q", namespace.path))
	}
	target, ok := namespace.labeled[attributes[1]]
	if !ok {
		return cty.DynamicVal, "", diagnostic("unknown HCL declaration", fmt.Sprintf("no labeled test %q is declared at %q", attributes[1], namespace.path))
	}
	return cty.StringVal(target.entityID), target.address, nil
}

func (e *evaluator) evaluateLocalDeclarations(expressions map[string]hcl.Expression, contextFor func(map[string]cty.Value) *hcl.EvalContext) (map[string]cty.Value, error) {
	values := make(map[string]cty.Value, len(expressions))
	pending := slices.Sorted(maps.Keys(expressions))
	for len(pending) > 0 {
		if err := e.context.Err(); err != nil {
			return nil, err
		}
		progress := false
		next := make([]string, 0, len(pending))
		var lastDiagnostics hcl.Diagnostics
		for _, name := range pending {
			expression, ok := expressions[name].(hclsyntax.Expression)
			if !ok {
				return nil, fmt.Errorf("local declaration %q has unsupported expression type %T", name, expressions[name])
			}
			value, _, diagnostics := e.evaluateExpression(expression, contextFor(values))
			if diagnostics.HasErrors() {
				lastDiagnostics = diagnostics
				next = append(next, name)
				continue
			}
			if !value.IsKnown() {
				return nil, fmt.Errorf("%s must be known", name)
			}
			values[name] = value
			progress = true
		}
		if !progress {
			if lastDiagnostics.HasErrors() {
				return nil, lastDiagnostics.Errs()[0]
			}
			return nil, fmt.Errorf("local declaration evaluation made no progress")
		}
		pending = next
	}
	return values, nil
}

func evaluateDeclarations(ctx context.Context, expressions map[string]hcl.Expression, inherited map[string]cty.Value, contextFor func(map[string]cty.Value) *hcl.EvalContext) (map[string]cty.Value, error) {
	values := make(map[string]cty.Value, len(expressions))
	pending := slices.Sorted(maps.Keys(expressions))
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		progress := false
		next := make([]string, 0, len(pending))
		var lastDiagnostics hcl.Diagnostics
		for _, name := range pending {
			value, diagnostics := expressions[name].Value(contextFor(values))
			if diagnostics.HasErrors() {
				lastDiagnostics = diagnostics
				next = append(next, name)
				continue
			}
			if !value.IsKnown() {
				return nil, fmt.Errorf("%s must be known", name)
			}
			values[name] = value
			progress = true
		}
		if !progress {
			if lastDiagnostics.HasErrors() {
				return nil, lastDiagnostics.Errs()[0]
			}
			return nil, fmt.Errorf("declaration evaluation made no progress")
		}
		pending = next
	}
	return values, nil
}

func objectValue(values map[string]cty.Value) cty.Value {
	if len(values) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(values)
}

func diagnostic(summary, detail string) hcl.Diagnostics {
	return hcl.Diagnostics{{Severity: hcl.DiagError, Summary: summary, Detail: detail}}
}

func dirOf(file string) string {
	dir := path.Dir(file)
	if dir == "." {
		return ""
	}
	return dir
}

func parentDir(dir string) string {
	parent := path.Dir(dir)
	if parent == "." {
		return ""
	}
	return parent
}

func rootedPath(file string) string {
	return "//" + strings.TrimSuffix(file, "/atte.hcl")
}

func cloneValues(values map[string]cty.Value) map[string]cty.Value {
	clone := make(map[string]cty.Value, len(values))
	for name, value := range values {
		clone[name] = value
	}
	return clone
}

func sortedKeys[T any](values map[string]T) []string {
	return slices.Sorted(maps.Keys(values))
}

func formatValues(values map[string]cty.Value) string {
	parts := make([]string, 0, len(values))
	for _, name := range sortedKeys(values) {
		parts = append(parts, fmt.Sprintf("%s=%s", name, values[name].GoString()))
	}
	return strings.Join(parts, ", ")
}

func namespaceDescription(namespace *pathNamespace) string {
	parts := make([]string, 0, len(namespace.labeled)+1)
	if namespace.unlabeled != nil {
		parts = append(parts, "test")
	}
	for label := range namespace.labeled {
		parts = append(parts, "test."+label)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func targetName(result targetResult) string {
	if result.label == "" {
		return "test"
	}
	return "test " + result.label
}
