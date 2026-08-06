// hcl-eval-loop demonstrates queue-based evaluation of cross-file HCL
// references. The complete repository file list is known before evaluation,
// so every rooted HCL path starts in the evaluation context as an unknown
// namespace with a known object shape.
package main

import (
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

const (
	goFile    = "src/go/atte.hcl"
	pyFile    = "src/py/atte.hcl"
	zigFile   = "src/zig/atte.hcl"
	basicFile = "src/basic/atte.hcl"
)

var repository = map[string]string{
	goFile: `test "go" {}`,
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
}

type evaluator struct {
	files          map[string]*hclsyntax.Body
	namespaces     map[string]cty.Value
	unlabeledTests map[string]cty.Value
	context        *hcl.EvalContext
	trace          []string
}

type evaluation struct {
	file      string
	known     bool
	waitingOn []string
	dependsOn []string
}

func main() {
	evaluator, err := newEvaluator(repository)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	results, err := evaluator.evaluateAll(ctx)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("initial context:")
	for _, path := range sortedKeys(evaluator.namespaces) {
		fmt.Printf("  %s = unknown\n", path)
	}
	fmt.Println()

	fmt.Println("queue trace:")
	for _, event := range evaluator.trace {
		fmt.Printf("  %s\n", event)
	}
	fmt.Println()

	fmt.Println("evaluation results:")
	for _, result := range results {
		fmt.Printf("  %s: resolved", result.file)
		if len(result.dependsOn) > 0 {
			fmt.Printf(" (depends_on: %s)", strings.Join(result.dependsOn, ", "))
		}
		fmt.Println()
	}
	fmt.Println()
	fmt.Println("The queue starts with src/py because it contains the cross-file references.")
	fmt.Println("The provided src/go file is a leaf, so evaluating it cannot itself observe an unknown dependency.")
}

func newEvaluator(repository map[string]string) (*evaluator, error) {
	files := make(map[string]*hclsyntax.Body, len(repository))
	for file, source := range repository {
		parsed, diagnostics := hclparse.NewParser().ParseHCL([]byte(source), file)
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("parse %q: %s", file, diagnostics.Error())
		}
		body, ok := parsed.Body.(*hclsyntax.Body)
		if !ok {
			return nil, fmt.Errorf("parse %q: expected *hclsyntax.Body, got %T", file, parsed.Body)
		}
		files[file] = body
	}

	// Discover the shape of every path namespace before evaluating any file.
	// This gives unknown values a useful cty object type, so traversals such as
	// .test.go remain unknown instead of failing with an unsupported-attribute
	// error.
	namespaces := make(map[string]cty.Value, len(files))
	unlabeledTests := make(map[string]cty.Value, len(files))
	for file, body := range files {
		path := rootedPath(file)
		shape, hasUnlabeledTest, err := declarationNamespace(body)
		if err != nil {
			return nil, fmt.Errorf("discover %q: %w", file, err)
		}
		namespaces[path] = cty.UnknownVal(shape.Type())
		if hasUnlabeledTest {
			unlabeledTests[path] = cty.UnknownVal(cty.String)
		}
	}

	evaluator := &evaluator{files: files, namespaces: namespaces, unlabeledTests: unlabeledTests}
	evaluator.context = evaluator.evalContext()
	return evaluator, nil
}

func (e *evaluator) evalContext() *hcl.EvalContext {
	// HCL variables cannot themselves be named "//src/go". The application
	// keeps the rooted paths in one context value and performs the path lookup
	// when it evaluates a rooted address expression.
	return &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"repository_paths": cty.ObjectVal(e.namespaces),
		},
	}
}

