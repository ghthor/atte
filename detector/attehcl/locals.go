package attehcl

import (
	"fmt"
	"maps"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

type hclScope struct {
	local   cty.Value
	targets map[string]cty.Value
}

func objectValue(values map[string]cty.Value) cty.Value {
	if len(values) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(values)
}

func localContext(values, targets map[string]cty.Value, functions map[string]function.Function) *hcl.EvalContext {
	variables := make(map[string]cty.Value, len(targets)+1)
	variables["local"] = objectValue(values)
	maps.Copy(variables, targets)
	return &hcl.EvalContext{Variables: variables, Functions: functions}
}

func evaluateDeclaration(expression hcl.Expression, context *hcl.EvalContext) (cty.Value, hcl.Diagnostics) {
	return expression.Value(context)
}

// evaluateLocals resolves file-local declarations without evaluating another
// atte.hcl file. Cross-file target references are resolved from declarations.
func targetContextValues(blocks []normalizedBlock, kinds map[Kind]targetKindSpec) map[string]cty.Value {
	attributes := make(map[Kind]map[string]cty.Value, len(kinds))
	for kind := range kinds {
		attributes[kind] = make(map[string]cty.Value)
	}
	for _, block := range blocks {
		if block.name == "" {
			continue
		}
		attributes[block.kind][block.name] = cty.StringVal(targetTraversalValue(block.kind, block.name))
	}
	targets := make(map[string]cty.Value, len(attributes))
	for kind, values := range attributes {
		targets[string(kind)] = objectValue(values)
	}
	return targets
}

func evaluateLocals(
	file *hclFile,
	expressions map[string]hcl.Expression,
	targets map[string]cty.Value,
	functions map[string]function.Function,
) (map[string]cty.Value, error) {
	values := make(map[string]cty.Value, len(expressions))
	pending := make(map[string]hcl.Expression, len(expressions))
	maps.Copy(pending, expressions)
	for len(pending) > 0 {
		progress := false
		var lastName string
		var lastDiags hcl.Diagnostics
		for name, expr := range pending {
			lastName = name
			ctx := localContext(values, targets, functions)
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

func (p localsPhase) scopeFor(file *hclFile, blocks []normalizedBlock) (hclScope, error) {
	functions, err := p.evaluator.hclFunctions(file.file)
	if err != nil {
		return hclScope{}, err
	}
	targets := targetContextValues(blocks, p.evaluator.kindSpecs)
	locals, err := evaluateLocals(file, file.locals, targets, functions)
	if err != nil {
		return hclScope{}, err
	}
	return hclScope{local: objectValue(locals), targets: targets}, nil
}
