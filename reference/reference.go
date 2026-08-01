// Package reference resolves repository-relative and Bazel-style file references.
package reference

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Tree is a non-root slash-separated repository-relative directory path.
type Tree string

// Root is the repository root tree.
const Root Tree = ""

// Blob is a slash-separated repository-relative file path.
type Blob string

// SomePath is a path reference whose syntax may be relative or root-relative.
// Path is the common repository-relative path interface implemented by Tree and Blob.
// Its Tree method returns the containing directory, allowing callers that do not
// need to distinguish object kind to share path traversal logic.
type Path interface {
	isPath()
	String() string
	Tree() Tree
}

type SomePath string

// SlashPath is a repository-root-relative path using the // prefix.
type SlashPath string

func parse(raw string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(raw, "\\", "/"))
	if clean == "." || clean == "" || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid repository-relative path %q", raw)
	}
	return clean, nil
}

// ParseTree validates raw as a repository-relative directory path.
func ParseTree(raw string) (Tree, error) {
	if raw == "" || raw == "." {
		return Root, nil
	}
	clean, err := parse(raw)
	return Tree(clean), err
}

// ParseBlob validates raw as a repository-relative file path.
func ParseBlob(raw string) (Blob, error) { clean, err := parse(raw); return Blob(clean), err }

// ParsePath validates raw as a repository-relative path without assigning an
// object kind. Callers that need the concrete kind should use ParseTree or
// ParseBlob instead.
func ParsePath(raw string) (Path, error) {
	clean, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return pathValue(clean), nil
}

type pathValue string

func (p pathValue) isPath()        {}
func (p pathValue) String() string { return string(p) }
func (p pathValue) Tree() Tree {
	parent := path.Dir(string(p))
	if parent == "." {
		return Root
	}
	return Tree(parent)
}

// String returns the repository-relative path represented by b.
func (b Blob) String() string { return string(b) }

func (b Blob) isPath() {}

// Tree returns the containing directory of b.
func (b Blob) Tree() Tree {
	parent := path.Dir(string(b))
	if parent == "." {
		return ""
	}
	return Tree(parent)
}

// String returns the repository-relative path represented by t.
func (t Tree) String() string { return string(t) }

func (t Tree) isPath() {}

// Tree returns t itself.
func (t Tree) Tree() Tree { return t }

// Parent returns the containing tree of t. The empty Tree is the repository
// root sentinel when t is a top-level directory.
func (t Tree) Parent() Tree {
	parent := path.Dir(string(t))
	if parent == "." {
		return ""
	}
	return Tree(parent)
}

// Blob joins relpath beneath t and validates the result.
func (t Tree) Blob(relpath string) (Blob, error) {
	return ParseBlob(path.Join(string(t), strings.ReplaceAll(relpath, "\\", "/")))
}

// ResolveFromBlob resolves a reference from a blob and returns a blob.
func ResolveFromBlob(blob Blob, value string) (Blob, error) {
	return ResolveBlobFromBlob(blob, SomePath(value))
}

// ResolveFromDir resolves a reference from a tree and returns a blob.
func ResolveFromDir(dir Tree, value string) (Blob, error) {
	return ResolveBlobFromTree(dir, SomePath(value))
}

// ResolveBlobFromBlob resolves a reference from a typed blob.
func ResolveBlobFromBlob(blob Blob, value SomePath) (Blob, error) {
	clean, err := resolve(string(blob.Tree()), string(value))
	if err != nil {
		return "", err
	}
	return Blob(clean), nil
}

// ResolveBlobFromTree resolves a reference from a typed tree.
func ResolveBlobFromTree(tree Tree, value SomePath) (Blob, error) {
	clean, err := resolve(string(tree), string(value))
	if err != nil {
		return "", err
	}
	return Blob(clean), nil
}

// Resolve resolves a general path reference relative to a typed tree.
func (p SomePath) Resolve(tree Tree) (Blob, error) { return ResolveBlobFromTree(tree, p) }

// Resolve resolves a //-prefixed path to a blob.
func (p SlashPath) Resolve() (Blob, error) {
	value := string(p)
	if !strings.HasPrefix(value, "//") {
		return "", fmt.Errorf("invalid slash path %q", value)
	}
	clean, err := parse(strings.TrimPrefix(value, "//"))
	return Blob(clean), err
}

// MatchFromBlob matches a reference against typed blob candidates.
func MatchFromBlobTyped(blob Blob, value string, candidates []Blob) ([]Blob, error) {
	return match(string(blob.Tree()), value, candidates)
}

// MatchFromDirTyped matches a reference against typed blob candidates.
func MatchFromDirTyped(dir Tree, value string, candidates []Blob) ([]Blob, error) {
	return match(string(dir), value, candidates)
}

// MatchFromBlob matches a reference against blob candidates in input order.
func MatchFromBlob(blob Blob, value string, candidates []Blob) ([]Blob, error) {
	return MatchFromBlobTyped(blob, value, candidates)
}

// MatchFromDir matches a reference against blob candidates in input order.
func MatchFromDir(dir Tree, value string, candidates []Blob) ([]Blob, error) {
	return MatchFromDirTyped(dir, value, candidates)
}

func match(dir, value string, candidates []Blob) ([]Blob, error) {
	pattern, err := resolve(dir, value)
	if err != nil {
		return nil, err
	}
	matches := make([]Blob, 0, len(candidates))
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

func resolve(dir, value string) (string, error) {
	if value == "" || (strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//")) {
		return "", fmt.Errorf("invalid repository reference %q", value)
	}
	value = strings.ReplaceAll(value, "\\", "/")
	resolved := path.Join(dir, value)
	if strings.HasPrefix(value, "//") {
		resolved = strings.TrimPrefix(value, "//")
	}
	clean, err := parse(resolved)
	if err != nil {
		return "", fmt.Errorf("repository reference %q escapes repository root", value)
	}
	return clean, nil
}
