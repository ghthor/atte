package detector

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcltarget"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty/function"
)

// SensorSpec describes the capabilities supplied by one detector Sensor.
type SensorSpec struct {
	Namespace      string
	Graph          func(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
	Targets        func(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
	TargetSelector func(graphtarget.ID) selector.Target
	ExecuteTarget  func(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error)
	DecodeID       func(graph.EntityID) (graph.Entity, error)
}

// HCLFunctionFactory constructs a function for one repository and HCL file.
type HCLFunctionFactory = func(context.Context, *attegit.Repo, reference.Blob) (function.Function, error)

// Scanner executes the compiled detector Sensors and scans repositories for
// targets and relationships.
type Scanner interface {
	// Targets discovers executable targets across all attached Sensors for repo.
	Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)

	// TargetSelector returns the Sensor-owned selector presentation for target.
	TargetSelector(graphtarget.ID) (selector.Target, bool)

	// TargetString renders target's canonical selector when its Sensor supports
	// target presentation.
	TargetString(graphtarget.ID) (string, bool)

	// TargetMatches reports whether input selects target from relative.
	TargetMatches(graphtarget.ID, string, string) bool

	// ResolveTarget discovers and resolves one unique target in this Scanner.
	ResolveTarget(context.Context, *attegit.Repo, string, string) (graphtarget.ID, error)

	// ExecuteTarget returns the command for a discovered target.
	ExecuteTarget(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error)

	// Graph combines the graphs produced by all attached Sensors for repo.
	// Each option is passed to every Sensor that contributes a graph.
	Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)

	// TargetKinds returns the attached HCL target-kind specifications.
	// The returned map and any schemas it contains are independent copies that
	// callers may modify without changing the Scanner.
	TargetKinds() map[attehcltarget.Kind]attehcltarget.KindSpec

	// DecodeID resolves id by dispatching it to the Sensor attached for its
	// namespace.
	DecodeID(graph.EntityID) (graph.Entity, error)

	// HCLFunctions constructs fresh HCL functions for repo and file. Factories
	// receive the context, repository, and source file so functions can be
	// scoped to the evaluation being performed.
	HCLFunctions(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)
}

// Builder collects Sensor, target-kind, and HCL function attachments.
// Builders are setup-only values, are not safe for concurrent mutation, and
// must be compiled before being passed to detector consumers.
type Builder struct {
	sensors        map[string]SensorSpec
	scannerSensors map[string]Sensor
	functions      map[string]HCLFunctionFactory
	targetKinds    map[attehcltarget.Kind]attehcltarget.KindSpec
}

type compiledScanner struct {
	sensors       []SensorSpec
	sensorsByName map[string]SensorSpec
	functions     map[string]HCLFunctionFactory
	targetKinds   map[attehcltarget.Kind]attehcltarget.KindSpec
}

var (
	_ Scanner                        = (*compiledScanner)(nil)
	_ attehcltarget.KindCapabilities = (*compiledScanner)(nil)
)

// NewBuilder returns an empty detector Builder.
func NewBuilder() *Builder {
	return &Builder{
		sensors:        make(map[string]SensorSpec),
		scannerSensors: make(map[string]Sensor),
		functions:      make(map[string]HCLFunctionFactory),
		targetKinds:    make(map[attehcltarget.Kind]attehcltarget.KindSpec),
	}
}