func (e *evaluator) evaluateAll(ctx context.Context) ([]evaluation, error) {
	files := sortedKeys(e.files)
	// Seed the queue with the file whose result was requested. The complete
	// file list was still discovered before this point, so all rooted paths are
	// represented in the context as unknown values.
	queue := make([]string, 0, len(files))
	queued := make(map[string]bool, len(files))
	resolved := make(map[string]bool, len(files))
	results := make([]evaluation, 0, len(files))

	enqueue := func(file string) {
		if resolved[file] || queued[file] {
			return
		}
		queue = append(queue, file)
		queued[file] = true
	}
	enqueue(pyFile)
	enqueue(basicFile)

	for len(queue) > 0 {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("evaluation made no progress: %w", ctx.Err())
		default:
		}

		file := queue[0]
		queue = queue[1:]
		delete(queued, file)
		e.trace = append(e.trace, "evaluate "+file)
		result, err := e.evaluateFile(file)
		if err != nil {
			return nil, err
		}
		if !result.known {
			// An unknown rooted path is a dependency on another file. Put those
			// files on the queue, then retry this file after the queue has had a
			// chance to resolve them. This is deliberately a queue, not recursion.
			e.trace = append(e.trace, fmt.Sprintf("  %s waits on %s", file, strings.Join(result.waitingOn, ", ")))
			for _, dependency := range result.waitingOn {
				dependencyFile, ok := e.fileForRootedPath(dependency)
				if !ok {
					return nil, fmt.Errorf("%s references unknown repository path %q", file, dependency)
				}
				e.trace = append(e.trace, "  queue "+dependencyFile)
				enqueue(dependencyFile)
			}
			e.trace = append(e.trace, "  retry "+file)
			enqueue(file)
			continue
		}

		resolved[file] = true
		e.trace = append(e.trace, "  publish "+rootedPath(file))
		results = append(results, result)
		// Publish the now-known namespace to subsequent evaluations.
		shape, hasUnlabeledTest, err := declarationNamespace(e.files[file])
		if err != nil {
			return nil, fmt.Errorf("discover %q: %w", file, err)
		}
		path := rootedPath(file)
		e.namespaces[path] = shape
		if hasUnlabeledTest {
			e.unlabeledTests[path] = cty.StringVal("unlabeled test")
		}
		e.context = e.evalContext()
	}

	return results, nil
}

func (e *evaluator) evaluateFile(file string) (evaluation, error) {
	result := evaluation{file: file}
	body := e.files[file]
	for _, block := range body.Blocks {
		if block.Type != "test" {
			continue
		}
		if len(block.Labels) > 1 {
			return result, fmt.Errorf("%s: test block has too many labels", file)
		}
		attribute, ok := block.Body.Attributes["depends_on"]
		if !ok {
			result.known = true
			continue
		}

		value, waitingOn, dependencies, diagnostics := e.evaluateExpression(attribute.Expr)
		if diagnostics.HasErrors() {
			return result, fmt.Errorf("evaluate %q: %s", file, diagnostics.Error())
		}
		result.waitingOn = append(result.waitingOn, waitingOn...)
		result.dependsOn = append(result.dependsOn, dependencies...)
		if !value.IsWhollyKnown() {
			return result, nil
		}
		result.known = true
	}
	return result, nil
}

