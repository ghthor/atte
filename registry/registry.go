// Package registry provides runtime registration for detector capabilities.
package registry

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/graph"
	"github.com/ghthor/atte/reference/selector"
)

// Target is detector-neutral runnable target metadata.
type Target struct {
	ID        graph.EntityID
	Namespace string
	Kind      string
	Path      string
	Name      string
	Index     int
}

// Detector contains capabilities supplied by a detector namespace.
type Detector struct {
	Namespace       string
	Graph           func(*attegit.Repo) (*graph.Graph, error)
	Targets         func(*attegit.Repo) ([]Target, error)
	Selectorize     func(Target) (selector.Target, bool)
	MatchIdentifier func(Target, string) bool
}

// Registry stores runtime detector registrations.
type Registry struct {
	mu        sync.RWMutex
	detectors map[string]Detector
}

// New returns an empty detector registry.
func New() *Registry {
	return &Registry{detectors: make(map[string]Detector)}
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

// Graph combines all registered detector graphs in namespace order.
func (r *Registry) Graph(repo *attegit.Repo) (*graph.Graph, error) {
	var result *graph.Graph
	for _, detector := range r.snapshot() {
		if detector.Graph == nil {
			continue
		}
		g, err := detector.Graph(repo)
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
func (r *Registry) Targets(repo *attegit.Repo) ([]Target, error) {
	var targets []Target
	for _, detector := range r.snapshot() {
		if detector.Targets == nil {
			continue
		}
		found, err := detector.Targets(repo)
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
	candidateDir := strings.TrimSuffix(canonical.Path, "/atte.hcl")
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
