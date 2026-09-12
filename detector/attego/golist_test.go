package attego

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/goccy/go-graphviz"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

type goListModule struct {
	Dir string
}

type goListError struct {
	Err string
}

type goListPackage struct {
	ImportPath   string
	ForTest      string
	Imports      []string
	TestImports  []string
	XTestImports []string
	Module       *goListModule
	Error        *goListError
}

type graphSnapshot struct {
	Entities  []graph.Entity
	Relations []graph.Relationship
}

func goListGraph(t *testing.T, repositoryDir string) *graph.Graph {
	t.Helper()
	modules := discoverGoListModules(t, repositoryDir)
	entities := make(map[graph.EntityID]graph.Entity)
	relations := make(map[graph.Relationship]struct{})
	for _, moduleDir := range modules {
		packages := runGoList(t, moduleDir)
		addGoListPackages(t, repositoryDir, packages, entities, relations)
	}
	entityList := make([]graph.Entity, 0, len(entities))
	for _, entity := range entities {
		entityList = append(entityList, entity)
	}
	relationList := make([]graph.Relationship, 0, len(relations))
	for relation := range relations {
		relationList = append(relationList, relation)
	}
	graph, err := graph.New(entityList, relationList)
	must.NoError(t, err)
	return graph
}

func discoverGoListModules(t *testing.T, repositoryDir string) []string {
	t.Helper()
	var modules []string
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = repositoryDir
	output, err := cmd.Output()
	must.NoError(t, err)
	for file := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if file == "" || filepath.Base(file) != "go.mod" {
			continue
		}
		modules = append(modules, filepath.Join(repositoryDir, filepath.Dir(file)))
	}
	sort.Strings(modules)
	return modules
}

func runGoList(t *testing.T, moduleDir string) []goListPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "-test", "./...")
	cmd.Dir = moduleDir
	output, err := cmd.Output()
	must.NoError(t, err, must.Sprintf("go list in %s", moduleDir))

	decoder := json.NewDecoder(strings.NewReader(string(output)))
	var packages []goListPackage
	for decoder.More() {
		var pkg goListPackage
		must.NoError(t, decoder.Decode(&pkg))
		if pkg.Error != nil {
			must.NoError(t, fmt.Errorf("%s: %s", pkg.ImportPath, pkg.Error.Err))
		}
		packages = append(packages, pkg)
	}
	return packages
}

func addGoListPackages(t *testing.T, repositoryDir string, listed []goListPackage, entities map[graph.EntityID]graph.Entity, relations map[graph.Relationship]struct{}) {
	t.Helper()
	packages := make(map[string]goListPackage)
	for _, pkg := range listed {
		if pkg.Module == nil || pkg.ForTest != "" || strings.HasSuffix(pkg.ImportPath, ".test") {
			continue
		}
		packages[pkg.ImportPath] = pkg
	}
	for _, pkg := range packages {
		moduleDir := relativeModuleDir(repositoryDir, pkg.Module.Dir)
		moduleTree := reference.Tree(moduleDir)
		if moduleDir != "" {
			var err error
			moduleTree, err = reference.ParseTree(moduleDir)
			must.NoError(t, err)
		}
		packageID := EntityID(PackageKind, moduleTree, pkg.ImportPath)
		entities[packageID] = graph.Entity{ID: packageID, Kind: PackageKind}
		if len(pkg.TestImports)+len(pkg.XTestImports) > 0 {
			testID := EntityID(PackageTestKind, moduleTree, pkg.ImportPath)
			entities[testID] = graph.Entity{ID: testID, Kind: PackageTestKind}
			imports := make([]string, 0, len(pkg.TestImports)+len(pkg.XTestImports))
			imports = append(imports, pkg.TestImports...)
			imports = append(imports, pkg.XTestImports...)
			for _, imp := range imports {
				addGoListImport(repositoryDir, pkg, testID, imp, packages, entities, relations)
			}
		}
		for _, imp := range pkg.Imports {
			addGoListImport(repositoryDir, pkg, packageID, imp, packages, entities, relations)
		}
	}
}

func addGoListImport(repositoryDir string, pkg goListPackage, from graph.EntityID, imported string, packages map[string]goListPackage, entities map[graph.EntityID]graph.Entity, relations map[graph.Relationship]struct{}) {
	moduleDir := relativeModuleDir(repositoryDir, pkg.Module.Dir)
	moduleTree := reference.Tree(moduleDir)
	if moduleDir != "" {
		var err error
		moduleTree, err = reference.ParseTree(moduleDir)
		must.NoError(nil, err)
	}
	kind := PackageExternalKind
	if isStdlibImport(imported) {
		kind = PackageStdlibKind
	}
	target := EntityID(kind, moduleTree, imported)
	if importedPkg, ok := packages[imported]; ok {
		moduleDir := relativeModuleDir(repositoryDir, importedPkg.Module.Dir)
		if moduleDir == "" || (moduleDir != ".." && !strings.HasPrefix(moduleDir, "../")) {
			importedTree := reference.Tree(moduleDir)
			if moduleDir != "" {
				var parseErr error
				importedTree, parseErr = reference.ParseTree(moduleDir)
				must.NoError(nil, parseErr)
			}
			target = EntityID(PackageKind, importedTree, imported)
			kind = PackageKind
		}
	}
	entities[target] = graph.Entity{ID: target, Kind: kind}
	relations[graph.Relationship{From: from, To: target, Kind: ImportsRelation}] = struct{}{}
}