func (e *evaluator) evaluateExpression(expression hclsyntax.Expression) (cty.Value, []string, []string, hcl.Diagnostics) {
	switch expression := expression.(type) {
	case *hclsyntax.RelativeTraversalExpr:
		pathValue, diagnostics := expression.Source.Value(e.context)
		if diagnostics.HasErrors() {
			return cty.DynamicVal, nil, nil, diagnostics
		}
		if pathValue.Type() != cty.String {
			return cty.DynamicVal, nil, nil, hcl.Diagnostics{
				{
					Severity: hcl.DiagError,
					Summary:  "invalid rooted HCL address",
					Detail:   "the path portion must evaluate to a string",
				},
			}
		}
		path := pathValue.AsString()
		namespace, ok := e.namespaces[path]
		if !ok {
			return cty.DynamicVal, nil, nil, hcl.Diagnostics{
				{
					Severity: hcl.DiagError,
					Summary:  "unknown rooted HCL address",
					Detail:   fmt.Sprintf("no repository file is known for %q", path),
				},
			}
		}
		if isUnlabeledTestTraversal(expression) {
			unlabeled, ok := e.unlabeledTests[path]
			if !ok {
				return cty.DynamicVal, nil, nil, hcl.Diagnostics{
					{
						Severity: hcl.DiagError,
						Summary:  "unknown HCL declaration",
						Detail:   fmt.Sprintf("no unlabeled test is declared at %q", path),
					},
				}
			}
			if !unlabeled.IsKnown() {
				return unlabeled, []string{path}, nil, nil
			}
			return unlabeled, nil, []string{path + "#test"}, nil
		}

		value, traversalDiagnostics := expression.Traversal.TraverseRel(namespace)
		if traversalDiagnostics.HasErrors() {
			return cty.DynamicVal, nil, nil, traversalDiagnostics
		}
		if !namespace.IsKnown() {
			return value, []string{path}, nil, nil
		}
		return value, nil, []string{path + "#" + traversalText(expression)}, nil

	case *hclsyntax.TupleConsExpr:
		values := make([]cty.Value, 0, len(expression.Exprs))
		waitingOn := make([]string, 0)
		dependencies := make([]string, 0)
		for _, child := range expression.Exprs {
			value, childWaiting, childDependencies, diagnostics := e.evaluateExpression(child)
			if diagnostics.HasErrors() {
				return cty.DynamicVal, nil, nil, diagnostics
			}
			values = append(values, value)
			waitingOn = append(waitingOn, childWaiting...)
			dependencies = append(dependencies, childDependencies...)
		}
		return cty.TupleVal(values), waitingOn, dependencies, nil

	default:
		value, diagnostics := expression.Value(e.context)
		return value, nil, nil, diagnostics
	}
}

func declarationNamespace(body *hclsyntax.Body) (cty.Value, bool, error) {
	tests := make(map[string]cty.Value)
	hasUnlabeledTest := false
	for _, block := range body.Blocks {
		if block.Type != "test" {
			continue
		}
		if len(block.Labels) > 1 {
			return cty.NilVal, false, fmt.Errorf("test block has too many labels")
		}
		if len(block.Labels) == 0 {
			// An unlabeled block contributes only its type to the HCL address:
			// "//src/basic".test. Labeled declarations share the test namespace:
			// "//src/basic".test.labeled. Since cty cannot make test both an
			// object and a terminal value, the evaluator handles the unlabeled
			// terminal as a special declaration alongside the object shape.
			hasUnlabeledTest = true
			continue
		}
		label := block.Labels[0]
		tests[label] = cty.StringVal("test " + label)
	}
	return cty.ObjectVal(map[string]cty.Value{"test": cty.ObjectVal(tests)}), hasUnlabeledTest, nil
}

func (e *evaluator) fileForRootedPath(path string) (string, bool) {
	for file := range e.files {
		if rootedPath(file) == path {
			return file, true
		}
	}
	return "", false
}

func rootedPath(file string) string {
	return "//" + strings.TrimSuffix(file, "/atte.hcl")
}

func isUnlabeledTestTraversal(expression *hclsyntax.RelativeTraversalExpr) bool {
	if len(expression.Traversal) != 1 {
		return false
	}
	attribute, ok := expression.Traversal[0].(hcl.TraverseAttr)
	return ok && attribute.Name == "test"
}

func traversalText(expression *hclsyntax.RelativeTraversalExpr) string {
	parts := make([]string, 0, len(expression.Traversal))
	for _, traverser := range expression.Traversal {
		if attribute, ok := traverser.(hcl.TraverseAttr); ok {
			parts = append(parts, attribute.Name)
		}
	}
	return strings.Join(parts, ".")
}

func sortedKeys[T any](values map[string]T) []string {
	return slices.Sorted(maps.Keys(values))
}
