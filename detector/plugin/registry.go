// Package plugin provides runtime registration for detector capabilities.
package plugin

import (
	"context"
	"fmt"
	"maps"
	"sort"

	"github.com/ghthor/atte/detector"
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

// Detector contains capabilities supplied by a detector namespace.
type Detector struct {
	Namespace string
	Graph     func(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
	Targets   func(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
	DecodeID  func(graph.EntityID) (graph.Entity, error)
}

// HCLFunctionFactory constructs a function for one repository and HCL file.
type HCLFunctionFactory func(context.Context, *attegit.Repo, reference.Blob) (function.Function, error)

// Registry exposes the immutable capabilities of a compiled plugin registry.
type Registry interface {
	// Targets discovers declared and implicit targets across all registered
	// detectors for repo.
	Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)

	// Graph combines the graphs produced by all registered detectors for repo.
	// Each option is passed to every detector that contributes a graph.
	Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)

	// TargetKinds returns the registered HCL target-kind specifications.
	// The returned map and any schemas it contains are independent copies that
	// callers may modify without changing the registry.
	TargetKinds() map[attehcl.Kind]attehcl.TargetKindSpec

	// DecodeID resolves id by dispatching it to the detector registered for its
	// namespace.
	DecodeID(graph.EntityID) (graph.Entity, error)

	// HCLFunctions constructs fresh HCL functions for repo and file. Factories
	// receive the context, repository, and source file so functions can be
	// scoped to the evaluation being performed.
	HCLFunctions(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)
}

// Builder collects detector, target-kind, and HCL function registrations.
// Builders are setup-only values, are not safe for concurrent mutation, and
// must be compiled before being passed to detector consumers.
type Builder struct {
	detectors   map[string]Detector
	functions   map[string]HCLFunctionFactory
	targetKinds map[attehcl.Kind]attehcl.TargetKindSpec
	includeHCL  bool
}

type compiledRegistry struct {
	detectors       []Detector
	detectorsByName map[string]Detector
	functions       map[string]HCLFunctionFactory
	targetKinds     map[attehcl.Kind]attehcl.TargetKindSpec
}

var _ Registry = (*compiledRegistry)(nil)

// New returns an empty plugin builder.
func New() *Builder {
	return &Builder{
		detectors:   make(map[string]Detector),
		functions:   make(map[string]HCLFunctionFactory),
		targetKinds: make(map[attehcl.Kind]attehcl.TargetKindSpec),
	}
}

// Compile returns an immutable runtime registry containing the registrations
// made on b. Later changes to b do not affect the returned registry.
func (b *Builder) Compile() Registry {
	if b == nil {
		return nil
	}
	result := &compiledRegistry{
		detectorsByName: make(map[string]Detector, len(b.detectors)+1),
		functions:       maps.Clone(b.functions),
		targetKinds:     cloneTargetKinds(b.targetKinds),
		detectors:       make([]Detector, 0, len(b.detectors)+1),
	}
	for _, detector := range b.detectors {
		result.detectors = append(result.detectors, detector)
		result.detectorsByName[detector.Namespace] = detector
	}
	if b.includeHCL {
		detector := adaptDetector(attehcl.NewDetector(result))
		result.detectors = append(result.detectors, detector)
		result.detectorsByName[detector.Namespace] = detector
	}
	sort.Slice(result.detectors, func(i, j int) bool {
		return result.detectors[i].Namespace < result.detectors[j].Namespace
	})
	return result
}

// RegisterHCLBlock adds a target kind to a plugin builder. A nil schema uses
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

// Register adds a detector to a plugin builder. Namespaces must be unique and non-empty.
func (b *Builder) Register(detector Detector) error {
	if b == nil {
		return fmt.Errorf("builder is nil")
	}
	if detector.Namespace == "" {
		return fmt.Errorf("detector namespace is empty")
	}
	if b.includeHCL && detector.Namespace == attehcl.Namespace {
		return fmt.Errorf("detector namespace %q is already registered", detector.Namespace)
	}
	if detector.Graph == nil && detector.Targets == nil && detector.DecodeID == nil {
		return fmt.Errorf("detector %q has no capabilities", detector.Namespace)
	}
	if _, exists := b.detectors[detector.Namespace]; exists {
		return fmt.Errorf("detector namespace %q is already registered", detector.Namespace)
	}
	b.detectors[detector.Namespace] = detector
	return nil
}

// RegisterDetector registers a method-based detector and its optional capabilities.
func (b *Builder) RegisterDetector(value detector.Detector) error {
	if value == nil {
		return fmt.Errorf("detector is nil")
	}
	return b.Register(adaptDetector(value))
}

func adaptDetector(value detector.Detector) Detector {
	adapted := Detector{Namespace: value.Namespace()}
	if graphDetector, ok := value.(detector.GraphDetector); ok {
		adapted.Graph = graphDetector.Graph
	}
	if targetDetector, ok := value.(detector.TargetDetector); ok {
		adapted.Targets = targetDetector.Targets
	}
	if entityDecoder, ok := value.(detector.EntityDecoder); ok {
		adapted.DecodeID = entityDecoder.DecodeID
	}
	return adapted
}

// RegisterHCLFunction registers a named HCL function factory on a plugin builder.
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
func (r *compiledRegistry) TargetKinds() map[attehcl.Kind]attehcl.TargetKindSpec {
	return cloneTargetKinds(r.targetKinds)
}

// HCLFunctions returns fresh functions for the repository and file.
func (r *compiledRegistry) HCLFunctions(ctx context.Context, repo *attegit.Repo, file reference.Blob) (map[string]function.Function, error) {
	result := make(map[string]function.Function, len(r.functions))
	for name, factory := range r.functions {
		fn, err := factory(ctx, repo, file)
		if err != nil {
			return nil, fmt.Errorf("build HCL function %q: %w", name, err)
		}
		result[name] = fn
	}
	return result, nil
}

// Graph combines all compiled detector graphs in namespace order.
func (r *compiledRegistry) Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
	var result *graph.Graph
	for _, detector := range r.detectors {
		if detector.Graph == nil {
			continue
		}
		g, err := detector.Graph(ctx, repo, options...)
		if err != nil {
			return nil, fmt.Errorf("build %s graph: %w", detector.Namespace, err)
		}
		if g == nil {
			continue
		}
		if result == nil {
			result = g
			continue
		}
		if err := result.Absorb(g); err != nil {
			return nil, fmt.Errorf("merge %s graph: %w", detector.Namespace, err)
		}
	}
	return result, nil
}

// Targets returns all compiled detector targets in namespace order.
func (r *compiledRegistry) Targets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	var targets []graphtarget.ID
	for _, detector := range r.detectors {
		if detector.Targets == nil {
			continue
		}
		found, err := detector.Targets(ctx, repo)
		if err != nil {
			return nil, fmt.Errorf("discover %s targets: %w", detector.Namespace, err)
		}
		targets = append(targets, found...)
	}
	return targets, nil
}

// DecodeID resolves an entity ID using the detector registered for its namespace.
func (r *compiledRegistry) DecodeID(id graph.EntityID) (graph.Entity, error) {
	namespace := id.Namespace()
	if namespace == "" {
		return graph.Entity{}, fmt.Errorf("entity ID %q has no namespace", id)
	}
	d, ok := r.detectorsByName[namespace]
	if !ok {
		return graph.Entity{}, fmt.Errorf("no detector registered for entity namespace %q", namespace)
	}
	if d.DecodeID == nil {
		return graph.Entity{}, fmt.Errorf("detector %q does not decode entity IDs", namespace)
	}
	entity, err := d.DecodeID(id)
	if err != nil {
		return graph.Entity{}, fmt.Errorf("decode %q entity ID: %w", namespace, err)
	}
	return entity, nil
}
