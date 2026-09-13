package attehcl

import (
	"fmt"
	"maps"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

type hclScope struct {
	local cty.Value
}

func objectValue(values map[string]cty.Value) cty.Value {
	if len(values) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(values)
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
