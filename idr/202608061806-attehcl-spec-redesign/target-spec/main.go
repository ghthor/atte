// target-spec is a self-contained executable experiment for the atte.hcl
// target model. It intentionally does not import any package from this
// repository; the small path value and path function below are local copies of
// the concepts needed by the experiment.
package main

import (
	"fmt"
	"log"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

const source = `
package "go" {
  command = "go test ./..."
}

codegen "go" {
  script = "go generate ./..."
}

target "lint" {
  script = "go vet ."
}

test {
}

test "go" {
  script = <<EOF
go test ./...
EOF
  depends_on = [
    codegen.go,
    path("./generated.go"),
  ]
}
`

var testSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "script"},
		{Name: "depends_on"},
	},
}

var packageSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "command", Required: true},
	},
}

var schemas = map[Kind]kindSpec{}

// repositoryPath is the experiment's local representation of a repository-
// relative path. It is carried through HCL as a capsule so it cannot be
// confused with an HCL target identifier string.
type repositoryPath string

var repositoryPathType = cty.CapsuleWithOps(
	"target_spec.repository_path",
	reflect.TypeFor[repositoryPath](),
	&cty.CapsuleOps{
		GoString: func(value any) string {
			return fmt.Sprintf("path(%q)", value.(repositoryPath))
		},
		RawEquals: func(a, b any) bool {
			return a.(repositoryPath) == b.(repositoryPath)
		},
	},
)

// Kind identifies the schema and identity namespace of a target. Kinds may
// share a schema without becoming aliases of one another.
type Kind string

const (
	KindTest    Kind = "test"
	KindCodegen Kind = "codegen"
	KindLint    Kind = "lint"
)

// TargetDecoder decodes the validated body of one target. The decoder owns
// the concrete value stored in Target.Decoded. Target-reference traversals
// should remain unresolved during this enumeration phase.
type TargetDecoder func(*hcl.BodyContent, *hcl.EvalContext) (any, error)

type kindSpec struct {
	schema  hcl.BodySchema
	decoder TargetDecoder
}

// Register adds a target kind and its decoder. Omitting the schema, or passing
// nil, uses the test schema. Shared schemas do not make registered kinds
// aliases: the kind remains part of every target identity.
func Register(kind string, decoder TargetDecoder, schema ...*hcl.BodySchema) error {
	if kind == "" {
		return fmt.Errorf("target kind is empty")
	}
	if !hclsyntax.ValidIdentifier(kind) {
		return fmt.Errorf("target kind %q is not a valid HCL identifier", kind)
	}
	if decoder == nil {
		return fmt.Errorf("target kind %q has no decoder", kind)
	}
	if schemas == nil {
		schemas = make(map[Kind]kindSpec)
	}
	key := Kind(kind)
	if _, exists := schemas[key]; exists {
		return fmt.Errorf("target kind %q is already registered", kind)
	}
	if len(schema) > 1 {
		return fmt.Errorf("target kind %q received more than one schema", kind)
	}

	selected := testSchema
	if len(schema) == 1 && schema[0] != nil {
		selected = *schema[0]
	}
	schemas[key] = kindSpec{schema: selected, decoder: decoder}
	return nil
}

func registerBuiltins() error {
	schemas = make(map[Kind]kindSpec, 4)
	if err := Register(string(KindTest), decodeTestTarget); err != nil {
		return err
	}
	if err := Register(string(KindCodegen), decodeTestTarget); err != nil {
		return err
	}
	if err := Register(string(KindLint), decodeTestTarget); err != nil {
		return err
	}
	return Register("package", decodePackageTarget, &packageSchema)
}

// Target is one target in a directory-local evaluation. Index is the
// deterministic zero-based source position among declarations of the target
// kind. Name is empty for an anonymous target. Decoded contains the kind-owned
// value returned by the decoder registered for Kind.
type Target struct {
	Kind    Kind
	Name    string
	Index   int
	Decoded any
}

// TestTarget is the value decoded by the built-in test schema.
type TestTarget struct {
	Script     string
	ScriptPath repositoryPath
	DependsOn  []Dependency
}

// PackageTarget demonstrates that a registered kind can define and decode its
// own schema without changing target enumeration.
type PackageTarget struct {
	Command string
}

// Dependency is either an unresolved HCL target identifier or a repository
// path. Target references deliberately do not use strings such as "test.0";
// the HCL traversal remains available to the later resolution phase.
type Dependency struct {
	Target hcl.Traversal
	Path   repositoryPath
}

func (dependency Dependency) String() string {
	if dependency.Target != nil {
		return traversalText(dependency.Target)
	}
	return "path(" + string(dependency.Path) + ")"
}

type normalizedBlock struct {
	block *hclsyntax.Block
	kind  Kind
	name  string
	index int
}

