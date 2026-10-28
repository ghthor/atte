package attego

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"

	detectorpkg "github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graph/graphtest"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// newBasicFixture creates a temporary Git repository with a root module
// containing package p, whose tests exercise both an internal ("p") and an
// external ("p_test") import.
func testPath(raw string) reference.Path {
	p, err := reference.ParsePath(raw)
	if err != nil {
		panic(err)
	}
	return p
}

func newBasicFixture(t *testing.T) string {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, map[string]string{
		"go.mod": `
module example.com/root

go 1.24

require github.com/shoenig/test v1.13.2

require github.com/google/go-cmp v0.7.0 // indirect
`,
		"go.sum": `
github.com/google/go-cmp v0.7.0 h1:wk8382ETsv4JYUZwIsn6YpYiWiBsYLSJiTsyBybVuN8=
github.com/google/go-cmp v0.7.0/go.mod h1:pXiqmnSA92OHEEa9HXL2W4E7lf9JzCmGVUdgjX3N/iU=
github.com/shoenig/test v1.13.2 h1:SaGxHxg7xkRuKuNtuFmHf0LgNGaAgcBT7HN4WHCKfqU=
github.com/shoenig/test v1.13.2/go.mod h1:MKmiRyEeuFl8y9PCoThaRDgYQZeWBhRQlH99poXz5LI=
`,
		"p/p.go": `
package p
import (
	"fmt"

	"github.com/shoenig/test/must"
)
var _ = fmt.Println
var _ = must.NoError
`,
		"p/p_test.go": `
package p_test
import (
	"testing"

	"github.com/shoenig/test/must"

	"example.com/root/p"
)
var _ = p.X
var _ *testing.T
var _ = must.NoError
`,
	})
	git.CommitAll(t, "init")
	return git.Dir()
}

func TestAttachScannerRetainsCompiledScanner(t *testing.T) {
	builder := detectorpkg.NewBuilder()
	scanner, err := builder.Compile()
	must.NoError(t, err)

	sensor := NewDetector()
	bound, err := sensor.AttachScanner(scanner)
	must.NoError(t, err)

	configured := bound.(*Detector)
	test.True(t, configured != sensor, test.Sprintf("AttachScanner should return a new Sensor instance"))
	test.True(t, configured.scanner == scanner, test.Sprintf("configured Sensor should retain the compiled Scanner"))
	test.Nil(t, sensor.scanner)
}

// goTestFixture is a repository with a root module containing packages a and b,
// plus a nested module containing package n. Each package has tests.
type goTestFixture struct {
	git  *attegittest.GitRepo
	repo *attegit.Repo
}

func newGoTestFixture(t *testing.T, hclFiles map[string]string) goTestFixture {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	files := map[string]string{
		"go.mod":             "module example.com/root\n\ngo 1.24\n",
		"a/a.go":             "package a\n",
		"a/a_test.go":        "package a\n",
		"b/b.go":             "package b\n",
		"b/b_test.go":        "package b\n",
		"nested/go.mod":      "module example.com/nested\n\ngo 1.24\n",
		"nested/n/n.go":      "package n\n",
		"nested/n/n_test.go": "package n\n",
	}
	maps.Copy(files, hclFiles)
	git.WriteFiles(t, files)
	git.CommitAll(t, "init")
	repo, err := attegit.Open(git.Dir(), "HEAD")
	must.NoError(t, err)
	return goTestFixture{git: git, repo: repo}
}

func (f goTestFixture) scanner(t *testing.T) detectorpkg.Scanner {
	t.Helper()
	builder := detectorpkg.NewBuilder()
	must.NoError(t, builder.AttachSensor(attehcl.NewDetector()))
	must.NoError(t, builder.AttachSensor(NewDetector()))
	scanner, err := builder.Compile()
	must.NoError(t, err)
	return scanner
}

func (f goTestFixture) target(t *testing.T, scanner detectorpkg.Scanner, packageDir string) graphtarget.ID {
	t.Helper()
	targets, err := scanner.Targets(t.Context(), f.repo)
	must.NoError(t, err)
	for _, target := range targets {
		if target.Namespace == Namespace && target.Path == packageDir {
			return target
		}
	}
	t.Fatalf("no go_test target for %q", packageDir)
	return graphtarget.ID{}
}

