// Package reference resolves repository-relative and Bazel-style file references.
package reference

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/ghthor/atte/detector/attegit"
)

// ResolveFromBlob converts a reference to a repository-relative path, resolving
// relative references against the directory containing blob.
func ResolveFromBlob(blob attegit.Path, value string) (attegit.Path, error) {
	return ResolveFromDir(attegit.Path(path.Dir(string(blob))), value)
}

// ResolveFromDir converts a reference to a repository-relative path, resolving
// relative references against dir.
func ResolveFromDir(dir attegit.Path, value string) (attegit.Path, error) {
	clean, err := resolve(dir, value)
	if err != nil {
		return "", err
	}
	return attegit.Path(clean), nil
}

// MatchFromBlob converts a reference into a doublestar glob pattern (see
// https://pkg.go.dev/github.com/bmatcuk/doublestar/v4), resolving relative
// references against the directory containing blob, and returns every path in
// candidates that the pattern matches, in the order they appear in candidates.
// A reference without glob metacharacters matches at most the one candidate with
// an identical path.
func MatchFromBlob(blob attegit.Path, value string, candidates []attegit.Path) ([]attegit.Path, error) {
	return MatchFromDir(attegit.Path(path.Dir(string(blob))), value, candidates)
}

// MatchFromDir is like MatchFromBlob, but resolves relative references against
// dir directly rather than against the directory containing a blob.
func MatchFromDir(dir attegit.Path, value string, candidates []attegit.Path) ([]attegit.Path, error) {
	pattern, err := resolve(dir, value)
	if err != nil {
		return nil, err
	}
	matches := make([]attegit.Path, 0, len(candidates))
	for _, candidate := range candidates {
		ok, err := doublestar.Match(pattern, string(candidate))
		if err != nil {
			return nil, fmt.Errorf("invalid repository reference %q: %w", value, err)
		}
		if ok {
			matches = append(matches, candidate)
		}
	}
	return matches, nil
}

func resolve(dir attegit.Path, value string) (string, error) {
	if value == "" || (strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//")) {
		return "", fmt.Errorf("invalid repository reference %q", value)
	}
	var resolved string
	if strings.HasPrefix(value, "//") {
		resolved = strings.TrimPrefix(value, "//")
	} else {
		resolved = path.Join(string(dir), value)
	}
	clean := path.Clean(resolved)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("repository reference %q escapes repository root", value)
	}
	return clean, nil
}
