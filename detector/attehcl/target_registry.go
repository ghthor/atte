package attehcl

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"github.com/ghthor/atte/detector/attehcltarget"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// FunctionCapabilities provides repository- and file-aware HCL functions to
// HCL evaluation.
type FunctionCapabilities interface {
	HCLFunctions(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)
}

// EntityCapabilities provides entity-ID decoding to HCL evaluation.
type EntityCapabilities interface {
	DecodeID(graph.EntityID) (graph.Entity, error)
}

// Capabilities provides the capabilities HCL evaluation needs from the
// compiled Scanner without importing the detector package.
type Capabilities interface {
	attehcltarget.KindCapabilities
	FunctionCapabilities
	EntityCapabilities
}

const (
	KindTest    attehcltarget.Kind = "test"
	KindCodegen attehcltarget.Kind = "codegen"
	KindLint    attehcltarget.Kind = "lint"
)

var testSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "script"},
		{Name: "depends_on"},
		{Name: "triggered_by"},
	},
}

// BuiltInTargetKinds returns the target kinds provided by atte itself.
func BuiltInTargetKinds() map[attehcltarget.Kind]attehcltarget.KindSpec {
	return map[attehcltarget.Kind]attehcltarget.KindSpec{
		KindTest: {
			Decoder:   decodeScriptTarget,
			Graph:     graphScriptTarget,
			Execution: executeScriptTarget,
			Config:    configScriptTarget,
			Script:    builtinTargetScript,
		},
		KindCodegen: {
			Decoder:   decodeScriptTarget,
			Graph:     graphScriptTarget,
			Execution: executeScriptTarget,
			Config:    configScriptTarget,
			Script:    builtinTargetScript,
		},
		KindLint: {
			Decoder:   decodeScriptTarget,
			Graph:     graphScriptTarget,
			Execution: executeScriptTarget,
			Config:    configScriptTarget,
			Script:    builtinTargetScript,
		},
	}
}

type decodedTarget struct {
	Script string
	Deps   []dependency
}

func validateDecoderResult(kind attehcltarget.Kind, decoded any) error {
	if decoded == nil {
		return fmt.Errorf("target kind %q decoder returned nil", kind)
	}
	value := reflect.ValueOf(decoded)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return fmt.Errorf("target kind %q decoder returned a nil pointer", kind)
	}
	return nil
}

const targetTraversalValuePrefix = "attehcl-target:"

var defaultTargetKinds = BuiltInTargetKinds()

func targetKinds(scanner attehcltarget.KindCapabilities) map[attehcltarget.Kind]attehcltarget.KindSpec {
	provided := defaultTargetKinds
	if scanner != nil {
		provided = scanner.TargetKinds()
	}
	result := make(map[attehcltarget.Kind]attehcltarget.KindSpec, len(provided))
	for kind, spec := range provided {
		schema := testSchema
		if spec.Schema != nil {
			schema = copyBodySchema(*spec.Schema)
		}
		spec.Schema = &schema
		result[kind] = spec
	}
	return result
}

func copyBodySchema(schema hcl.BodySchema) hcl.BodySchema {
	return hcl.BodySchema{
		Attributes: append([]hcl.AttributeSchema(nil), schema.Attributes...),
		Blocks:     append([]hcl.BlockHeaderSchema(nil), schema.Blocks...),
	}
}

func targetTraversalValue(kind attehcltarget.Kind, name string) string {
	return targetTraversalValuePrefix + string(kind) + "." + name
}

const targetReferenceValuePrefix = "attehcl-target-ref:"

func targetReferenceValue(file reference.Blob, kind attehcltarget.Kind, name string) string {
	return targetReferenceValuePrefix + file.String() + "#" + string(kind) + "." + name
}

func targetReferenceFromValue(value string) (targetReference, bool) {
	raw, ok := strings.CutPrefix(value, targetReferenceValuePrefix)
	if !ok {
		return targetReference{}, false
	}
	fileName, identifier, ok := strings.Cut(raw, "#")
	if !ok {
		return targetReference{}, false
	}
	file, err := reference.ParseBlob(fileName)
	if err != nil {
		return targetReference{}, false
	}
	kind, name, ok := strings.Cut(identifier, ".")
	if !ok || !hclsyntax.ValidIdentifier(kind) || !hclsyntax.ValidIdentifier(name) {
		return targetReference{}, false
	}
	return targetReference{
		file: file,
		kind: attehcltarget.Kind(kind),
		name: name,
	}, true
}

func targetTraversal(kind attehcltarget.Kind, name string) hcl.Traversal {
	return hcl.Traversal{
		hcl.TraverseRoot{Name: string(kind)},
		hcl.TraverseAttr{Name: name},
	}
}

func targetTraversalFromValue(value string) (hcl.Traversal, bool) {
	raw, ok := strings.CutPrefix(value, targetTraversalValuePrefix)
	if !ok {
		return nil, false
	}
	kind, name, ok := strings.Cut(raw, ".")
	if !ok || !hclsyntax.ValidIdentifier(kind) || !hclsyntax.ValidIdentifier(name) {
		return nil, false
	}
	return hcl.Traversal{
		hcl.TraverseRoot{Name: kind},
		hcl.TraverseAttr{Name: name},
	}, true
}

