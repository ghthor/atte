// Package registry provides runtime registration for detector capabilities.
package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/graph"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty/function"
)

// Target is detector-neutral runnable target metadata.

type Target struct {
	ID        graph.EntityID
	Namespace string
	Kind      string
	Path      string
	Name      string
	Index     int
	Aliases   []string
}

// Detector contains capabilities supplied by a detector namespace.
type Detector struct {
	Namespace       string
	Graph           func(context.Context, *attegit.Repo, ...detector.GraphOption) (*graph.Graph, error)
	Targets         func(context.Context, *attegit.Repo) ([]Target, error)
	Selectorize     func(Target) (selector.Target, bool)
	MatchIdentifier func(Target, string) bool
}

// Plugin is the required base capability for a runtime plugin.
type Plugin interface {
	Graph(context.Context, *attegit.Repo, ...detector.GraphOption) (*graph.Graph, error)
}

// HCLFunctionFactory constructs a function for one repository and HCL file.
type HCLFunctionFactory func(context.Context, *attegit.Repo, reference.Blob) (function.Function, error)

// HCLFunctionProvider is implemented by plugins that expose HCL functions.
type HCLFunctionProvider interface {
	HCLFunctions(context.Context, *attegit.Repo, reference.Blob) (map[string]HCLFunctionFactory, error)
}

// HCLBlockHandler is a future extension point for named parsed HCL blocks.
type HCLBlockHandler interface {
	HCLBlockType() string
	HandleHCLBlock(*hclsyntax.Block, *hcl.EvalContext, HCLGraphBuilder) error
}

// HCLGraphBuilder is intentionally small so block handlers cannot replace parsing.
type HCLGraphBuilder interface {
	AddEntity(graph.Entity)
	AddRelationship(graph.Relationship)
}

// Registry stores runtime detector and plugin registrations.
type Registry struct {
	mu        sync.RWMutex
	detectors map[string]Detector
	plugins   []Plugin
	functions map[string]HCLFunctionFactory
}

// New returns an empty detector registry.
func New() *Registry {
	return &Registry{detectors: make(map[string]Detector), functions: make(map[string]HCLFunctionFactory)}
}

// Register adds a detector. Namespaces must be unique and non-empty.
func (r *Registry) Register(detector Detector) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if detector.Namespace == "" {
		return fmt.Errorf("detector namespace is empty")
	}
	if detector.Graph == nil && detector.Targets == nil && detector.Selectorize == nil {
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

// RegisterPlugin adds a plugin and discovers its optional capabilities.
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

// FunctionProvider adapts registry HCL functions to detector evaluation.
func (r *Registry) FunctionProvider() detector.FunctionProvider {
	return func(ctx context.Context, repo *attegit.Repo, file reference.Blob) (map[string]function.Function, error) {
		return r.HCLFunctions(ctx, repo, file)
	}
}

// HCLFunctions returns fresh functions for the repository and file.
func (r *Registry) HCLFunctions(ctx context.Context, repo *attegit.Repo, file reference.Blob) (map[string]function.Function, error) {
	if r == nil {
		return nil, fmt.Errorf("registry is nil")
	}
	r.mu.RLock()
	factories := make(map[string]HCLFunctionFactory, len(r.functions))
	for name, factory := range r.functions {
		factories[name] = factory
	}
	plugins := append([]Plugin(nil), r.plugins...)
	r.mu.RUnlock()
	result := make(map[string]function.Function, len(factories))
	for _, plugin := range plugins {
		provider, ok := plugin.(HCLFunctionProvider)
		if !ok {
			continue
		}
		provided, err := provider.HCLFunctions(ctx, repo, file)
		if err != nil {
			return nil, err
		}
		for name, factory := range provided {
			if _, exists := factories[name]; exists {
				return nil, fmt.Errorf("HCL function %q is already registered", name)
			}
			factories[name] = factory
		}
	}
	for name, factory := range factories {
		fn, err := factory(ctx, repo, file)
		if err != nil {
			return nil, fmt.Errorf("build HCL function %q: %w", name, err)
		}
		result[name] = fn
	}
	return result, nil
}

// RegisterPlugin adds a plugin as a graph detector and discovers optional HCL capabilities.
func (r *Registry) RegisterPlugin(namespace string, plugin Plugin) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if namespace == "" {
		return fmt.Errorf("plugin namespace is empty")
	}
	if plugin == nil {
		return fmt.Errorf("plugin %q is nil", namespace)
	}
	if err := r.Register(Detector{Namespace: namespace, Graph: func(ctx context.Context, repo *attegit.Repo, options ...detector.GraphOption) (*graph.Graph, error) {
		return plugin.Graph(ctx, repo, options...)
	}}); err != nil {
		return err
	}
	r.mu.Lock()
	r.plugins = append(r.plugins, plugin)
	r.mu.Unlock()
	return nil
}

// Graph combines all registered detector graphs in namespace order.
func (r *Registry) Graph(ctx context.Context, repo *attegit.Repo, options ...detector.GraphOption) (*graph.Graph, error) {
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
func (r *Registry) Targets(ctx context.Context, repo *attegit.Repo) ([]Target, error) {
	var targets []Target
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

// Selector returns the canonical selector for a target when its detector provides one.
func (r *Registry) Selector(target Target) (selector.Target, bool) {
	r.mu.RLock()
	detector, ok := r.detectors[target.Namespace]
	r.mu.RUnlock()
	if !ok || detector.Selectorize == nil {
		return selector.Target{}, false
	}
	return detector.Selectorize(target)
}

// Matches reports whether input selects target from a repository-relative directory.
// Path resolution is handled by the selector package; identifier interpretation is
// delegated to the detector registered for target.Namespace.
func (r *Registry) Matches(target Target, input, relative string) bool {
	canonical, ok := r.Selector(target)
	if !ok {
		return false
	}
	parsed, err := selector.Resolve(input, relative)
	if err != nil {
		if !strings.Contains(input, "#") {
			parsed, err = selector.Resolve("#"+input, relative)
		}
		if err != nil {
			return false
		}
	}
	candidateDir := strings.TrimSuffix(canonical.Path, "/"+attehcl.Filename)
	pathless := strings.HasPrefix(strings.TrimSpace(input), "#") || !strings.Contains(input, "#")
	if parsed.Path == "" && !pathless && candidateDir != "" {
		return false
	}
	if !selector.PathMatches(parsed.Path, relative, canonical.Path, candidateDir) {
		return false
	}
	if detector, ok := r.detector(target.Namespace); ok && detector.MatchIdentifier != nil {
		return detector.MatchIdentifier(target, parsed.Identifier)
	}
	canonicalSelector, err := selector.Parse(canonical.String())
	if err != nil {
		return false
	}
	return parsed.Identifier == canonicalSelector.Identifier
}

func (r *Registry) detector(namespace string) (Detector, bool) {
	r.mu.RLock()
	detector, ok := r.detectors[namespace]
	r.mu.RUnlock()
	return detector, ok
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