// Compile returns an immutable runtime Scanner containing the attachments
// made on b. Scanner-aware Sensors receive that Scanner during compilation.
// Later changes to b do not affect the returned Scanner.
func (b *Builder) Compile() (Scanner, error) {
	if b == nil {
		return nil, fmt.Errorf("builder is nil")
	}
	sensorCount := len(b.sensors) + len(b.scannerSensors)
	result := &compiledScanner{
		sensorsByName: make(map[string]SensorSpec, sensorCount),
		functions:     maps.Clone(b.functions),
		targetKinds:   cloneTargetKinds(b.targetKinds),
		sensors:       make([]SensorSpec, 0, sensorCount),
	}
	for _, sensor := range b.sensors {
		result.sensors = append(result.sensors, sensor)
		result.sensorsByName[sensor.Namespace] = sensor
	}
	namespaces := make([]string, 0, len(b.scannerSensors))
	for namespace := range b.scannerSensors {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	for _, namespace := range namespaces {
		sensor := b.scannerSensors[namespace]
		scannerSensor, ok := sensor.(SensorWithScanner)
		if !ok {
			return nil, fmt.Errorf("sensor %q does not support scanner attachment", namespace)
		}
		bound, err := scannerSensor.AttachScanner(result)
		if err != nil {
			return nil, fmt.Errorf("attach scanner to sensor %q: %w", namespace, err)
		}
		boundSensor, ok := bound.(Sensor)
		if !ok {
			return nil, fmt.Errorf("scanner-aware sensor %q returned %T, which is not a Sensor", namespace, bound)
		}
		if boundSensor.Namespace() != namespace {
			return nil, fmt.Errorf("scanner-aware sensor %q returned Sensor with namespace %q", namespace, boundSensor.Namespace())
		}
		adapted := adaptSensor(boundSensor)
		result.sensors = append(result.sensors, adapted)
		result.sensorsByName[adapted.Namespace] = adapted
	}
	sort.Slice(result.sensors, func(i, j int) bool {
		return result.sensors[i].Namespace < result.sensors[j].Namespace
	})
	return result, nil
}

// AttachHCLTargetBlock adds a target kind to a detector Builder. A nil schema uses
// the built-in script-target schema. Attachment rejects duplicate kinds.
//
// This is a function rather than a method because Go does not yet support
// generic methods on non-generic types. Once the minimum Go version reaches
// Go 1.27 and generic methods are available, this can become a Builder method.
func AttachHCLTargetBlock[K ~string](b *Builder, kind K, spec attehcltarget.KindSpec) error {
	if b == nil {
		return fmt.Errorf("builder is nil")
	}
	key := attehcltarget.Kind(kind)
	normalized, err := b.normalizeHCLTargetBlock(key, spec)
	if err != nil {
		return err
	}
	b.targetKinds[key] = normalized
	return nil
}

func (b *Builder) normalizeHCLTargetBlock(kind attehcltarget.Kind, spec attehcltarget.KindSpec) (attehcltarget.KindSpec, error) {
	name := string(kind)
	if name == "" {
		return spec, fmt.Errorf("target kind is empty")
	}
	if !hclsyntax.ValidIdentifier(name) {
		return spec, fmt.Errorf("target kind %q is not a valid HCL identifier", kind)
	}
	if spec.Decoder == nil {
		return spec, fmt.Errorf("target kind %q has no decoder", kind)
	}
	if _, exists := b.targetKinds[kind]; exists {
		return spec, fmt.Errorf("target kind %q is already attached", kind)
	}
	if spec.Schema != nil {
		schema := copyBodySchema(*spec.Schema)
		spec.Schema = &schema
	}
	return spec, nil
}

func cloneTargetKinds(source map[attehcltarget.Kind]attehcltarget.KindSpec) map[attehcltarget.Kind]attehcltarget.KindSpec {
	result := make(map[attehcltarget.Kind]attehcltarget.KindSpec, len(source))
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

// Attach adds a SensorSpec to a detector Builder. Namespaces must be unique
// and non-empty.
func (b *Builder) Attach(sensor SensorSpec) error {
	if err := b.validateSensor(sensor); err != nil {
		return err
	}
	b.sensors[sensor.Namespace] = sensor
	return nil
}

func (b *Builder) validateSensor(sensor SensorSpec) error {
	if b == nil {
		return fmt.Errorf("builder is nil")
	}
	if sensor.Namespace == "" {
		return fmt.Errorf("sensor namespace is empty")
	}
	targetCapability := sensor.Targets != nil || sensor.TargetSelector != nil || sensor.ExecuteTarget != nil
	if targetCapability {
		switch {
		case sensor.Targets == nil:
			return fmt.Errorf("sensor %q target capability is missing Targets", sensor.Namespace)
		case sensor.TargetSelector == nil:
			return fmt.Errorf("sensor %q target capability is missing TargetSelector", sensor.Namespace)
		case sensor.ExecuteTarget == nil:
			return fmt.Errorf("sensor %q target capability is missing ExecuteTarget", sensor.Namespace)
		}
	}
	if sensor.Graph == nil && !targetCapability && sensor.DecodeID == nil {
		return fmt.Errorf("sensor %q has no capabilities", sensor.Namespace)
	}
	if _, exists := b.sensors[sensor.Namespace]; exists {
		return fmt.Errorf("sensor namespace %q is already attached", sensor.Namespace)
	}
	if _, exists := b.scannerSensors[sensor.Namespace]; exists {
		return fmt.Errorf("sensor namespace %q is already attached", sensor.Namespace)
	}
	return nil
}

// AttachSensor attaches a method-based Sensor and any HCL capabilities it
// provides. Sensors implementing SensorWithScanner receive the compiled Scanner.
func (b *Builder) AttachSensor(value Sensor) error {
	if b == nil {
		return fmt.Errorf("builder is nil")
	}
	if value == nil {
		return fmt.Errorf("sensor is nil")
	}
	sensor := adaptSensor(value)
	_, scannerAware := value.(SensorWithScanner)
	if err := b.validateSensor(sensor); err != nil {
		return err
	}
	functions, blocks := sensorCapabilities(value)
	if err := b.validateHCLCapabilities(functions, blocks); err != nil {
		return err
	}
	if scannerAware {
		b.scannerSensors[sensor.Namespace] = value
	} else {
		b.sensors[sensor.Namespace] = sensor
	}
	b.attachHCLCapabilities(functions, blocks)
	return nil
}

func sensorCapabilities(value Sensor) (map[string]HCLFunctionFactory, map[attehcltarget.Kind]attehcltarget.KindSpec) {
	functions := make(map[string]HCLFunctionFactory)
	if provider, ok := value.(SensorProvidingHCLFunctions); ok {
		maps.Copy(functions, provider.HCLFunctions())
	}
	blocks := make(map[attehcltarget.Kind]attehcltarget.KindSpec)
	if provider, ok := value.(SensorProvidingHCLTargetBlocks); ok {
		maps.Copy(blocks, provider.HCLTargetBlocks())
	}
	return functions, blocks
}

func (b *Builder) validateHCLCapabilities(functions map[string]HCLFunctionFactory, blocks map[attehcltarget.Kind]attehcltarget.KindSpec) error {
	functionNames := make([]string, 0, len(functions))
	for name := range functions {
		functionNames = append(functionNames, name)
	}
	sort.Strings(functionNames)
	for _, name := range functionNames {
		if err := b.validateHCLFunction(name, functions[name]); err != nil {
			return err
		}
	}

	blockKinds := make([]attehcltarget.Kind, 0, len(blocks))
	for kind := range blocks {
		blockKinds = append(blockKinds, kind)
	}
	slices.Sort(blockKinds)
	for _, kind := range blockKinds {
		if _, err := b.normalizeHCLTargetBlock(kind, blocks[kind]); err != nil {
			return err
		}
	}
	return nil
}

func (b *Builder) attachHCLCapabilities(functions map[string]HCLFunctionFactory, blocks map[attehcltarget.Kind]attehcltarget.KindSpec) {
	maps.Copy(b.functions, functions)
	for kind, spec := range blocks {
		normalized, _ := b.normalizeHCLTargetBlock(kind, spec)
		b.targetKinds[kind] = normalized
	}
}

func adaptSensor(value Sensor) SensorSpec {
	adapted := SensorSpec{Namespace: value.Namespace()}
	if sensor, ok := value.(GraphSensor); ok {
		adapted.Graph = sensor.Graph
	}
	if sensor, ok := value.(TargetSensor); ok {
		adapted.Targets = sensor.Targets
		adapted.TargetSelector = sensor.TargetSelector
		adapted.ExecuteTarget = sensor.ExecuteTarget
	}
	if sensor, ok := value.(EntityDecodingSensor); ok {
		adapted.DecodeID = sensor.DecodeID
	}
	return adapted
}

// AttachHCLFunction attaches a named HCL function factory on a detector
// Builder.
func (b *Builder) AttachHCLFunction(name string, factory HCLFunctionFactory) error {
	if b == nil {
		return fmt.Errorf("builder is nil")
	}
	if err := b.validateHCLFunction(name, factory); err != nil {
		return err
	}
	b.functions[name] = factory
	return nil
}

func (b *Builder) validateHCLFunction(name string, factory HCLFunctionFactory) error {
	if name == "" {
		return fmt.Errorf("HCL function name is empty")
	}
	if factory == nil {
		return fmt.Errorf("HCL function %q factory is nil", name)
	}
	if _, exists := b.functions[name]; exists {
		return fmt.Errorf("HCL function %q is already attached", name)
	}
	return nil
}

// TargetKinds returns a copy of the compiled target-kind capabilities.
func (s *compiledScanner) TargetKinds() map[attehcltarget.Kind]attehcltarget.KindSpec {
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

// Targets returns all compiled executable targets in namespace order.
func (s *compiledScanner) Targets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	targets := make([]graphtarget.ID, 0)
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

// TargetSelector dispatches to the Sensor that owns target's namespace.
func (s *compiledScanner) TargetSelector(target graphtarget.ID) (selector.Target, bool) {
	sensor, ok := s.sensorsByName[string(target.Namespace)]
	if !ok || sensor.TargetSelector == nil {
		return selector.Target{}, false
	}
	return sensor.TargetSelector(target), true
}

// TargetString renders target's canonical selector using its attached Sensor.
func (s *compiledScanner) TargetString(target graphtarget.ID) (string, bool) {
	ts, ok := s.TargetSelector(target)
	if !ok {
		return "", false
	}
	return ts.String(), true
}

// TargetMatches reports whether input selects target from relative.
func (s *compiledScanner) TargetMatches(target graphtarget.ID, input, relative string) bool {
	ts, ok := s.TargetSelector(target)
	return ok && ts.Matches(input, relative)
}

// ResolveTarget discovers and resolves one unique target using this Scanner's
// attached Sensors for both discovery and selector matching.
func (s *compiledScanner) ResolveTarget(ctx context.Context, repo *attegit.Repo, input, relative string) (graphtarget.ID, error) {
	if err := ctx.Err(); err != nil {
		return graphtarget.ID{}, err
	}
	targets, err := s.Targets(ctx, repo)
	if err != nil {
		return graphtarget.ID{}, fmt.Errorf("resolve target %q: discover targets: %w", input, err)
	}
	type match struct {
		target   graphtarget.ID
		selector string
	}
	matches := make([]match, 0)
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return graphtarget.ID{}, err
		}
		ts, ok := s.TargetSelector(target)
		if !ok {
			return graphtarget.ID{}, fmt.Errorf("resolve target %q: target %q in namespace %q has no selector capability", input, target.ID, target.Namespace)
		}
		if s.TargetMatches(target, input, relative) {
			matches = append(matches, match{target: target, selector: ts.String()})
		}
	}
	if err := ctx.Err(); err != nil {
		return graphtarget.ID{}, err
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].selector != matches[j].selector {
			return matches[i].selector < matches[j].selector
		}
		return matches[i].target.ID < matches[j].target.ID
	})
	switch len(matches) {
	case 0:
		return graphtarget.ID{}, &selector.NoMatchError{Input: input}
	case 1:
		return matches[0].target, nil
	default:
		candidates := make([]selector.AmbiguousCandidate, 0, len(matches))
		for _, candidate := range matches {
			candidates = append(candidates, selector.AmbiguousCandidate{
				TargetID: string(candidate.target.ID),
				Selector: candidate.selector,
			})
		}
		return graphtarget.ID{}, &selector.AmbiguousError{Input: input, Candidates: candidates}
	}
}

