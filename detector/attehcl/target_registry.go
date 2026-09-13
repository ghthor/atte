package attehcl

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// Kind identifies a registered target kind and its target namespace.
type Kind string

// TargetDecoder decodes a schema-validated target body into a kind-owned value.
type TargetDecoder func(*hcl.BodyContent, *hcl.EvalContext) (any, error)

// TargetGraphProjection projects a target into graph entities and relationships.
// The attachToTree argument requests the common file/tree containment relations.
type TargetGraphProjection func(context.Context, *attegit.Repo, Target, TargetGraphContext, bool) (TargetGraph, error)

// TargetExecutionProjection constructs the command used to run a target. Args
// contains the executable and its arguments; Dir is the process working directory.
type TargetExecutionProjection func(Target, string) (TargetCommand, error)

// TargetConfigProjection adds decoded target data to config show output. The
// returned keys must not overlap the standard target identity fields.
type TargetConfigProjection func(Target) (map[string]any, error)

// TargetKindSpec describes the independent capabilities of a registered kind.
// Decoder is required; the other projections are optional.
type TargetKindSpec struct {
	Schema    *hcl.BodySchema
	Decoder   TargetDecoder
	Graph     TargetGraphProjection
	Execution TargetExecutionProjection
	Config    TargetConfigProjection
}

// TargetGraphContext provides common dependency resolution to a graph projector.
type TargetGraphContext struct {
	ResolveTarget   func(hcl.Traversal) (graph.Entity, error)
	ResolveTargetAt func(reference.Blob, hcl.Traversal) (graph.Entity, error)
	EntityKind      func(graph.EntityID) (string, error)
}

// TargetGraph is the graph projection of one target.
type TargetGraph struct {
	Entities      []graph.Entity
	Relationships []graph.Relationship
}

// TargetCommand is the executable representation of one target.
type TargetCommand struct {
	Dir  string
	Args []string
}

const (
	KindTest    Kind = "test"
	KindCodegen Kind = "codegen"
	KindLint    Kind = "lint"
)

var testSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "script"},
		{Name: "depends_on"},
		{Name: "triggered_by"},
	},
}

type targetScriptProjection func(*attegit.Repo, reference.Blob, any) (reference.Blob, string, error)

type targetKindSpec struct {
	TargetKindSpec
	script targetScriptProjection
}

var (
	registryMu sync.RWMutex
	registry   = make(map[Kind]targetKindSpec)
)

func init() {
	for _, kind := range []Kind{KindTest, KindCodegen, KindLint} {
		if err := registerBuiltIn(kind, TargetKindSpec{
			Decoder:   decodeScriptTarget,
			Graph:     graphScriptTarget,
			Execution: executeScriptTarget,
			Config:    configScriptTarget,
		}); err != nil {
			panic(err)
		}
	}
}

// Register adds a target kind to the process-wide target registry. A nil schema
// uses the built-in script-target schema. Registration rejects duplicate kinds.
// Each evaluator takes a stable registry snapshot when it is created.
func Register[K ~string](kind K, spec TargetKindSpec) error {
	return register(kind, spec, nil)
}

func registerBuiltIn[K ~string](kind K, spec TargetKindSpec) error {
	return register(kind, spec, builtinTargetScript)
}

func register[K ~string](kind K, spec TargetKindSpec, script targetScriptProjection) error {
	name := string(kind)
	if kind == "" {
		return fmt.Errorf("target kind is empty")
	}
	if !hclsyntax.ValidIdentifier(name) {
		return fmt.Errorf("target kind %q is not a valid HCL identifier", kind)
	}
	if spec.Decoder == nil {
		return fmt.Errorf("target kind %q has no decoder", kind)
	}

	schema := testSchema
	if spec.Schema != nil {
		schema = copyBodySchema(*spec.Schema)
	} else {
		schema = copyBodySchema(schema)
	}

	key := Kind(kind)
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[key]; exists {
		return fmt.Errorf("target kind %q is already registered", kind)
	}
	registry[key] = targetKindSpec{
		TargetKindSpec: TargetKindSpec{
			Schema:    &schema,
			Decoder:   spec.Decoder,
			Graph:     spec.Graph,
			Execution: spec.Execution,
			Config:    spec.Config,
		},
		script: script,
	}
	return nil
}

func registeredKind(kind Kind) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, registered := registry[kind]
	return registered
}

func targetRegistrySnapshot() map[Kind]targetKindSpec {
	registryMu.RLock()
	defer registryMu.RUnlock()
	snapshot := make(map[Kind]targetKindSpec, len(registry))
	for kind, spec := range registry {
		schema := copyBodySchema(*spec.Schema)
		snapshot[kind] = targetKindSpec{
			TargetKindSpec: TargetKindSpec{
				Schema:    &schema,
				Decoder:   spec.Decoder,
				Graph:     spec.Graph,
				Execution: spec.Execution,
				Config:    spec.Config,
			},
			script: spec.script,
		}
	}
	return snapshot
}

func copyBodySchema(schema hcl.BodySchema) hcl.BodySchema {
	return hcl.BodySchema{
		Attributes: append([]hcl.AttributeSchema(nil), schema.Attributes...),
		Blocks:     append([]hcl.BlockHeaderSchema(nil), schema.Blocks...),
	}
}

type decodedTarget struct {
	Script string
	Deps   []dependency
}

func validateDecoderResult(kind Kind, decoded any) error {
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

func targetTraversalValue(kind Kind, name string) string {
	return targetTraversalValuePrefix + string(kind) + "." + name
}

const targetReferenceValuePrefix = "attehcl-target-ref:"

func targetReferenceValue(file reference.Blob, kind Kind, name string) string {
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
		kind: Kind(kind),
		name: name,
	}, true
}

func targetTraversal(kind Kind, name string) hcl.Traversal {
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

func decodeTargetDependencies(attribute *hcl.Attribute, ctx *hcl.EvalContext) ([]dependency, error) {
	value, diagnostics := attribute.Expr.Value(dependencyEvalContext(ctx))
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

	for _, name := range []string{"depends_on", "triggered_by"} {
		attribute, ok := content.Attributes[name]
		if !ok {
			continue
		}
		dependencies, err := decodeTargetDependencies(attribute, ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		decoded.Deps = append(decoded.Deps, dependencies...)
	}
	return decoded, nil
}