func main() {
	if err := registerBuiltins(); err != nil {
		log.Fatal(err)
	}

	targets, err := Evaluate("atte.hcl", []byte(source))
	if err != nil {
		log.Fatal(err)
	}

	type row struct {
		id     string
		kind   Kind
		target Target
	}
	rows := make([]row, 0)
	for kind, kindTargets := range targets {
		for _, target := range kindTargets {
			rows = append(rows, row{id: targetID(kind, target), kind: kind, target: target})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })

	fmt.Println("evaluated targets:")
	for _, row := range rows {
		fmt.Printf("  %-10s kind=%q", row.id, row.target.Kind)
		if row.target.Name != "" {
			fmt.Printf(" name=%q", row.target.Name)
		}
		printDecodedTarget(row.target.Decoded)
		fmt.Println()
	}
}

func printDecodedTarget(decoded any) {
	switch target := decoded.(type) {
	case TestTarget:
		if target.Script != "" {
			fmt.Printf(" script=%q", target.Script)
		}
		if target.ScriptPath != "" {
			fmt.Printf(" script_path=%q", target.ScriptPath)
		}
		if len(target.DependsOn) > 0 {
			dependencies := make([]string, 0, len(target.DependsOn))
			for _, dependency := range target.DependsOn {
				dependencies = append(dependencies, dependency.String())
			}
			fmt.Printf(" depends_on=%q", dependencies)
		}
	case PackageTarget:
		fmt.Printf(" command=%q", target.Command)
	default:
		fmt.Printf(" decoded=%T", decoded)
	}
}

// Evaluate parses and evaluates exactly one atte.hcl file. No other file is
// read, so the target evaluation boundary is isolated to one directory.
// Target-reference identifiers are only required to be syntactically valid;
// enumeration does not resolve whether a referenced target exists.
func Evaluate(filename string, source []byte) (map[Kind][]Target, error) {
	parsed, diagnostics := hclparse.NewParser().ParseHCL(source, filename)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("parse %q: %s", filename, diagnostics.Error())
	}
	body, ok := parsed.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse %q: expected HCL syntax body, got %T", filename, parsed.Body)
	}

	blocks, err := normalizeBlocks(body)
	if err != nil {
		return nil, fmt.Errorf("decode %q: %w", filename, err)
	}
	ctx := evaluationContext(filename)
	targets := make(map[Kind][]Target, len(schemas))
	for _, normalized := range blocks {
		target, err := decodeTarget(normalized, ctx)
		if err != nil {
			return nil, fmt.Errorf("decode %q target %s: %w", filename, targetID(normalized.kind, Target{Name: normalized.name, Index: normalized.index}), err)
		}
		targets[normalized.kind] = append(targets[normalized.kind], target)
	}
	return targets, nil
}

func normalizeBlocks(body *hclsyntax.Body) ([]normalizedBlock, error) {
	blocks := make([]normalizedBlock, 0, len(body.Blocks))
	kindIndexes := make(map[Kind]int, len(schemas))
	named := make(map[Kind]map[string]struct{}, len(schemas))

	for _, block := range body.Blocks {
		kind, name, err := blockKindAndName(block)
		if err != nil {
			return nil, err
		}
		if _, registered := schemas[kind]; !registered {
			return nil, fmt.Errorf("unknown target kind %q", kind)
		}
		index := kindIndexes[kind]
		kindIndexes[kind]++
		if name != "" {
			if isNumericName(name) {
				return nil, fmt.Errorf("%s target name %q must not be numeric", kind, name)
			}
			if named[kind] == nil {
				named[kind] = make(map[string]struct{})
			}
			if _, exists := named[kind][name]; exists {
				return nil, fmt.Errorf("duplicate %s target name %q", kind, name)
			}
			named[kind][name] = struct{}{}
		}
		blocks = append(blocks, normalizedBlock{block: block, kind: kind, name: name, index: index})
	}
	return blocks, nil
}

func blockKindAndName(block *hclsyntax.Block) (Kind, string, error) {
	if block.Type == "target" {
		if len(block.Labels) < 1 || len(block.Labels) > 2 {
			return "", "", fmt.Errorf("target block must have one or two labels")
		}
		return Kind(block.Labels[0]), optionalLabel(block.Labels, 1), nil
	}
	if _, registered := schemas[Kind(block.Type)]; !registered {
		return "", "", fmt.Errorf("unknown block %q; short form requires a registered target kind", block.Type)
	}
	if len(block.Labels) > 1 {
		return "", "", fmt.Errorf("short-form %s block must have zero or one label", block.Type)
	}
	return Kind(block.Type), optionalLabel(block.Labels, 0), nil
}

func optionalLabel(labels []string, index int) string {
	if index >= len(labels) {
		return ""
	}
	return labels[index]
}

func isNumericName(name string) bool {
	return name != "" && strings.Trim(name, "0123456789") == ""
}

