package selector

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ghthor/atte/detector/graphtarget"
)

// MatchFunc matches an identifier against a detector target.
type MatchFunc func(graphtarget.ID, string) bool

// SelectorFunc converts a detector target into its canonical selector string.
type SelectorFunc func(graphtarget.ID) string

// Mapping contains selector operations keyed by detector namespace.
type Mapping struct {
	Matchers  map[graphtarget.Namespace]MatchFunc
	Selectors map[graphtarget.Namespace]SelectorFunc
}

var mappings = struct {
	sync.RWMutex
	value Mapping
}{value: Mapping{
	Matchers:  make(map[graphtarget.Namespace]MatchFunc),
	Selectors: make(map[graphtarget.Namespace]SelectorFunc),
}}

// Register associates selector operations with namespace. A namespace may only
// be registered once.
func Register(namespace graphtarget.Namespace, match MatchFunc, selector SelectorFunc) error {
	if namespace == "" {
		return fmt.Errorf("selector namespace is empty")
	}
	if match == nil {
		return fmt.Errorf("selector matcher for namespace %q is nil", namespace)
	}
	if selector == nil {
		return fmt.Errorf("selector converter for namespace %q is nil", namespace)
	}

	mappings.Lock()
	defer mappings.Unlock()
	if _, exists := mappings.value.Matchers[namespace]; exists {
		return fmt.Errorf("selector namespace %q is already registered", namespace)
	}
	mappings.value.Matchers[namespace] = match
	mappings.value.Selectors[namespace] = selector
	return nil
}

// String converts target to its canonical selector string using the mapping
// registered for its namespace.
func String(target graphtarget.ID) (string, bool) {
	mappings.RLock()
	selectorize, ok := mappings.value.Selectors[target.Namespace]
	mappings.RUnlock()
	if !ok {
		return "", false
	}
	return selectorize(target), true
}

// Matches reports whether input selects target from relative, a
// repository-relative directory, using the mapping registered for its
// namespace.
func Matches(target graphtarget.ID, input, relative string) bool {
	mappings.RLock()
	match, ok := mappings.value.Matchers[target.Namespace]
	selectorize, selectorOK := mappings.value.Selectors[target.Namespace]
	mappings.RUnlock()
	if !ok || !selectorOK {
		return false
	}
	canonical, err := Parse(selectorize(target))
	if err != nil {
		return false
	}
	rawPath, _, splitErr := split(input)
	if splitErr != nil && !strings.Contains(input, "#") {
		rawPath, _, splitErr = split("#" + input)
	}
	if splitErr != nil {
		return false
	}
	parsed, err := Resolve(input, relative)
	if err != nil {
		if !strings.Contains(input, "#") {
			parsed, err = Resolve("#"+input, relative)
		}
		if err != nil {
			return false
		}
	}
	candidateDir := ContainingDir(canonical.Path)
	pathless := strings.HasPrefix(strings.TrimSpace(input), "#") || !strings.Contains(input, "#")
	if rawPath == "" && !pathless && candidateDir != "" {
		return false
	}
	if !PathMatches(rawPath, relative, canonical.Path, candidateDir) {
		return false
	}
	return match(target, parsed.Identifier)
}