func decodeTargetDependencies(attribute *hcl.Attribute, dependencyCtx *hcl.EvalContext) ([]dependency, error) {
	value, diagnostics := attribute.Expr.Value(dependencyCtx)
	if diagnostics.HasErrors() {
		return nil, targetDiagnosticsError{diagnostics: diagnostics}
	}
	if !value.IsKnown() {
		return nil, fmt.Errorf("dependency expression must be known")
	}
	if value.IsNull() || (!value.Type().IsTupleType() && !value.Type().IsListType() && !value.Type().IsSetType()) {
		return nil, fmt.Errorf("dependency expression must evaluate to a list")
	}
	return decodeDependencyValue(value)
}

func dependencyEvalContext(ctx *hcl.EvalContext) *hcl.EvalContext {
	variables := make(map[string]cty.Value, len(ctx.Variables))
	for name, value := range ctx.Variables {
		variables[name] = dependencyValue(value)
	}
	functions := make(map[string]function.Function, len(ctx.Functions))
	maps.Copy(functions, ctx.Functions)
	for _, name := range []string{"path", "atte::path"} {
		if path, ok := functions[name]; ok {
			functions[name] = dependencyPathFunction(path)
		}
	}
	return &hcl.EvalContext{Variables: variables, Functions: functions}
}

func dependencyPathFunction(path function.Function) function.Function {
	return function.New(&function.Spec{
		Params: path.Params(),
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			value, err := path.Call(args)
			if err != nil {
				return cty.NilVal, err
			}
			return dependencyValue(value), nil
		},
	})
}

func dependencyValue(value cty.Value) cty.Value {
	if !value.IsKnown() || value.IsNull() {
		return value
	}
	if value.Type() == attegit.RepositoryPathType {
		blob := *value.EncapsulatedValue().(*reference.Blob)
		return cty.StringVal(DecodingPathPrefix + blob.String())
	}
	if value.Type().IsTupleType() || value.Type().IsListType() || value.Type().IsSetType() {
		elements := make([]cty.Value, 0)
		iterator := value.ElementIterator()
		for iterator.Next() {
			_, element := iterator.Element()
			elements = append(elements, dependencyValue(element))
		}
		return cty.TupleVal(elements)
	}
	if value.Type().IsObjectType() {
		attributes := make(map[string]cty.Value, len(value.AsValueMap()))
		for name, attribute := range value.AsValueMap() {
			attributes[name] = dependencyValue(attribute)
		}
		return cty.ObjectVal(attributes)
	}
	return value
}

func decodeDependencyValue(value cty.Value) ([]dependency, error) {
	if !value.IsKnown() {
		return nil, fmt.Errorf("dependency value must be known")
	}
	if value.IsNull() {
		return nil, fmt.Errorf("dependency value must not be null")
	}
	if value.Type().IsTupleType() || value.Type().IsListType() || value.Type().IsSetType() {
		dependencies := make([]dependency, 0)
		iterator := value.ElementIterator()
		for iterator.Next() {
			_, element := iterator.Element()
			decoded, err := decodeDependencyValue(element)
			if err != nil {
				return nil, err
			}
			dependencies = append(dependencies, decoded...)
		}
		return dependencies, nil
	}
	if value.Type() == attegit.RepositoryPathType {
		blob := *value.EncapsulatedValue().(*reference.Blob)
		return []dependency{{kind: dependencyPath, path: blob.String()}}, nil
	}
	if value.Type() != cty.String {
		return nil, fmt.Errorf("values must be strings or paths")
	}
	raw := value.AsString()
	if traversal, ok := targetTraversalFromValue(raw); ok {
		return []dependency{{kind: dependencyTarget, traversal: traversal}}, nil
	}
	if target, ok := targetReferenceFromValue(raw); ok {
		return []dependency{
			{
				kind:      dependencyTarget,
				target:    target,
				traversal: targetTraversal(target.kind, target.name),
			},
		}, nil
	}
	if entity, ok := strings.CutPrefix(raw, "attehcl-id:"); ok {
		return []dependency{{kind: dependencyEntity, entity: graph.EntityID(entity)}}, nil
	}
	if strings.HasPrefix(raw, "attego:") {
		return []dependency{{kind: dependencyEntity, entity: graph.EntityID(raw)}}, nil
	}
	return []dependency{{kind: dependencyPath, path: strings.TrimPrefix(raw, DecodingPathPrefix)}}, nil
}

type targetDiagnosticsError struct {
	diagnostics hcl.Diagnostics
}

func (err targetDiagnosticsError) Error() string {
	return err.diagnostics.Error()
}

func decodeScriptTarget(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
	decoded := decodedTarget{}
	if attribute, ok := content.Attributes["script"]; ok {
		value, diagnostics := attribute.Expr.Value(ctx)
		if diagnostics.HasErrors() {
			return nil, targetDiagnosticsError{diagnostics: diagnostics}
		}
		if !value.IsKnown() {
			return nil, fmt.Errorf("script must be known")
		}
		switch {
		case value.Type() == attegit.RepositoryPathType:
			blob := *value.EncapsulatedValue().(*reference.Blob)
			decoded.Script = DecodingPathPrefix + blob.String()
		case value.Type() == cty.String:
			decoded.Script = value.AsString()
		default:
			return nil, fmt.Errorf("script must be a string or path")
		}
	}

	var dependencyContext *hcl.EvalContext
	for _, name := range []string{"depends_on", "triggered_by"} {
		attribute, ok := content.Attributes[name]
		if !ok {
			continue
		}
		if dependencyContext == nil {
			dependencyContext = dependencyEvalContext(ctx)
		}
		dependencies, err := decodeTargetDependencies(attribute, dependencyContext)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		decoded.Deps = append(decoded.Deps, dependencies...)
	}
	return decoded, nil
}
