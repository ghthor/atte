// Package attego detects Go modules, packages, and package-test dependencies.
//
// All Go files are parsed conservatively without evaluating build constraints.
// Files belong to the deepest enclosing go.mod. Imports that cannot be resolved
// to a package in the repository are scoped to the importing module.
package attego

import (
	"encoding/base64"
	"fmt"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/graph"
	"golang.org/x/mod/modfile"
)

const (
	Namespace                          = "attego"
	PackageKind                        = Namespace + ":package"
	PackageTestKind                    = Namespace + ":package-test"
	ImportsRelation graph.RelationKind = "imports"
)

type module struct {
	dir      attegit.Path
	name     string
	replaces []replace
}
type replace struct{ old, target string }
type goFile struct {
	dir, name string
	imports   []string
	test      bool
}
type packageInfo struct {
	module          *module
	dir, importPath string
	imports         map[string]struct{}
	testImports     map[string]struct{}
	hasTests        bool
}

// EntityID returns a stable ID for a package owned by moduleDir.
func EntityID(kind string, moduleDir attegit.Path, importPath string) graph.EntityID {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return graph.EntityID(fmt.Sprintf("%s:%s:%s:%s", Namespace, kind[len(Namespace)+1:], enc(string(moduleDir)), enc(importPath)))
}

// Graph analyzes Go modules and returns their package dependency graph.
func Graph(repo *attegit.Repo) (*graph.Graph, error) {
	if repo == nil {
		return nil, fmt.Errorf("nil repository")
	}
	mods, files, err := scan(repo)
	if err != nil {
		return nil, err
	}
	packages := make(map[string]*packageInfo)
	for _, f := range files {
		m := owner(mods, f.dir)
		if m == nil {
			continue
		}
		key := string(m.dir) + "\x00" + f.dir
		p := packages[key]
		if p == nil {
			rel := strings.TrimPrefix(f.dir, string(m.dir))
			rel = strings.TrimPrefix(rel, "/")
			imp := m.name
			if rel != "" && rel != "." {
				imp += "/" + rel
			}
			p = &packageInfo{module: m, dir: f.dir, importPath: imp, imports: map[string]struct{}{}, testImports: map[string]struct{}{}}
			packages[key] = p
		}
		if f.test {
			p.hasTests = true
			for _, imp := range f.imports {
				p.testImports[imp] = struct{}{}
			}
		} else {
			for _, imp := range f.imports {
				p.imports[imp] = struct{}{}
			}
		}
	}
	byModulePath := make(map[string][]*module)
	for _, m := range mods {
		byModulePath[m.name] = append(byModulePath[m.name], m)
	}
	entities := map[graph.EntityID]graph.Entity{}
	relations := map[graph.Relationship]struct{}{}
	for _, p := range packages {
		pid := EntityID(PackageKind, p.module.dir, p.importPath)
		entities[pid] = graph.Entity{ID: pid, Kind: PackageKind}
		tid := EntityID(PackageTestKind, p.module.dir, p.importPath)
		if p.hasTests {
			entities[tid] = graph.Entity{ID: tid, Kind: PackageTestKind}
		}
		for _, imp := range sortedSet(p.imports) {
			addImport(p, pid, imp, packages, mods, byModulePath, entities, relations)
		}
		if len(p.testImports) > 0 {
			for _, imp := range sortedSet(p.testImports) {
				addImport(p, tid, imp, packages, mods, byModulePath, entities, relations)
			}
		}
	}
	el := make([]graph.Entity, 0, len(entities))
	for _, e := range entities {
		el = append(el, e)
	}
	rl := make([]graph.Relationship, 0, len(relations))
	for r := range relations {
		rl = append(rl, r)
	}
	return graph.New(el, rl)
}