func TestExecuteTargetHonorsGoTestSetting(t *testing.T) {
	fixture := newGoTestFixture(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
locals {
  race = "-race"
}

go_test {
  command = ["go", "test", local.race, "./..."]
}
`, "\n"),
		"b/atte.hcl": strings.TrimLeft(`
go_test {
  command = ["go", "test", "-count=1"]
}
`, "\n"),
	})
	scanner := fixture.scanner(t)
	execute := func(packageDir string, want []string) {
		t.Helper()
		target := fixture.target(t, scanner, packageDir)
		execution, err := scanner.ExecuteTarget(t.Context(), fixture.repo, fixture.git.Dir(), target)
		must.NoError(t, err)
		test.SliceEqOp(t, want, execution.Args)
		test.EqOp(t, filepath.Join(fixture.git.Dir(), filepath.FromSlash(packageDir)), execution.Dir)
	}
	t.Run("module override", func(t *testing.T) {
		execute("a", []string{"go", "test", "-race", "./..."})
	})
	t.Run("package override", func(t *testing.T) {
		execute("b", []string{"go", "test", "-count=1"})
	})
	t.Run("other module default", func(t *testing.T) {
		execute("nested/n", DefaultGoTestCommand)
	})
}

func TestExecuteTargetWithoutScannerUsesDefault(t *testing.T) {
	execution, err := NewDetector().ExecuteTarget(t.Context(), nil, "/root", graphtarget.ID{Path: "a"})
	must.NoError(t, err)
	test.SliceEqOp(t, DefaultGoTestCommand, execution.Args)
}

func TestGoTestSettingRejectsInvalidCommands(t *testing.T) {
	evaluate := func(t *testing.T, body, want string) {
		t.Helper()
		fixture := newGoTestFixture(t, map[string]string{"a/atte.hcl": body})
		_, err := fixture.scanner(t).HCLSettings(t.Context(), fixture.repo, reference.Tree("a"))
		test.ErrorContains(t, err, want)
	}
	t.Run("missing", func(t *testing.T) {
		evaluate(t, `go_test {}`, "command")
	})
	t.Run("empty", func(t *testing.T) {
		evaluate(t, `go_test { command = [] }`, "command must not be empty")
	})
	t.Run("not a list", func(t *testing.T) {
		evaluate(t, `go_test { command = "go test" }`, "command must be a list of strings")
	})
	t.Run("non-string element", func(t *testing.T) {
		evaluate(t, `go_test { command = ["go", 1] }`, "command elements must be strings")
	})
	t.Run("labels", func(t *testing.T) {
		evaluate(t, `go_test "a" { command = ["go"] }`, "setting blocks take no labels")
	})
}

func TestGoTestSettingShowsInConfigOutput(t *testing.T) {
	fixture := newGoTestFixture(t, map[string]string{
		"a/atte.hcl": `go_test { command = ["go", "test", "-short"] }`,
	})
	scanner := fixture.scanner(t)
	values, err := scanner.HCLSettings(t.Context(), fixture.repo, reference.Tree("a"))
	must.NoError(t, err)
	must.Len(t, 1, values)
	test.EqOp(t, GoTestSettingBlock, values[0].Name)
	projected, err := values[0].Configuration()
	must.NoError(t, err)
	test.Eq(t, map[string]any{"command": []string{"go", "test", "-short"}}, projected)
}

func TestHCLFunctionsResolvePackages(t *testing.T) {
	repoPath := newBasicFixture(t)
	repo, err := attegit.Open(repoPath, "HEAD")
	must.NoError(t, err)
	functions, err := HCLFunctions(t.Context(), repo, reference.Blob("atte.hcl"))
	must.NoError(t, err)

	resolve := func(t *testing.T, fn function.Function, value string, want graph.EntityID) {
		t.Helper()
		got, err := fn.Call([]cty.Value{cty.StringVal(value)})
		test.NoError(t, err)
		test.EqOp(t, string(want), got.AsString())
	}
	reject := func(t *testing.T, fn function.Function, value, message string) {
		t.Helper()
		_, err := fn.Call([]cty.Value{cty.StringVal(value)})
		test.ErrorContains(t, err, message)
	}

	resolve(t, functions["gopkg"], "example.com/root/p", EntityID(PackageKind, reference.Root, "example.com/root/p"))
	resolve(t, functions["gopkg_test"], "p", EntityID(PackageTestKind, reference.Root, "example.com/root/p"))
	reject(t, functions["gopkg_test"], "p/p.go", "names a Go file")
}

func TestEntityIDRoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		moduleDir  reference.Tree
		importPath string
	}{
		{name: "root module", moduleDir: "", importPath: "example.com/root/p"},
		{name: "nested module", moduleDir: "sub", importPath: "example.com/sub/p"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := EntityID(PackageKind, c.moduleDir, c.importPath)
			kind, moduleDir, importPath, err := DecodeEntityID(id)
			must.NoError(t, err)
			test.EqOp(t, PackageKind, kind)
			test.EqOp(t, c.moduleDir, moduleDir)
			test.EqOp(t, c.importPath, importPath)
		})
	}
}

func TestEntityIDContainsGoModPath(t *testing.T) {
	test.EqOp(t, graph.EntityID("attego:package:go.mod:ZXhhbXBsZS5jb20vcm9vdC9w"), EntityID(PackageKind, "", "example.com/root/p"))
	test.EqOp(t, graph.EntityID("attego:package:sub/go.mod:ZXhhbXBsZS5jb20vcm9vdC9w"), EntityID(PackageKind, "sub", "example.com/root/p"))
}

func TestDecodeEntityIDRejectsMalformed(t *testing.T) {
	_, _, _, err := DecodeEntityID(graph.EntityID("attego:package:not-a-gomod-path:ZXhhbXBsZS5jb20vcm9vdC9w"))
	test.Error(t, err)
}

func openTestRepo(t *testing.T, dir string) *attegit.Repo {
	t.Helper()
	repo, err := attegit.Open(dir, "HEAD")
	must.NoError(t, err)
	return repo
}

func openTestGraph(t *testing.T, dir string) *graph.Graph {
	t.Helper()
	got, err := Graph(t.Context(), openTestRepo(t, dir))
	must.NoError(t, err)
	return got
}

func openTestGraphWithContainment(t *testing.T, dir string) *graph.Graph {
	t.Helper()
	got, err := Graph(t.Context(), openTestRepo(t, dir), graphset.WithAttachToTree())
	must.NoError(t, err)
	return got
}

func TestGraphMatchesGoList(t *testing.T) {
	dir := newBasicFixture(t)
	assertGraphsEqual(t, goListGraph(t, dir), openTestGraph(t, dir))
}

func TestGraphPackagesAndTests(t *testing.T) {
	repo := newBasicFixture(t)
	g := openTestGraphWithContainment(t, repo)
	var normal, testPackage graph.EntityID
	for _, id := range g.EntityKeys {
		if g.Entities[id].Kind == PackageKind && string(id) != "" {
			normal = id
		}
		if g.Entities[id].Kind == PackageTestKind {
			testPackage = id
		}
	}
	test.NotEq(t, graph.EntityID(""), normal, test.Sprintf("missing normal node: %#v", g.EntityKeys))
	test.NotEq(t, graph.EntityID(""), testPackage, test.Sprintf("missing test node: %#v", g.EntityKeys))
	packageID := EntityID(PackageKind, "", "example.com/root/p")
	normalFile := attegit.EntityID(testPath("p/p.go"))
	testFile := attegit.EntityID(testPath("p/p_test.go"))
	graphtest.MustHaveRelation(t, g, testPackage, packageID, attegit.ContainsRelation)
	graphtest.MustHaveRelation(t, g, normal, normalFile, SourceFileRelation)
	graphtest.MustNotHaveRelation(t, g, normal, testFile, SourceFileRelation)
	graphtest.MustHaveRelation(t, g, testPackage, testFile, SourceFileRelation)
	graphtest.MustNotHaveRelation(t, g, testPackage, normalFile, SourceFileRelation)
	treeID := attegit.EntityID(testPath("p"))
	graphtest.MustHaveRelation(t, g, treeID, normal, attegit.ContainsRelation)
	graphtest.MustHaveRelation(t, g, treeID, testPackage, attegit.ContainsRelation)
	graphtest.MustNotHaveRelation(t, g, normal, treeID, attegit.ContainsRelation)
}