func targetID(kind Kind, target Target) string {
	if target.Name != "" {
		return string(kind) + "." + target.Name
	}
	return string(kind) + "." + strconv.Itoa(target.Index)
}

func decodeTarget(block normalizedBlock, ctx *hcl.EvalContext) (Target, error) {
	spec := schemas[block.kind]
	content, diagnostics := block.block.Body.Content(&spec.schema)
	if diagnostics.HasErrors() {
		return Target{}, fmt.Errorf("%s", diagnostics.Error())
	}
	decoded, err := spec.decoder(content, ctx)
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: block.kind, Name: block.name, Index: block.index, Decoded: decoded}, nil
}

func decodeTestTarget(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
	target := TestTarget{}
	if attribute, ok := content.Attributes["script"]; ok {
		value, diagnostics := attribute.Expr.Value(ctx)
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("script: %s", diagnostics.Error())
		}
		switch value.Type() {
		case cty.String:
			target.Script = value.AsString()
		case repositoryPathType:
			target.ScriptPath = *value.EncapsulatedValue().(*repositoryPath)
		default:
			return nil, fmt.Errorf("script must be a string or path")
		}
	}
	if attribute, ok := content.Attributes["depends_on"]; ok {
		dependencies, err := decodeDependencies(attribute.Expr, ctx)
		if err != nil {
			return nil, err
		}
		target.DependsOn = dependencies
	}
	return target, nil
}

func decodePackageTarget(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
	value, diagnostics := content.Attributes["command"].Expr.Value(ctx)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("command: %s", diagnostics.Error())
	}
	if value.Type() != cty.String {
		return nil, fmt.Errorf("command must be a string")
	}
	return PackageTarget{Command: value.AsString()}, nil
}

func decodeDependencies(expression hcl.Expression, ctx *hcl.EvalContext) ([]Dependency, error) {
	list, ok := expression.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil, fmt.Errorf("depends_on must be a list of HCL identifiers or paths")
	}
	dependencies := make([]Dependency, 0, len(list.Exprs))
	for _, element := range list.Exprs {
		if target, ok := element.(*hclsyntax.ScopeTraversalExpr); ok {
			if err := validateTargetTraversal(target.Traversal); err != nil {
				return nil, fmt.Errorf("depends_on target: %w", err)
			}
			dependencies = append(dependencies, Dependency{Target: target.Traversal})
			continue
		}

		value, diagnostics := element.Value(ctx)
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("depends_on: %s", diagnostics.Error())
		}
		if value.Type() != repositoryPathType {
			return nil, fmt.Errorf("depends_on members must be HCL identifiers or paths")
		}
		pathValue := *value.EncapsulatedValue().(*repositoryPath)
		dependencies = append(dependencies, Dependency{Path: pathValue})
	}
	return dependencies, nil
}

func validateTargetTraversal(traversal hcl.Traversal) error {
	if len(traversal) == 0 {
		return fmt.Errorf("target identifier is empty")
	}
	for index, traverser := range traversal {
		switch traverser := traverser.(type) {
		case hcl.TraverseRoot:
			if index != 0 || !hclsyntax.ValidIdentifier(traverser.Name) {
				return fmt.Errorf("target identifier must contain HCL identifiers")
			}
		case hcl.TraverseAttr:
			if !hclsyntax.ValidIdentifier(traverser.Name) {
				return fmt.Errorf("target identifier must contain HCL identifiers")
			}
		default:
			return fmt.Errorf("target identifier must be an HCL traversal")
		}
	}
	return nil
}

func evaluationContext(filename string) *hcl.EvalContext {
	variables := make(map[string]cty.Value, len(schemas))
	for kind := range schemas {
		// DynamicVal permits any syntactically valid target traversal to remain
		// unresolved during enumeration. The later graph phase resolves it.
		variables[string(kind)] = cty.DynamicVal
	}
	return &hcl.EvalContext{
		Variables: variables,
		Functions: map[string]function.Function{"path": pathFunction(filename)},
	}
}

func pathFunction(filename string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(repositoryPathType),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			resolved, err := resolvePath(filename, args[0].AsString())
			if err != nil {
				return cty.NilVal, err
			}
			return cty.CapsuleVal(repositoryPathType, &resolved), nil
		},
	})
}

func resolvePath(filename, raw string) (repositoryPath, error) {
	base := path.Dir(filename)
	resolved := path.Clean(path.Join(base, raw))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("path %q escapes the repository", raw)
	}
	return repositoryPath(resolved), nil
}

func traversalText(traversal hcl.Traversal) string {
	var builder strings.Builder
	for index, traverser := range traversal {
		switch traverser := traverser.(type) {
		case hcl.TraverseRoot:
			if index > 0 {
				builder.WriteByte('.')
			}
			builder.WriteString(traverser.Name)
		case hcl.TraverseAttr:
			builder.WriteByte('.')
			builder.WriteString(traverser.Name)
		}
	}
	return builder.String()
}