// ExecuteTarget dispatches command construction to the Sensor that owns target.
func (s *compiledScanner) ExecuteTarget(ctx context.Context, repo *attegit.Repo, root string, target graphtarget.ID) (graphtarget.Execution, error) {
	if err := ctx.Err(); err != nil {
		return graphtarget.Execution{}, err
	}
	sensor, ok := s.sensorsByName[string(target.Namespace)]
	if !ok {
		return graphtarget.Execution{}, fmt.Errorf("no Sensor attached for target namespace %q", target.Namespace)
	}
	if sensor.ExecuteTarget == nil {
		return graphtarget.Execution{}, fmt.Errorf("Sensor %q cannot execute targets", target.Namespace)
	}
	execution, err := sensor.ExecuteTarget(ctx, repo, root, target)
	if err != nil {
		return graphtarget.Execution{}, fmt.Errorf("construct command for target %q: %w", target.ID, err)
	}
	if len(execution.Args) == 0 {
		return graphtarget.Execution{}, fmt.Errorf("construct command for target %q: command has no arguments", target.ID)
	}
	return execution, nil
}

// DecodeID resolves an entity ID using the Sensor attached for its namespace.
func (s *compiledScanner) DecodeID(id graph.EntityID) (graph.Entity, error) {
	namespace := id.Namespace()
	if namespace == "" {
		return graph.Entity{}, fmt.Errorf("entity ID %q has no namespace", id)
	}
	sensor, ok := s.sensorsByName[namespace]
	if !ok {
		return graph.Entity{}, fmt.Errorf("no Sensor attached for entity namespace %q", namespace)
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
