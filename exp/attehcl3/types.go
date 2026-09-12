// Package attehcl implements the experimental file-local HCL detector.
package attehcl

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

const (
	Filename  = "atte.hcl"
	Namespace = "attehcl"

	TestKind    = Namespace + ":test"
	CodegenKind = Namespace + ":codegen"
	LintKind    = Namespace + ":lint"

	SourceFileRelation graph.RelationKind = "source-file"
	ScriptRelation     graph.RelationKind = "script"
	DependsOnRelation  graph.RelationKind = "depends-on"

	DecodingPathPrefix = "attehcl-path:"
)

// Kind identifies a registered target kind without its detector namespace.
type Kind string

const (
	KindTest    Kind = "test"
	KindCodegen Kind = "codegen"
	KindLint    Kind = "lint"
)

// TargetDecoder decodes a schema-validated target body into a kind-owned value.
type TargetDecoder func(*hcl.BodyContent, *hcl.EvalContext) (any, error)

type targetKindSpec struct {
	schema  hcl.BodySchema
	decoder TargetDecoder
}

var (
	registryMu sync.RWMutex
	registry   = make(map[Kind]targetKindSpec)
)

var testSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "script"},
		{Name: "depends_on"},
		{Name: "triggered_by"},
	},
}

func init() {
	for _, kind := range []Kind{KindTest, KindCodegen, KindLint} {
		if err := Register(kind, decodeScriptTarget); err != nil {
			panic(err)
		}
	}
}

// Register adds a kind to this experiment's private target registry.
func Register[K ~string](kind K, decoder TargetDecoder, schemas ...*hcl.BodySchema) error {
	name := string(kind)
	if name == "" {
		return fmt.Errorf("target kind is empty")
	}
	if !hclsyntax.ValidIdentifier(name) {
		return fmt.Errorf("target kind %q is not a valid HCL identifier", name)
	}
	if decoder == nil {
		return fmt.Errorf("target kind %q has no decoder", kind)
	}
	if len(schemas) > 1 {
		return fmt.Errorf("target kind %q received more than one schema", kind)
	}
	schema := copyBodySchema(testSchema)
	if len(schemas) == 1 && schemas[0] != nil {
		schema = copyBodySchema(*schemas[0])
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

// TargetDeclaration is the identity and source location of a recognized target
// declaration. Its body has not been schema-validated or evaluated.
type TargetDeclaration struct {
	ID     graph.EntityID
	Kind   Kind
	File   reference.Blob
	Name   string
	Index  int
	Source hcl.Range
}

// Target describes a fully decoded target declaration.
type Target struct {
	ID      graph.EntityID
	Kind    string
	File    reference.Blob
	Name    string
	Label   string
	Index   int
	Aliases []string
	Script  reference.Blob
	Inline  string
	Decoded any
	Source  hcl.Range
}

// Config is the evaluated target configuration for a repository file.
type Config struct {
	Targets map[Kind][]Target
}

type dependency struct {
	value     string
	entity    graph.EntityID
	traversal hcl.Traversal
}

type scriptTarget struct {
	Script string
	Deps   []dependency
}

type targetDiagnosticsError struct {
	diagnostics hcl.Diagnostics
}

func (err targetDiagnosticsError) Error() string { return err.diagnostics.Error() }

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
		switch {
		case strings.HasPrefix(raw, "attehcl-id:"):
			dependencies = append(dependencies, dependency{entity: graph.EntityID(strings.TrimPrefix(raw, "attehcl-id:"))})
		case strings.HasPrefix(raw, "attego:"):
			dependencies = append(dependencies, dependency{entity: graph.EntityID(raw)})
		default:
			dependencies = append(dependencies, dependency{value: raw})
		}
	}
	return dependencies, nil
}

func decodeScriptTarget(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
	decoded := scriptTarget{}
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

// DisplayName returns the stable identifier component for a target.
func (target Target) DisplayName() string { return displayName(target.Name, target.Index) }

// Command constructs a command used to execute the target from a repository root.
func (target Target) Command(root string) (*exec.Cmd, error) {
	if target.Script != "" {
		return exec.Command("/usr/bin/env", "bash", filepath.Join(root, filepath.FromSlash(target.Script.String()))), nil
	}
	if target.Inline != "" {
		return exec.Command("/usr/bin/env", "bash", "-c", target.Inline), nil
	}
	return nil, fmt.Errorf("target %q has no script", target.ID)
}

// Selector converts a target to its selector-facing identity.
func Selector(target Target) selector.Target {
	return selector.Target{
		Path:    target.File.String(),
		Kind:    strings.TrimPrefix(target.Kind, Namespace+":"),
		Name:    target.DisplayName(),
		Index:   target.Index,
		Aliases: target.Aliases,
	}
}

func EntityID(kind string, file reference.Blob, name string) graph.EntityID {
	return graph.EntityID(fmt.Sprintf("%s:%s:%s", kind, file, name))
}

func DecodeEntityID(id graph.EntityID) (string, reference.Blob, string, error) {
	parts := strings.SplitN(string(id), ":", 4)
	if len(parts) != 4 || parts[0] != Namespace {
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	kind := Kind(parts[1])
	if _, registered := targetRegistrySnapshot()[kind]; !registered {
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	file, err := reference.ParseBlob(parts[2])
	if err != nil {
		return "", "", "", fmt.Errorf("invalid HCL file path %q: %w", parts[2], err)
	}
	return Namespace + ":" + parts[1], file, parts[3], nil
}

func objectValue(values map[string]cty.Value) cty.Value {
	if len(values) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(values)
}

func displayName(name string, index int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("%d", index)
}

func isNumericName(name string) bool {
	return name != "" && strings.Trim(name, "0123456789") == ""
}
