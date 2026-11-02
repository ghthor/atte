package attego

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
)

// DependencyRelations reports the relationships that mean "From depends on To":
// a package depends on what it imports, on its source files, on the Go version
// of its module, and a package test depends on the package it tests.
func (Detector) DependencyRelations() []graph.RelationKind {
	return []graph.RelationKind{ImportsRelation, SourceFileRelation, TestsRelation, GoVersionRelation}
}

// ContentTokens returns the content tokens of Go entities:
//
//   - go-module:<module>@<version> for an external package, using the
//     require directive of the go.mod that imported it, or the replacement
//     module version when a versioned replace directive applies;
//   - go-version:go=<go>[,toolchain=<toolchain>] for the Go version of a go.mod.
//
// Packages and standard library packages have no content of their own. An
// external package whose module cannot be determined from go.mod is an error.
func (Detector) ContentTokens(ctx context.Context, repo *attegit.Repo, g *graph.Graph) (map[graph.EntityID]string, error) {
	mods, err := scanModules(ctx, repo)
	if err != nil {
		return nil, err
	}
	byDir := make(map[reference.Tree]*module, len(mods))
	for _, m := range mods {
		byDir[m.dir] = m
	}
	tokens := make(map[graph.EntityID]string)
	for _, id := range g.EntityKeys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		kind := g.Entities[id].Kind
		if kind != GoVersionKind && kind != PackageExternalKind {
			continue
		}
		_, moduleDir, importPath, err := DecodeEntityID(id)
		if err != nil {
			return nil, err
		}
		m, ok := byDir[moduleDir]
		if !ok {
			return nil, fmt.Errorf("%q: no go.mod at %q", id, path.Join(string(moduleDir), "go.mod"))
		}
		if kind == GoVersionKind {
			tokens[id] = m.goVersionToken()
			continue
		}
		token, err := m.externalToken(importPath)
		if err != nil {
			return nil, err
		}
		tokens[id] = token
	}
	return tokens, nil
}

// scanModules parses every go.mod in repo.
func scanModules(ctx context.Context, repo *attegit.Repo) ([]*module, error) {
	mods := make([]*module, 0)
	for _, p := range repo.ObjKeys {
		if repo.Obj[p].Kind != attegit.Blob || path.Base(p.String()) != "go.mod" {
			continue
		}
		contents, err := repo.ShowContext(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", p, err)
		}
		dir := path.Dir(p.String())
		if dir == "." {
			dir = ""
		}
		m, err := parseModule(dir, p.String(), contents)
		if err != nil {
			return nil, err
		}
		mods = append(mods, m)
	}
	return mods, nil
}

// assumedGoVersion is the language version the go command assumes for a go.mod
// that has no go directive.
const assumedGoVersion = "1.16"

func (m *module) goVersionToken() string {
	version := m.goVersion
	if version == "" {
		version = assumedGoVersion
	}
	token := "go-version:go=" + version
	if m.toolchain != "" {
		token += ",toolchain=" + m.toolchain
	}
	return token
}

// externalToken returns the go-module token for an import path that
// resolved to no package in the repository.
func (m *module) externalToken(importPath string) (string, error) {
	goMod := path.Join(string(m.dir), "go.mod")
	for _, r := range m.replaces {
		if importPath == r.old || strings.HasPrefix(importPath, r.old+"/") {
			return "", fmt.Errorf("%s: import %q is replaced by %q, which is not a package in the repository", goMod, importPath, r.target)
		}
	}
	var owner *require
	for i := range m.requires {
		r := &m.requires[i]
		if importPath != r.path && !strings.HasPrefix(importPath, r.path+"/") {
			continue
		}
		if owner == nil || len(r.path) > len(owner.path) {
			owner = r
		}
	}
	if owner == nil {
		return "", fmt.Errorf("%s: import %q matches no require directive", goMod, importPath)
	}
	modulePath, version := owner.path, owner.version
	var unversioned *moduleReplace
	for i := range m.moduleReplaces {
		r := &m.moduleReplaces[i]
		switch {
		case r.oldPath != owner.path:
		case r.oldVersion == owner.version:
			return "go-module:" + r.newPath + "@" + r.newVersion, nil
		case r.oldVersion == "":
			unversioned = r
		}
	}
	if unversioned != nil {
		modulePath, version = unversioned.newPath, unversioned.newVersion
	}
	return "go-module:" + modulePath + "@" + version, nil
}
