// Package plugin provides runtime registration for detector capabilities.
package plugin

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"sync"

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

// Registry stores runtime detector, target-kind, and HCL function registrations.
type Registry struct {
	mu          sync.RWMutex
	detectors   map[string]Detector
	functions   map[string]HCLFunctionFactory
	targetKinds map[attehcl.Kind]attehcl.TargetKindSpec
}

// New returns an empty detector registry.
func New() *Registry {
	return &Registry{
		detectors:   make(map[string]Detector),
		functions:   make(map[string]HCLFunctionFactory),
		targetKinds: make(map[attehcl.Kind]attehcl.TargetKindSpec),
	}
}

// RegisterHCLBlock adds a target kind to a plugin registry. A nil schema uses the
// built-in script-target schema. Registration rejects duplicate kinds.
//
// This is a function rather than a method because Go does not yet support
// generic methods on non-generic types. Once the minimum Go version reaches
// Go 1.27 and generic methods are available, this can become a Registry method.
func RegisterHCLBlock[K ~string](r *Registry, kind K, spec attehcl.TargetKindSpec) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.targetKinds[key]; exists {
		return fmt.Errorf("target kind %q is already registered", kind)
	}
	r.targetKinds[key] = spec
	return nil
}

// TargetKinds returns a snapshot of target-kind capabilities registered with r.
func (r *Registry) TargetKinds() map[attehcl.Kind]attehcl.TargetKindSpec {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[attehcl.Kind]attehcl.TargetKindSpec, len(r.targetKinds))
	for kind, spec := range r.targetKinds {
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

// Register adds a detector. Namespaces must be unique and non-empty.
func (r *Registry) Register(detector Detector) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if detector.Namespace == "" {
		return fmt.Errorf("detector namespace is empty")
	}
	if detector.Graph == nil && detector.Targets == nil && detector.DecodeID == nil {
		return fmt.Errorf("detector %q has no capabilities", detector.Namespace)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.detectors[detector.Namespace]; exists {
		return fmt.Errorf("detector namespace %q is already registered", detector.Namespace)
	}
	r.detectors[detector.Namespace] = detector
	return nil
}

// RegisterDetector registers a method-based detector and its optional capabilities.
func (r *Registry) RegisterDetector(value detector.Detector) error {
	if value == nil {
		return fmt.Errorf("detector is nil")
	}
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
	return r.Register(adapted)
}

// RegisterHCLFunction registers a named HCL function factory.
func (r *Registry) RegisterHCLFunction(name string, factory HCLFunctionFactory) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if name == "" {
		return fmt.Errorf("HCL function name is empty")
	}
	if factory == nil {
		return fmt.Errorf("HCL function %q factory is nil", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.functions[name]; exists {
		return fmt.Errorf("HCL function %q is already registered", name)
	}
	r.functions[name] = factory
	return nil
}

// HCLFunctions returns fresh functions for the repository and file.
func (r *Registry) HCLFunctions(ctx context.Context, repo *attegit.Repo, file reference.Blob) (map[string]function.Function, error) {
	if r == nil {
		return nil, fmt.Errorf("registry is nil")
	}
	r.mu.RLock()
	factories := make(map[string]HCLFunctionFactory, len(r.functions))
	maps.Copy(factories, r.functions)
	r.mu.RUnlock()
	result := make(map[string]function.Function, len(factories))
	for name, factory := range factories {
		fn, err := factory(ctx, repo, file)
		if err != nil {
			return nil, fmt.Errorf("build HCL function %q: %w", name, err)
		}
		result[name] = fn
	}
	return result, nil
}

// Graph combines all registered detector graphs in namespace order.
func (r *Registry) Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
	var result *graph.Graph
	for _, detector := range r.snapshot() {
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

// Targets returns all registered detector targets in namespace order.
func (r *Registry) Targets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	var targets []graphtarget.ID
	for _, detector := range r.snapshot() {
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
func (r *Registry) DecodeID(id graph.EntityID) (graph.Entity, error) {
	if r == nil {
		return graph.Entity{}, fmt.Errorf("registry is nil")
	}
	namespace := id.Namespace()
	if namespace == "" {
		return graph.Entity{}, fmt.Errorf("entity ID %q has no namespace", id)
	}
	r.mu.RLock()
	d, ok := r.detectors[namespace]
	r.mu.RUnlock()
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

func (r *Registry) snapshot() []Detector {
	r.mu.RLock()
	result := make([]Detector, 0, len(r.detectors))
	for _, detector := range r.detectors {
		result = append(result, detector)
	}
	r.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].Namespace < result[j].Namespace })
	return result
}
