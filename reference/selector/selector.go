// Package selector defines selectors for runnable repository targets.
package selector

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Target describes the selector-facing identity of a runnable target.
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
	canonicalPath := canonical
	if i := strings.IndexByte(canonicalPath, '#'); i >= 0 {
		canonicalPath = canonicalPath[:i]
	}
	canonicalDir := strings.TrimSuffix(canonicalPath, "/atte.hcl")
	canonicalDir = strings.TrimSuffix(canonicalDir, "/")
	if pathPart != "" && pathPart != "." && pathPart != canonicalPath && pathPart != canonicalDir {
		if relative == "" {
			return false
		}
		resolved := filepath.ToSlash(filepath.Clean(filepath.Join(relative, filepath.FromSlash(pathPart))))
		if resolved == "." {
			resolved = ""
		}
		if strings.HasSuffix(pathPart, "/atte.hcl") || pathPart == "atte.hcl" {
			if resolved != canonicalPath {
				return false
			}
		} else if resolved != canonicalPath && resolved != canonicalDir {
			return false
		}
		if strings.HasPrefix(resolved, "../") || resolved == ".." {
			return false
		}
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
		(selectorPath == pathPart || selectorPath == strings.TrimSuffix(pathPart, "/atte.hcl"))
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
