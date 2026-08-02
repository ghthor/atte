// Package selector defines selectors for runnable repository targets.
package selector

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// hclFilename is the canonical filename for HCL target declarations. It must
// stay in sync with attehcl.Filename; the selector package cannot import
// attehcl without creating an import cycle (attehcl imports selector).
const hclFilename = "atte.hcl"

// Target describes the selector-facing identity of a runnable target.
// Selector is the formal repository selector syntax: <path>#<identifier>.
//
// Path is stored without the canonical // repository-root prefix. The path is
// repository-relative; Identifier is opaque and is interpreted by a detector.
type Selector struct {
	Path       string
	Identifier string
}

// Parse parses a formal selector. The optional // prefix means repository root.
// Identifier syntax is intentionally opaque to this package.
func Parse(raw string) (Selector, error) {
	value := strings.TrimPrefix(raw, "//")
	parts := strings.SplitN(value, "#", 2)
	if len(parts) != 2 || parts[1] == "" {
		return Selector{}, fmt.Errorf("invalid selector %q: expected <path>#<identifier>", raw)
	}
	clean, err := normalizePath(parts[0])
	if err != nil {
		return Selector{}, err
	}
	return Selector{Path: clean, Identifier: parts[1]}, nil
}

// Resolve resolves a selector path against a repository-relative directory.
// A // path is rooted at the repository; other paths are relative to relative.
func Resolve(raw, relative string) (Selector, error) {
	value := strings.TrimPrefix(raw, "//")
	parts := strings.SplitN(value, "#", 2)
	if len(parts) != 2 || parts[1] == "" {
		return Selector{}, fmt.Errorf("invalid selector %q: expected <path>#<identifier>", raw)
	}
	if strings.HasPrefix(raw, "//") {
		clean, err := normalizePath(parts[0])
		if err != nil {
			return Selector{}, err
		}
		return Selector{Path: clean, Identifier: parts[1]}, nil
	}
	resolved, err := ResolvePath(parts[0], relative)
	if err != nil {
		return Selector{}, err
	}
	return Selector{Path: resolved, Identifier: parts[1]}, nil
}

// ResolvePath resolves a repository-relative path and rejects root escapes.
func ResolvePath(value, relative string) (string, error) {
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return "", fmt.Errorf("invalid selector path %q", value)
	}
	value = strings.TrimPrefix(value, "//")
	resolved := path.Clean(path.Join(relative, strings.ReplaceAll(value, "\\", "/")))
	if resolved == "." {
		resolved = ""
	}
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("selector path %q escapes repository root", value)
	}
	return resolved, nil
}

// PathMatches reports whether inputPath addresses candidatePath. candidateDir
// may provide an equivalent containing-directory spelling for file targets.
func PathMatches(inputPath, relative, candidatePath, candidateDir string) bool {
	if inputPath == "" || inputPath == "." || inputPath == candidatePath || inputPath == candidateDir {
		return true
	}
	resolved, err := ResolvePath(inputPath, relative)
	if err != nil {
		return false
	}
	return resolved == candidatePath || resolved == candidateDir
}

// ContainingDir returns the directory containing an HCL target file, or
// candidatePath unchanged if it does not name an HCL file.
func ContainingDir(candidatePath string) string {
	if candidatePath == hclFilename {
		return ""
	}
	if !strings.HasSuffix(candidatePath, "/"+hclFilename) {
		return candidatePath
	}
	return strings.TrimSuffix(candidatePath, "/"+hclFilename)
}

// PathIsWithin reports whether candidate is dir itself or a descendant of dir,
// comparing complete slash-separated path segments rather than raw string
// prefixes so that sibling paths sharing a prefix (e.g. "foo" and "foobar")
// are never mistaken for an ancestor/descendant relationship.
func PathIsWithin(candidate, dir string) bool {
	if dir == "" {
		return true
	}
	if candidate == dir {
		return true
	}
	return strings.HasPrefix(candidate, dir+"/")
}

// RelativePath returns the path of candidate relative to dir, using
// segment-wise comparison, and reports whether candidate is dir itself or a
// descendant of dir. The returned path uses forward slashes and never begins
// with "../"; callers that need ancestor traversal should use ResolvePath.
func RelativePath(candidate, dir string) (string, bool) {
	if !PathIsWithin(candidate, dir) {
		return "", false
	}
	if candidate == dir {
		return "", true
	}
	if dir == "" {
		return candidate, true
	}
	return strings.TrimPrefix(candidate, dir+"/"), true
}

