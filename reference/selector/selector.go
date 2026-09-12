// Package selector defines selectors for runnable repository targets.
package selector

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/ghthor/atte/reference"
)

// HclFilename is the canonical filename for HCL target declarations. It must
// stay in sync with attehcl.Filename; the selector package cannot import
// attehcl without creating an import cycle (attehcl imports selector).
const HclFilename = "atte.hcl"

const Seperator = "#"

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
	pathPart, identifier, err := split(raw)
	if err != nil {
		return Selector{}, err
	}
	clean, err := normalizePath(pathPart)
	if err != nil {
		return Selector{}, err
	}
	return Selector{Path: clean, Identifier: identifier}, nil
}

func split(raw string) (string, string, error) {
	value := strings.TrimPrefix(raw, "//")
	parts := strings.SplitN(value, Seperator, 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", fmt.Errorf("invalid selector %q: expected <path>#<identifier>", raw)
	}
	return parts[0], parts[1], nil
}

func (s Selector) Tree() reference.Tree {
	if s.HCL() {
		t := reference.Tree(filepath.Dir(s.Path))
		if t == "." {
			return reference.Root
		}
		return t
	}
	return reference.Tree(s.Path)
}

func (s Selector) HCL() bool {
	return filepath.Base(s.Path) == HclFilename
}

// Resolve resolves a selector path against a repository-relative directory.
// A // path is rooted at the repository; other paths are relative to relative.
func Resolve(raw, relative string) (Selector, error) {
	pathPart, identifier, err := split(raw)
	if err != nil {
		return Selector{}, err
	}
	if strings.HasPrefix(raw, "//") {
		clean, err := normalizePath(pathPart)
		if err != nil {
			return Selector{}, err
		}
		return Selector{Path: clean, Identifier: identifier}, nil
	}
	resolved, err := ResolvePath(pathPart, relative)
	if err != nil {
		return Selector{}, err
	}
	return Selector{Path: resolved, Identifier: identifier}, nil
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
	if candidatePath == HclFilename {
		return ""
	}
	if !strings.HasSuffix(candidatePath, "/"+HclFilename) {
		return candidatePath
	}
	return strings.TrimSuffix(candidatePath, "/"+HclFilename)
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

// String returns the canonical repository-root-qualified selector.
func (s Selector) String() string { return "//" + s.Path + "#" + s.Identifier }

// StringShort removed the /atte.hcl filename portion from a Canonical selector path
func (s Selector) StringShort() string {
	s.Path = string(s.Tree())
	return s.String()
}

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

// Target is the user-facing identity used to construct short CLI selectors.
//
// Unlike graphtarget.ID, it intentionally omits graph identity and namespace
// bookkeeping. Its fields describe the canonical path and the identifier users
// can type, while Aliases lists additional accepted spellings. Use String or
// StringShort to render the target for the CLI, and Matches to compare input.
type Target struct {
	// Path is the canonical repository path, including atte.hcl for HCL targets.
	// It is the path component of a user-facing selector, not a graph entity ID.
	Path  string
	Kind  string
	Name  string
	Index int
	// Aliases are additional identifiers accepted when matching this target.
	Aliases []string
}

// Aliases returns the identifiers accepted when matching target.
func Aliases(target Target) []string {
	return target.identifierAliases()
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
	aliases := t.identifierAliases()
	input = strings.TrimPrefix(input, "//")
	parts := strings.SplitN(input, Seperator, 2)
	if len(parts) == 1 {
		return containsAlias(aliases, input)
	}
	if !containsAlias(aliases, parts[1]) {
		return false
	}
	return PathMatches(parts[0], relative, t.Path, ContainingDir(t.Path))
}

func (t Target) identifierAliases() []string {
	canonical := strings.Join([]string{t.Kind, t.Name}, ".")
	if t.Name == "" {
		canonical = t.Kind
	}
	blockName := canonical
	if t.Kind == "go_test" {
		blockName = "go_test"
	}
	aliases := make([]string, 0, 5+len(t.Aliases))
	for _, alias := range append([]string{canonical, shortName(canonical), t.Kind, blockName, t.Kind + "." + itoa(t.Index)}, t.Aliases...) {
		if !containsAlias(aliases, alias) {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

func containsAlias(aliases []string, value string) bool {
	return slices.Contains(aliases, value)
}

func shortName(name string) string {
	if _, short, ok := strings.Cut(name, "."); ok {
		return short
	}
	return name
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
