package detector

import (
	"context"
	"fmt"
	"maps"
	"sort"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty/function"
)

// SensorSpec describes the capabilities supplied by one detector Sensor.
type SensorSpec struct {
	Namespace string
	Graph     func(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
	Targets   func(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
	DecodeID  func(graph.EntityID) (graph.Entity, error)
}

// HCLFunctionFactory constructs a function for one repository and HCL file.
type HCLFunctionFactory func(context.Context, *attegit.Repo, reference.Blob) (function.Function, error)

// Scanner executes the compiled detector Sensors and scans repositories for
// targets and relationships.
type Scanner interface {
	// Targets discovers declared and implicit targets across all registered
	// Sensors for repo.
	Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)

	// Graph combines the graphs produced by all registered Sensors for repo.
	// Each option is passed to every Sensor that contributes a graph.
	Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)

	// TargetKinds returns the registered HCL target-kind specifications.
	// The returned map and any schemas it contains are independent copies that
	// callers may modify without changing the Scanner.
	TargetKinds() map[attehcl.Kind]attehcl.TargetKindSpec

	// DecodeID resolves id by dispatching it to the Sensor registered for its
	// namespace.
	DecodeID(graph.EntityID) (graph.Entity, error)

	// HCLFunctions constructs fresh HCL functions for repo and file. Factories
	// receive the context, repository, and source file so functions can be
	// scoped to the evaluation being performed.
	HCLFunctions(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)
}

// Builder collects Sensor, target-kind, and HCL function registrations.
// Builders are setup-only values, are not safe for concurrent mutation, and
// must be compiled before being passed to detector consumers.
type Builder struct {
	sensors     map[string]SensorSpec
	functions   map[string]HCLFunctionFactory
	targetKinds map[attehcl.Kind]attehcl.TargetKindSpec
	includeHCL  bool
}

type compiledScanner struct {
	sensors       []SensorSpec
	sensorsByName map[string]SensorSpec
	functions     map[string]HCLFunctionFactory
	targetKinds   map[attehcl.Kind]attehcl.TargetKindSpec
}

var (
	_ Scanner              = (*compiledScanner)(nil)
	_ attehcl.Capabilities = (*compiledScanner)(nil)
)

// NewBuilder returns an empty detector Builder.
func NewBuilder() *Builder {
	return &Builder{
		sensors:     make(map[string]SensorSpec),
		functions:   make(map[string]HCLFunctionFactory),
		targetKinds: make(map[attehcl.Kind]attehcl.TargetKindSpec),
	}
}

// Compile returns an immutable runtime Scanner containing the registrations
// made on b. Later changes to b do not affect the returned Scanner.
func (b *Builder) Compile() Scanner {
	if b == nil {
		return nil
	}
	result := &compiledScanner{
		sensorsByName: make(map[string]SensorSpec, len(b.sensors)+1),
		functions:     maps.Clone(b.functions),
		targetKinds:   cloneTargetKinds(b.targetKinds),
		sensors:       make([]SensorSpec, 0, len(b.sensors)+1),
	}
	for _, sensor := range b.sensors {
		result.sensors = append(result.sensors, sensor)
		result.sensorsByName[sensor.Namespace] = sensor
	}
	if b.includeHCL {
		sensor := adaptSensor(attehcl.NewDetector(result))
		result.sensors = append(result.sensors, sensor)
		result.sensorsByName[sensor.Namespace] = sensor
	}
	sort.Slice(result.sensors, func(i, j int) bool {
		return result.sensors[i].Namespace < result.sensors[j].Namespace
	})
	return result
}

// RegisterHCLBlock adds a target kind to a detector Builder. A nil schema uses
// the built-in script-target schema. Registration rejects duplicate kinds.
//
// This is a function rather than a method because Go does not yet support
// generic methods on non-generic types. Once the minimum Go version reaches
// Go 1.27 and generic methods are available, this can become a Builder method.
func RegisterHCLBlock[K ~string](b *Builder, kind K, spec attehcl.TargetKindSpec) error {
	if b == nil {
		return fmt.Errorf("builder is nil")
	}
	name := string(kind)
	if name == "" {
		return fmt.Errorf("target kind is empty")
	}
	if !hclsyntax.ValidIdentifier(name) {
		return fmt.Errorf("target kind %q is not a valid HCL identifier", kind)
	}
	if spec.Decoder == nil {
		return fmt.Errorf("target kind %q has no decoder", kind)
	}
	if spec.Schema != nil {
		schema := copyBodySchema(*spec.Schema)
		spec.Schema = &schema
	}
	key := attehcl.Kind(kind)
	if _, exists := b.targetKinds[key]; exists {
		return fmt.Errorf("target kind %q is already registered", kind)
	}
	b.targetKinds[key] = spec
	return nil
}