// PathHasSegmentPrefix reports whether candidate's path segments begin with
// prefix's segments, treating prefix's final segment as a possibly
// incomplete, partially typed segment matched via raw string prefix. All
// segments before the last must match exactly.
//
// This models shell-completion prefix matching, which is a distinct
// operation from path containment (see PathIsWithin): a completion prefix is
// frequently an incomplete final segment (e.g. "r" typed toward
// "reference"), not a complete ancestor path, so its last segment
// legitimately needs raw string-prefix semantics rather than a segment
// boundary match.
func PathHasSegmentPrefix(candidate, prefix string) bool {
	if prefix == "" {
		return true
	}
	if candidate == prefix || strings.HasPrefix(candidate, prefix+"/") {
		return true
	}
	candidateSegments := strings.Split(candidate, "/")
	prefixSegments := strings.Split(prefix, "/")
	if len(prefixSegments) > len(candidateSegments) {
		return false
	}
	for i := 0; i < len(prefixSegments)-1; i++ {
		if candidateSegments[i] != prefixSegments[i] {
			return false
		}
	}
	return strings.HasPrefix(candidateSegments[len(prefixSegments)-1], prefixSegments[len(prefixSegments)-1])
}

// IsImmediateChild reports whether candidate is exactly one path segment
// beneath dir, as opposed to a deeper descendant. Directory-style completion
// reveals one path segment at a time (mirroring shell file-path completion),
// so once a prefix names a complete directory boundary, only its direct
// children are viable next completions; deeper descendants require the user
// to type another segment first.
func IsImmediateChild(candidate, dir string) bool {
	rel, ok := RelativePath(candidate, dir)
	if !ok || rel == "" {
		return false
	}
	return !strings.Contains(rel, "/")
}

// String returns the canonical repository-root-qualified selector.
func (s Selector) String() string { return "//" + s.Path + "#" + s.Identifier }

func normalizePath(raw string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(raw, "\\", "/"))
	if clean == "." {
		clean = ""
	}
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid selector path %q", raw)
	}
	return clean, nil
}

type Target struct {
	// Path is the canonical repository path, including atte.hcl for HCL targets.
	Path  string
	Kind  string
	Name  string
	Index int
}

// String returns the canonical repository-root-qualified selector.
func (t Target) String() string {
	parts := make([]string, 0, 2)
	if t.Kind != "" {
		parts = append(parts, t.Kind)
	}
	if t.Name != "" {
		parts = append(parts, t.Name)
	}
	return Render(t.Path, strings.Join(parts, "."))
}

// GoTest returns a Go package-test selector target.
func Render(path, target string) string {
	return "//" + strings.Join([]string{path, target}, "#")
}

func GoTest(packagePath string) Target {
	return Target{Path: packagePath, Kind: "go_test"}
}

// HCL returns an HCL block selector target.
func HCL(filePath, kind, name string, index int) Target {
	return Target{Path: filePath, Kind: kind, Name: name, Index: index}
}

// Matches reports whether input selects t from relative, a repository-relative directory.
func (t Target) Matches(input, relative string) bool {
	canonical := strings.TrimPrefix(t.String(), "//")
	if matchesCanonical(input, canonical) {
		return true
	}
	kind := t.Kind
	blockName := kind + "." + t.Name
	if t.Kind == "go_test" {
		blockName = "go_test"
	}
	if input == kind || input == blockName || input == kind+"."+itoa(t.Index) {
		return true
	}
	input = strings.TrimPrefix(input, "//")
	parts := strings.SplitN(input, "#", 2)
	if len(parts) != 2 {
		return false
	}
	pathPart, block := parts[0], parts[1]
	canonicalPath := t.Path
	canonicalDir := ContainingDir(canonicalPath)
	if !PathMatches(pathPart, relative, canonicalPath, canonicalDir) {
		return false
	}
	return block == kind || block == blockName || block == kind+"."+itoa(t.Index)
}

func matchesCanonical(input, canonical string) bool {
	input = strings.TrimPrefix(input, "//")
	if input == canonical {
		return true
	}
	parts := strings.SplitN(canonical, "#", 2)
	if len(parts) != 2 {
		return false
	}
	pathPart, targetName := parts[0], parts[1]
	selectorParts := strings.SplitN(input, "#", 2)
	if len(selectorParts) == 1 {
		return input == targetName || input == shortName(targetName)
	}
	selectorPath, selectorName := selectorParts[0], selectorParts[1]
	return (selectorName == targetName || selectorName == shortName(targetName)) &&
		(selectorPath == pathPart || selectorPath == ContainingDir(pathPart))
}

func shortName(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
