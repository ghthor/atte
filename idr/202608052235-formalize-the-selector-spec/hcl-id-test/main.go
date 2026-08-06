// hcl-id-test demonstrates how a rooted HCL declaration address such as
// "//pkg".test can be evaluated.
//
// HCL parses the quoted repository path as an expression and .test as a
// relative traversal. A normal HCL evaluation context cannot resolve this by
// itself because the quoted path evaluates to a string. The application must
// evaluate the path, use it to look up a namespace in its context, and then
// apply the parsed traversal to that namespace.
package main

import (
	"fmt"
	"log"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

func main() {
	const source = `target = "//pkg".test`

	file, diagnostics := hclparse.NewParser().ParseHCL([]byte(source), "example.hcl")
	if diagnostics.HasErrors() {
		log.Fatalf("parse HCL: %s", diagnostics.Error())
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		log.Fatalf("expected *hclsyntax.Body, got %T", file.Body)
	}

	expression, ok := body.Attributes["target"].Expr.(*hclsyntax.RelativeTraversalExpr)
	if !ok {
		log.Fatalf("expected a relative traversal, got %T", body.Attributes["target"].Expr)
	}

	pathValue, diagnostics := expression.Source.Value(nil)
	if diagnostics.HasErrors() {
		log.Fatalf("evaluate path root: %s", diagnostics.Error())
	}
	if pathValue.Type() != cty.String {
		log.Fatalf("expected a string path root, got %s", pathValue.Type().FriendlyName())
	}

	path := pathValue.AsString()
	if path != "//pkg" {
		log.Fatalf("unexpected path root %q", path)
	}

	// A normal HCL evaluation demonstrates why the application needs to add
	// path-aware resolution. The literal evaluates to a string, and HCL cannot
	// apply .test to a string.
	_, normalDiagnostics := expression.Value(&hcl.EvalContext{})
	if !normalDiagnostics.HasErrors() {
		log.Fatal("expected normal HCL evaluation to reject traversal from a string")
	}

	// The detector adds a path-keyed namespace to its evaluation context. The
	// key is not an HCL variable name; it is application data consumed by the
	// path-aware evaluator below. cty object values are used for the namespace
	// so the existing HCL traversal machinery can resolve .test.
	context := &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"//pkg": cty.ObjectVal(map[string]cty.Value{
				"test": cty.StringVal("declaration at //pkg#test"),
			}),
		},
	}
	value, diagnostics := evaluateAddress(expression, context)
	if diagnostics.HasErrors() {
		log.Fatalf("evaluate rooted HCL address: %s", diagnostics.Error())
	}

	fmt.Printf("parsed path root: %s\n", path)
	fmt.Printf("parsed traversal steps: %d\n", len(expression.Traversal))
	fmt.Printf("normal HCL evaluation: rejected string traversal\n")
	fmt.Printf("context namespace key: %q\n", path)
	fmt.Printf("resolved declaration: %s\n", value.AsString())
}

// evaluateAddress implements the application-specific part of rooted HCL
// declaration addresses. It evaluates the quoted path, looks up the resulting
// key in the supplied context, and delegates the remaining .test traversal to
// HCL.
func evaluateAddress(expression *hclsyntax.RelativeTraversalExpr, context *hcl.EvalContext) (cty.Value, hcl.Diagnostics) {
	pathValue, diagnostics := expression.Source.Value(context)
	if diagnostics.HasErrors() {
		return cty.DynamicVal, diagnostics
	}
	if pathValue.Type() != cty.String {
		return cty.DynamicVal, hcl.Diagnostics{
			{
				Severity: hcl.DiagError,
				Summary:  "invalid HCL declaration address",
				Detail:   "the rooted path must evaluate to a string",
			},
		}
	}

	path := pathValue.AsString()
	namespace, ok := context.Variables[path]
	if !ok {
		return cty.DynamicVal, hcl.Diagnostics{
			{
				Severity: hcl.DiagError,
				Summary:  "unknown HCL declaration path",
				Detail:   fmt.Sprintf("there is no declaration namespace for %q", path),
			},
		}
	}

	return expression.Traversal.TraverseRel(namespace)
}