func relativeModuleDir(repositoryDir, moduleDir string) string {
	rel, err := filepath.Rel(repositoryDir, moduleDir)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

func snapshotGraph(g *graph.Graph) graphSnapshot {
	snapshot := graphSnapshot{Entities: make([]graph.Entity, 0, len(g.EntityKeys))}
	for _, id := range g.EntityKeys {
		snapshot.Entities = append(snapshot.Entities, g.Entities[id])
		snapshot.Relations = append(snapshot.Relations, g.Out(id)...)
	}
	sort.Slice(snapshot.Relations, func(i, j int) bool {
		a, b := snapshot.Relations[i], snapshot.Relations[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	return snapshot
}

func assertGraphsEqual(t *testing.T, want, got *graph.Graph) {
	t.Helper()
	wantSnapshot, gotSnapshot := snapshotGraph(want), snapshotGraph(got)
	test.SliceEqOp(t, wantSnapshot.Entities, gotSnapshot.Entities)
	test.SliceEqOp(t, wantSnapshot.Relations, gotSnapshot.Relations)
}

func graphvizSnapshot(t *testing.T, g *graph.Graph) string {
	t.Helper()
	ctx := t.Context()
	viz, err := graphviz.New(ctx)
	must.NoError(t, err)
	defer func() { must.NoError(t, viz.Close()) }()
	dot, err := viz.Graph()
	must.NoError(t, err)
	defer func() { must.NoError(t, dot.Close()) }()

	nodes := make(map[graph.EntityID]*graphviz.Node, len(g.EntityKeys))
	for _, id := range g.EntityKeys {
		node, err := dot.CreateNodeByName(nodeName(id))
		must.NoError(t, err)
		node.SetLabel(graphvizLabel(id))
		nodes[id] = node
	}
	for _, id := range g.EntityKeys {
		for _, relation := range g.Out(id) {
			edge, err := dot.CreateEdgeByName(nodeName(relation.From)+"_"+nodeName(relation.To), nodes[relation.From], nodes[relation.To])
			must.NoError(t, err)
			edge.SetLabel(string(relation.Kind))
		}
	}
	var buf bytes.Buffer
	must.NoError(t, viz.Render(ctx, dot, "dot", &buf))
	return buf.String()
}

func nodeName(id graph.EntityID) string {
	return "n_" + base64.RawURLEncoding.EncodeToString([]byte(id))
}

func graphvizLabel(id graph.EntityID) string {
	parts := strings.Split(string(id), ":")
	if len(parts) != 4 || parts[0] != Namespace {
		return string(id)
	}
	decode := func(value string) string {
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			return value
		}
		return string(decoded)
	}
	return fmt.Sprintf("%s\\nmodule=%s\\nimport=%s", parts[1], parts[2], decode(parts[3]))
}

func TestGraphvizSnapshot(t *testing.T) {
	dir := newBasicFixture(t)
	want := graphvizSnapshot(t, goListGraph(t, dir))
	fixturePath := filepath.Join("testdata", "go-list.dot")
	boxartPath := filepath.Join("testdata", "go-list.txt")
	if os.Getenv("ATTE_CODEGEN") != "" {
		must.NoError(t, os.MkdirAll(filepath.Dir(fixturePath), 0o755))
		must.NoError(t, os.WriteFile(fixturePath, []byte(want), 0o644))
		must.NoError(t, os.WriteFile(boxartPath, []byte(graphEasyBoxart(t, want)), 0o644))
	}
	fixture, err := os.ReadFile(fixturePath)
	must.NoError(t, err)
	test.EqOp(t, string(fixture), want)
	boxartFixture, err := os.ReadFile(boxartPath)
	must.NoError(t, err)
	test.EqOp(t, string(boxartFixture), graphEasyBoxart(t, want))
}

// defaultLabelLine matches the Graphviz-emitted "node [label=\"\\N\"];" and
// "edge [label=\"\\E\"];" defaults, which graph-easy renders literally
// instead of respecting per-element label overrides.
var defaultLabelLine = regexp.MustCompile(`(?m)^\t(?:node|edge) \[label="\\[NE]"\];\n`)

// graphAttrsLine matches the Graphviz-emitted graph-level layout attributes
// (bounding box, etc.), which graph-easy does not understand.
var graphAttrsLine = regexp.MustCompile(`(?m)^\tgraph \[.*\];\n`)

// attrBlock matches a Graphviz "[ ... ]" attribute list so it can be reduced
// to just its label attribute.
var attrBlock = regexp.MustCompile(`(?s)\[(.*?)\]`)

// labelAttr extracts a label attribute's value, quoted or bare.
var labelAttr = regexp.MustCompile(`label=(?:"((?:[^"\\]|\\.)*)"|(\w+))`)

// cleanForGraphEasy strips Graphviz layout-only attributes (pos, lp, bb,
// height, width, key, and the default \N/\E label templates) that graph-easy
// does not understand, keeping only node and edge labels.
func cleanForGraphEasy(dot string) string {
	dot = defaultLabelLine.ReplaceAllString(dot, "")
	dot = graphAttrsLine.ReplaceAllString(dot, "")
	return attrBlock.ReplaceAllStringFunc(dot, func(block string) string {
		m := labelAttr.FindStringSubmatch(block[1 : len(block)-1])
		if m == nil {
			return ""
		}
		label := m[1]
		if label == "" {
			label = m[2]
		}
		return `[label="` + label + `"]`
	})
}

func graphEasyBoxart(t *testing.T, dot string) string {
	t.Helper()
	cmd := exec.Command("graph-easy", "--as=boxart")
	cmd.Stdin = strings.NewReader(cleanForGraphEasy(dot))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	must.NoError(t, cmd.Run(), must.Sprintf("graph-easy: %s", stderr.String()))
	return stdout.String()
}