func cloneTargetKinds(source map[attehcl.Kind]attehcl.TargetKindSpec) map[attehcl.Kind]attehcl.TargetKindSpec {
	result := make(map[attehcl.Kind]attehcl.TargetKindSpec, len(source))
	for kind, spec := range source {
		if spec.Schema != nil {
			schema := copyBodySchema(*spec.Schema)
			spec.Schema = &schema
		}
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

// Register adds a SensorSpec to a detector Builder. Namespaces must be unique
// and non-empty.
func (b *Builder) Register(sensor SensorSpec) error {
	if b == nil {
		return fmt.Errorf("builder is nil")
	}
	if sensor.Namespace == "" {
		return fmt.Errorf("sensor namespace is empty")
	}
	if b.includeHCL && sensor.Namespace == attehcl.Namespace {
		return fmt.Errorf("sensor namespace %q is already registered", sensor.Namespace)
	}
	if sensor.Graph == nil && sensor.Targets == nil && sensor.DecodeID == nil {
		return fmt.Errorf("sensor %q has no capabilities", sensor.Namespace)
	}
	if _, exists := b.sensors[sensor.Namespace]; exists {
		return fmt.Errorf("sensor namespace %q is already registered", sensor.Namespace)
	}
	b.sensors[sensor.Namespace] = sensor
	return nil
}

// RegisterSensor registers a method-based Sensor and its optional capabilities.
func (b *Builder) RegisterSensor(value Sensor) error {
	if value == nil {
		return fmt.Errorf("sensor is nil")
	}
	return b.Register(adaptSensor(value))
}

func adaptSensor(value Sensor) SensorSpec {
	adapted := SensorSpec{Namespace: value.Namespace()}
	if sensor, ok := value.(SensorGraph); ok {
		adapted.Graph = sensor.Graph
	}
	if sensor, ok := value.(SensorTarget); ok {
		adapted.Targets = sensor.Targets
	}
	if sensor, ok := value.(SensorEntityDecoder); ok {
		adapted.DecodeID = sensor.DecodeID
	}
	return adapted
}

// RegisterHCLFunction registers a named HCL function factory on a detector
// Builder.
func (b *Builder) RegisterHCLFunction(name string, factory HCLFunctionFactory) error {
	if b == nil {
		return fmt.Errorf("builder is nil")
	}
	if name == "" {
		return fmt.Errorf("HCL function name is empty")
	}
	if factory == nil {
		return fmt.Errorf("HCL function %q factory is nil", name)
	}
	if _, exists := b.functions[name]; exists {
		return fmt.Errorf("HCL function %q is already registered", name)
	}
	b.functions[name] = factory
	return nil
}

// TargetKinds returns a copy of the compiled target-kind capabilities.
func (s *compiledScanner) TargetKinds() map[attehcl.Kind]attehcl.TargetKindSpec {
	return cloneTargetKinds(s.targetKinds)
}

// HCLFunctions returns fresh functions for the repository and file.
func (s *compiledScanner) HCLFunctions(ctx context.Context, repo *attegit.Repo, file reference.Blob) (map[string]function.Function, error) {
	result := make(map[string]function.Function, len(s.functions))
	for name, factory := range s.functions {
		fn, err := factory(ctx, repo, file)
		if err != nil {
			return nil, fmt.Errorf("build HCL function %q: %w", name, err)
		}
		result[name] = fn
	}
	return result, nil
}

// Graph combines all compiled Sensor graphs in namespace order.
func (s *compiledScanner) Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
	var result *graph.Graph
	for _, sensor := range s.sensors {
		if sensor.Graph == nil {
			continue
		}
		g, err := sensor.Graph(ctx, repo, options...)
		if err != nil {
			return nil, fmt.Errorf("build %s graph: %w", sensor.Namespace, err)
		}
		if g == nil {
			continue
		}
		if result == nil {
			result = g
			continue
		}
		if err := result.Absorb(g); err != nil {
			return nil, fmt.Errorf("merge %s graph: %w", sensor.Namespace, err)
		}
	}
	return result, nil
}

// Targets returns all compiled Sensor targets in namespace order.
func (s *compiledScanner) Targets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	var targets []graphtarget.ID
	for _, sensor := range s.sensors {
		if sensor.Targets == nil {
			continue
		}
		found, err := sensor.Targets(ctx, repo)
		if err != nil {
			return nil, fmt.Errorf("discover %s targets: %w", sensor.Namespace, err)
		}
		targets = append(targets, found...)
	}
	return targets, nil
}

// DecodeID resolves an entity ID using the Sensor registered for its namespace.
func (s *compiledScanner) DecodeID(id graph.EntityID) (graph.Entity, error) {
	namespace := id.Namespace()
	if namespace == "" {
		return graph.Entity{}, fmt.Errorf("entity ID %q has no namespace", id)
	}
	sensor, ok := s.sensorsByName[namespace]
	if !ok {
		return graph.Entity{}, fmt.Errorf("no Sensor registered for entity namespace %q", namespace)
	}
	if sensor.DecodeID == nil {
		return graph.Entity{}, fmt.Errorf("Sensor %q does not decode entity IDs", namespace)
	}
	entity, err := sensor.DecodeID(id)
	if err != nil {
		return graph.Entity{}, fmt.Errorf("decode %q entity ID: %w", namespace, err)
	}
	return entity, nil
}
