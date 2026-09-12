package attehcl

import (
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

const (
	KindTest    Kind = "test"
	KindCodegen Kind = "codegen"
	KindLint    Kind = "lint"
)

type targetKindSpec struct {
	schema  hcl.BodySchema
	decoder TargetDecoder
}

var (
	registryMu sync.RWMutex
	registry   = make(map[Kind]targetKindSpec)
)

func init() {
	for _, kind := range []Kind{KindTest, KindCodegen, KindLint} {
		if err := Register(kind, decodeTestTarget); err != nil {
			panic(err)
		}
	}
}

var testSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "script"},
		{Name: "depends_on"},
		{Name: "triggered_by"},
	},
}

// Register adds a target kind to the process-wide target registry. A nil or
// omitted schema uses the built-in target schema. Registration rejects duplicate
// kinds. Each evaluator takes a stable registry snapshot when it is created.
func Register[K ~string](kind K, decoder TargetDecoder, schemas ...*hcl.BodySchema) error {
	name := string(kind)
	if kind == "" {
		return fmt.Errorf("target kind is empty")
	}
	if !hclsyntax.ValidIdentifier(name) {
		return fmt.Errorf("target kind %q is not a valid HCL identifier", kind)
	}
	if decoder == nil {
		return fmt.Errorf("target kind %q has no decoder", kind)
	}
	if len(schemas) > 1 {
		return fmt.Errorf("target kind %q received more than one schema", kind)
	}

	schema := testSchema
	if len(schemas) == 1 && schemas[0] != nil {
		schema = copyBodySchema(*schemas[0])
	} else {
		schema = copyBodySchema(schema)
	}

	key := Kind(kind)
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[key]; exists {
		return fmt.Errorf("target kind %q is already registered", kind)
	}
	registry[key] = targetKindSpec{schema: schema, decoder: decoder}
	return nil
}

func targetRegistrySnapshot() map[Kind]targetKindSpec {
	registryMu.RLock()
	defer registryMu.RUnlock()
	snapshot := make(map[Kind]targetKindSpec, len(registry))
	for kind, spec := range registry {
		snapshot[kind] = targetKindSpec{schema: copyBodySchema(spec.schema), decoder: spec.decoder}
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
