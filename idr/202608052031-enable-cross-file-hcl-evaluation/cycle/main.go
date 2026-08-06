// cycle demonstrates the failure mode for a dependency cycle in the
// queue-based HCL evaluation model. Unknown namespaces never become known, so
// the queue makes no progress and the context deadline terminates evaluation.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

func main() {
	const (
		fileA = "src/a/atte.hcl"
		fileB = "src/b/atte.hcl"
	)
	sources := map[string]string{
		fileA: `test "a" { depends_on = ["//src/b".test.b] }`,
		fileB: `test "b" { depends_on = ["//src/a".test.a] }`,
	}

	bodies, err := parseFiles(sources)
	if err != nil {
		log.Fatal(err)
	}

	// The shapes are known, but the declaration values are not. A cycle means
	// neither path can be published as a known namespace.
	namespaces := map[string]cty.Value{
		"//src/a": cty.UnknownVal(cty.Object(map[string]cty.Type{
			"test": cty.Object(map[string]cty.Type{"a": cty.String}),
		})),
		"//src/b": cty.UnknownVal(cty.Object(map[string]cty.Type{
			"test": cty.Object(map[string]cty.Type{"b": cty.String}),
		})),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := evaluateQueue(ctx, bodies, namespaces); err == nil {
		log.Fatal("expected the cyclic evaluation to time out")
	} else {
		fmt.Printf("cycle rejected: %s\n", err)
	}
}

func parseFiles(sources map[string]string) (map[string]*hclsyntax.Body, error) {
	bodies := make(map[string]*hclsyntax.Body, len(sources))
	for filename, source := range sources {
		file, diagnostics := hclparse.NewParser().ParseHCL([]byte(source), filename)
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("parse %q: %s", filename, diagnostics.Error())
		}
		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			return nil, fmt.Errorf("parse %q: expected *hclsyntax.Body, got %T", filename, file.Body)
		}
		bodies[filename] = body
	}
	return bodies, nil
}

func evaluateQueue(ctx context.Context, bodies map[string]*hclsyntax.Body, namespaces map[string]cty.Value) error {
	queue := []string{"src/a/atte.hcl"}
	attempts := 0

	for len(queue) > 0 {
		select {
		case <-ctx.Done():
			return fmt.Errorf("evaluation made no progress after %d attempts: %w", attempts, ctx.Err())
		default:
		}

		file := queue[0]
		queue = queue[1:]
		attempts++

		body := bodies[file]
		for _, block := range body.Blocks {
			attribute := block.Body.Attributes["depends_on"]
			value, diagnostics := evaluateDependency(attribute.Expr, namespaces)
			if diagnostics.HasErrors() {
				return fmt.Errorf("evaluate %q: %s", file, diagnostics.Error())
			}
			if !value.IsWhollyKnown() {
				// In a real evaluator, the unknown path would be extracted from
				// the expression and queued here. Both cycle members are already
				// known to be pending, so retrying the current file is enough to
				// demonstrate the no-progress guard.
				queue = append(queue, file)
				continue
			}
		}
	}
	return nil
}

func evaluateDependency(expression hclsyntax.Expression, namespaces map[string]cty.Value) (cty.Value, hcl.Diagnostics) {
	tuple, ok := expression.(*hclsyntax.TupleConsExpr)
	if !ok {
		return expression.Value(nil)
	}

	values := make([]cty.Value, 0, len(tuple.Exprs))
	for _, child := range tuple.Exprs {
		address, ok := child.(*hclsyntax.RelativeTraversalExpr)
		if !ok {
			value, diagnostics := child.Value(nil)
			if diagnostics.HasErrors() {
				return cty.DynamicVal, diagnostics
			}
			values = append(values, value)
			continue
		}
		pathValue, diagnostics := address.Source.Value(nil)
		if diagnostics.HasErrors() {
			return cty.DynamicVal, diagnostics
		}
		namespace := namespaces[pathValue.AsString()]
		value, diagnostics := address.Traversal.TraverseRel(namespace)
		if diagnostics.HasErrors() {
			return cty.DynamicVal, diagnostics
		}
		values = append(values, value)
	}
	return cty.TupleVal(values), nil
}
