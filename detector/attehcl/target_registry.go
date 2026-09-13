package attehcl

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
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
	ResolveTarget func(hcl.Traversal) (graph.Entity, error)
	EntityKind    func(graph.EntityID) (string, error)
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
			Decoder:   decodeTestTarget,
			Graph:     graphTestTarget,
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

func decodeTargetDependencies(attribute *hcl.Attribute, ctx *hcl.EvalContext) ([]dependency, error) {
	list, ok := attribute.Expr.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil, fmt.Errorf("must be a literal list")
	}
	dependencies := make([]dependency, 0, len(list.Exprs))
	for _, element := range list.Exprs {
		if traversal, ok := element.(*hclsyntax.ScopeTraversalExpr); ok {
			dependencies = append(dependencies, dependency{traversal: traversal.Traversal})
			continue
		}
		value, diagnostics := element.Value(ctx)
		if diagnostics.HasErrors() {
			return nil, targetDiagnosticsError{diagnostics: diagnostics}
		}
		if value.Type() == attegit.RepositoryPathType {
			blob := *value.EncapsulatedValue().(*reference.Blob)
			dependencies = append(dependencies, dependency{value: DecodingPathPrefix + blob.String()})
			continue
		}
		if value.Type() != cty.String {
			return nil, fmt.Errorf("values must be strings or paths")
		}
		raw := value.AsString()
		if value, ok := strings.CutPrefix(raw, "attehcl-id:"); ok {
			dependencies = append(dependencies, dependency{entity: graph.EntityID(value)})
		} else if strings.HasPrefix(raw, "attego:") {
			dependencies = append(dependencies, dependency{entity: graph.EntityID(raw)})
		} else {
			dependencies = append(dependencies, dependency{value: raw})
		}
	}
	return dependencies, nil
}

type targetDiagnosticsError struct {
	diagnostics hcl.Diagnostics
}

func (err targetDiagnosticsError) Error() string {
	return err.diagnostics.Error()
}

func decodeTestTarget(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
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
