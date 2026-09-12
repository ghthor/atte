package runcomp

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
)

func Match(repoRoot, wd, toComplete string, targets []string) ([]string, error) {
	path, id, ok := strings.Cut(toComplete, "#")
	if ok {
		id = "#" + id
	}
	slices.Sort(targets)

	if strings.HasPrefix(path, "/") {
		return matchCanonicalPath(toComplete, targets), nil
	}

	if hasRelative(wd, path) {
		return nil, fmt.Errorf("relative completion matching is unsupported")
	}

	tree, err := toTree(repoRoot, wd, reference.SomePath(path))
	if err != nil {
		return nil, err
	}

	relWd, err := filepath.Rel(repoRoot, wd)
	if err != nil {
		return nil, fmt.Errorf("working dir not relative to repository root: %w", err)
	}

	if relWd == "." {
		relWd = reference.Root.String()
	}

	// Short matches first
	wdTargets := matchToDirectory(relWd, toComplete, targets)

	// Long matches ordered second
	matches := matchRepoPath(string(tree)+id, targets)
	if relWd != "" {
		for i, m := range matches {
			matches[i] = strings.TrimPrefix(m, relWd+"/")
		}
	}
	wdTargets = append(wdTargets, matches...)

	return wdTargets, nil
}

func hasRelative(wd, path string) bool {
	if path == "" {
		return false
	}

	if path[0] == '.' {
		return true
	}

	if strings.Contains(path, "..") {
		return true
	}

	clean := filepath.Clean(filepath.Join(wd, path))
	return !strings.HasPrefix(clean, wd)
}

func toTree(repoRoot, wd string, input reference.SomePath) (reference.Tree, error) {
	p := filepath.Join(wd, string(input))
	rel, err := filepath.Rel(repoRoot, p)
	if err != nil {
		return "", fmt.Errorf("failed to resolve repo relative path: %w", err)
	}

	return reference.Tree(rel), nil
}

func matchCanonicalPath(toMatch string, targets []string) []string {
	matches := make([]string, 0, len(targets))
	for _, t := range targets {
		tt := t
		if strings.HasPrefix(tt, toMatch) {
			matches = append(matches, t)
		}
	}
	return matches
}

func matchRepoPath(toMatch string, targets []string) []string {
	matches := make([]string, 0, len(targets))
	for _, t := range targets {
		tt := strings.TrimPrefix(t, "//")
		if strings.HasPrefix(tt, toMatch) {
			matches = append(matches, tt)
		}
	}
	return matches
}

func matchToDirectory(wd, toComplete string, targets []string) []string {
	matches := make([]string, 0, len(targets))
	for _, t := range targets {
		tt := strings.TrimPrefix(t, "//")
		s, err := selector.Parse(tt)
		if err != nil {
			panic(fmt.Errorf("all targets should be valid selectors: %v", targets))
		}
		if wd == s.Tree().String() && (toComplete == "" || strings.HasPrefix(s.Identifier, toComplete)) {
			matches = append(matches, s.Identifier)
		}
	}
	return matches
}
