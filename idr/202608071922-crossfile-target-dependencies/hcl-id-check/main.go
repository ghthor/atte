// hcl-id-check investigates whether HCL accepts numeric attribute traversal
// syntax such as test.0. It compares that syntax with index expressions and
// named attribute references, which are candidate forms for same-file target
// references.
package main

import (
	"fmt"
	"log"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

type caseResult struct {
	expression  string
	parsed      bool
	syntaxType  string
	value       string
	diagnostics string
}

func main() {
	cases := []struct {
		expression string
		context    *hcl.EvalContext
	}{
		{
			expression: "test.0",
			context: objectContext(map[string]cty.Value{
				"0": cty.StringVal("anonymous target 0"),
			}),
		},
		{
			expression: "test[0]",
			context:    tupleContext(cty.StringVal("anonymous target 0")),
		},
		{
			expression: "test.build",
			context: objectContext(map[string]cty.Value{
				"build": cty.StringVal("named target build"),
			}),
		},
		{
			expression: `test["0"]`,
			context: objectContext(map[string]cty.Value{
				"0": cty.StringVal("anonymous target 0"),
			}),
		},
	}

	for _, item := range cases {
		result := check(item.expression, item.context)
		fmt.Printf("expression %q\n", result.expression)
		fmt.Printf("  parsed: %t\n", result.parsed)
		if result.syntaxType != "" {
			fmt.Printf("  syntax node: %s\n", result.syntaxType)
		}
		if result.value != "" {
			fmt.Printf("  value: %s\n", result.value)
		}
		if result.diagnostics != "" {
			fmt.Printf("  diagnostics: %s\n", result.diagnostics)
		}
	}

	result := check("test.0", objectContext(map[string]cty.Value{
		"0": cty.StringVal("anonymous target 0"),
	}))
	if !result.parsed || result.value != "anonymous target 0" {
		log.Fatalf("expected test.0 to parse as an attribute reference and evaluate, got %#v", result)
	}

	result = check("test[0]", tupleContext(cty.StringVal("anonymous target 0")))
	if !result.parsed || result.value != "anonymous target 0" {
		log.Fatalf("expected test[0] to parse and evaluate, got %#v", result)
	}

	result = check("test.build", objectContext(map[string]cty.Value{
		"build": cty.StringVal("named target build"),
	}))
	if !result.parsed || result.value != "named target build" {
		log.Fatalf("expected test.build to parse and evaluate, got %#v", result)
	}
}

func objectContext(values map[string]cty.Value) *hcl.EvalContext {
	return &hcl.EvalContext{Variables: map[string]cty.Value{
		"test": cty.ObjectVal(values),
	}}
}

func tupleContext(values ...cty.Value) *hcl.EvalContext {
	return &hcl.EvalContext{Variables: map[string]cty.Value{
		"test": cty.TupleVal(values),
	}}
}

func check(expression string, context *hcl.EvalContext) caseResult {
	file, diagnostics := hclparse.NewParser().ParseHCL(
		[]byte("value = "+expression+"\n"),
		"expression.hcl",
	)
	result := caseResult{expression: expression}
	if diagnostics.HasErrors() {
		result.diagnostics = diagnostics.Error()
		return result
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		result.diagnostics = fmt.Sprintf("expected *hclsyntax.Body, got %T", file.Body)
		return result
	}
	attribute, ok := body.Attributes["value"]
	if !ok {
		result.diagnostics = "missing value attribute"
		return result
	}
	result.parsed = true
	result.syntaxType = fmt.Sprintf("%T", attribute.Expr)

	value, evalDiagnostics := attribute.Expr.Value(context)
	if evalDiagnostics.HasErrors() {
		result.diagnostics = evalDiagnostics.Error()
		return result
	}
	if value.Type() != cty.String {
		result.diagnostics = fmt.Sprintf("expected string result, got %s", value.Type().FriendlyName())
		return result
	}
	result.value = value.AsString()
	return result
}