func scan(repo *attegit.Repo) ([]*module, []goFile, error) {
	mods := make([]*module, 0)
	files := make([]goFile, 0)
	for _, p := range repo.ObjKeys {
		if repo.Obj[p].Kind != attegit.Blob {
			continue
		}
		name := string(p)
		if path.Base(name) != "go.mod" && !strings.HasSuffix(name, ".go") {
			continue
		}
		contents, err := repo.Show(p)
		if err != nil {
			return nil, nil, fmt.Errorf("read %q: %w", p, err)
		}
		dir := path.Dir(name)
		if dir == "." {
			dir = ""
		}
		if path.Base(name) == "go.mod" {
			f, err := modfile.Parse(name, contents, nil)
			if err != nil {
				return nil, nil, fmt.Errorf("parse go.mod %q: %w", p, err)
			}
			m := &module{dir: attegit.Path(dir), name: f.Module.Mod.Path}
			for _, r := range f.Replace {
				if r.New.Version == "" {
					m.replaces = append(m.replaces, replace{old: r.Old.Path, target: path.Clean(path.Join(dir, r.New.Path))})
				}
			}
			mods = append(mods, m)
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, contents, parser.ImportsOnly)
		if err != nil {
			return nil, nil, fmt.Errorf("parse Go file %q: %w", p, err)
		}
		imports := make([]string, 0, len(f.Imports))
		for _, imp := range f.Imports {
			imports = append(imports, strings.Trim(imp.Path.Value, `"`))
		}
		files = append(files, goFile{dir: dir, name: name, imports: imports, test: strings.HasSuffix(name, "_test.go")})
	}
	sort.Slice(mods, func(i, j int) bool { return len(mods[i].dir) > len(mods[j].dir) })
	return mods, files, nil
}

func owner(mods []*module, dir string) *module {
	for _, m := range mods {
		if dir == string(m.dir) || strings.HasPrefix(dir, string(m.dir)+"/") || (m.dir == "" && dir != "") {
			return m
		}
	}
	return nil
}
func sortedSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
func addImport(p *packageInfo, from graph.EntityID, imp string, packages map[string]*packageInfo, mods []*module, byPath map[string][]*module, entities map[graph.EntityID]graph.Entity, relations map[graph.Relationship]struct{}) {
	var target graph.EntityID
	if q := resolveLocal(p, imp, packages); q != nil {
		target = EntityID(PackageKind, q.module.dir, q.importPath)
	} else if q := resolveModuleImport(imp, byPath, packages); q != nil {
		target = EntityID(PackageKind, q.module.dir, q.importPath)
	} else {
		target = EntityID(PackageKind, p.module.dir, imp)
		entities[target] = graph.Entity{ID: target, Kind: PackageKind}
	}
	relations[graph.Relationship{From: from, To: target, Kind: ImportsRelation}] = struct{}{}
	_ = mods
}
func resolveLocal(p *packageInfo, imp string, packages map[string]*packageInfo) *packageInfo {
	for _, r := range p.module.replaces {
		if imp == r.old || strings.HasPrefix(imp, r.old+"/") {
			suffix := strings.TrimPrefix(imp, r.old)
			dir := path.Join(r.target, suffix)
			if q := findPackageDir(dir, packages); q != nil {
				return q
			}
		}
	}
	if imp == p.module.name || strings.HasPrefix(imp, p.module.name+"/") {
		dir := strings.TrimPrefix(imp, p.module.name)
		dir = strings.TrimPrefix(dir, "/")
		return findPackage(p.module.dir, path.Join(string(p.module.dir), dir), packages)
	}
	return nil
}
func findPackage(modDir attegit.Path, dir string, packages map[string]*packageInfo) *packageInfo {
	if dir == "." {
		dir = ""
	}
	return packages[string(modDir)+"\x00"+dir]
}
func findPackageDir(dir string, packages map[string]*packageInfo) *packageInfo {
	for _, q := range packages {
		if string(q.dir) == dir {
			return q
		}
	}
	return nil
}
func resolveModuleImport(imp string, byPath map[string][]*module, packages map[string]*packageInfo) *packageInfo {
	var best *module
	bestLen := -1
	for name, ms := range byPath {
		if imp == name || strings.HasPrefix(imp, name+"/") {
			if len(name) > bestLen {
				bestLen = len(name)
				best = ms[0]
			}
		}
	}
	if best == nil {
		return nil
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(imp, best.name), "/")
	return findPackage(best.dir, path.Join(string(best.dir), rel), packages)
}
