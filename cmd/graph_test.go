package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestRepositoryContext(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	subdir := filepath.Join(git.Dir(), "nested", "working")
	must.NoError(t, os.MkdirAll(subdir, 0o755))

	root, relative, err := repositoryContext(git.Dir())
	must.NoError(t, err)
	test.EqOp(t, git.Dir(), root)
	test.EqOp(t, "", relative)

	root, relative, err = repositoryContext(subdir)
	must.NoError(t, err)
	test.EqOp(t, git.Dir(), root)
	test.EqOp(t, "nested/working", relative)
}

func TestRepositoryContextRejectsNonRepository(t *testing.T) {
	_, _, err := repositoryContext(t.TempDir())
	test.ErrorContains(t, err, "resolve repository root")
}

func TestPrintGraphGolden(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"README.md": "read me",
		"atte.hcl": `
			test "default" {
				script = path("./test.sh")
			}
		`,
		"test.sh": "#!/bin/sh\n",
		"pkg/pkg.go": `
			package pkg
		`,
		"pkg/pkg_test.go": `
			package pkg

			import "testing"

			func TestPackage(t *testing.T) {}
		`,
		"go.mod": `
			module example.com/root

			go 1.20
		`,
		"main.go": `
			package main

			import "example.com/root/cmd"

			var _ = cmd.Run
		`,
		"cmd/cmd.go": `
			package cmd

			import "example.com/root/internal/value"

			var Run = value.Value
		`,
		"internal/value/value.go": `
			package value

			const Value = 1
		`,
	})
	assertGraphGolden(t, "default", renderTestGraph(t, repo))
	assertGraphGolden(t, "run-targets", renderTestGraph(t, repo, PrintGraphOptions{IncludeRunTargets: true}))
}

func TestPrintGraphImportsGolden(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"go.mod": `
			module example.com/root

			go 1.20
		`,
		"main.go": `
			package main

			import (
				"fmt"
				"example.com/root/internal/value"
				"github.com/shoenig/test/must"
			)

			var _ = fmt.Println
			var _ = value.Value
			var _ = must.NoError
		`,
		"main_test.go": `
			package main_test

			import "testing"

			func TestMain(t *testing.T) {}
		`,
		"internal/value/value.go": `
			package value

			const Value = 1
		`,
	})
	assertGraphGolden(t, "imports", renderTestGraph(t, repo, PrintGraphOptions{IncludeExternalImports: true}))
}

func assertGraphGolden(t *testing.T, name, got string) {
	t.Helper()
	goldenPath := filepath.Join(graphTestdataDir(), name+".golden")
	must.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
	got = ansi.Strip(got)
	if os.Getenv("ATTE_CODEGEN") != "" {
		must.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(goldenPath)
	must.NoError(t, err)
	test.EqOp(t, string(want), got)
}

func graphTestdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "testdata", "graph")
}

func TestPrintGraphUsesGoPerspective(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"go.mod": `module example.com/root

go 1.20
`,
		"main.go": `package main

import "fmt"

func main() { fmt.Println("hello") }
`,
	})
	got := renderTestGraph(t, repo)
	test.StrContains(t, got, "go package")
	test.StrContains(t, got, "go.mod")
	test.StrContains(t, got, "main.go")
}

func TestPrintGraphFallsBackToGitPerspective(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"root.txt": "root",
	})
	got := renderTestGraph(t, repo)
	test.StrContains(t, got, "root.txt")
	test.StrNotContains(t, got, "attego:")
}

func TestPrintGraphReportsGoParseError(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"go.mod": `module example.com/root

go 1.20
`,
		"broken.go": `package main
import "unterminated
`,
	})
	var got bytes.Buffer
	err := printGraph(&got, repo, "")
	test.ErrorContains(t, err, "build Go graph")
}

func TestPrintGraphLocalImports(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"go.mod": `module example.com/root

go 1.20
`,
		"main.go": `package main

import "example.com/root/cmd"

var _ = cmd.Run
`,
		"cmd/cmd.go": `package cmd

import (
	"example.com/root/detector"
)

var _ = detector.Value
`,
		"detector/detector.go": `package detector

var Value = 1
`,
	})
	got := renderTestGraph(t, repo)
	test.StrContains(t, got, "import example.com/root/detector")
	test.StrNotContains(t, got, "attego:")
}

func TestPrintGraphExternalImports(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"go.mod": `module example.com/root

go 1.20
`,
		"main.go": `package main

import (
	"fmt"

	"github.com/shoenig/test/must"
)

func main() { fmt.Println(must.NoError) }
`,
	})
	without := renderTestGraph(t, repo)
	test.StrNotContains(t, without, "external import fmt")

	with := renderTestGraph(t, repo, PrintGraphOptions{IncludeExternalImports: true})
	test.StrContains(t, with, "go package example.com/root")
	test.StrContains(t, with, "std import fmt")
	test.StrNotContains(t, with, "external import fmt")
}

func TestIsGraphChildKind(t *testing.T) {
	childKind := func(kind string, want bool) {
		t.Helper()
		test.EqOp(t, want, isGraphChildKind(kind))
	}

	childKind(attego.PackageKind, true)
	childKind(attego.PackageTestKind, true)
	childKind(attehcl.TestKind, true)
	childKind(attehcl.CodegenKind, true)
	childKind(attehcl.LintKind, true)
	childKind(attegit.TreeKind, false)
	childKind(attegit.BlobKind, false)
	childKind("unknown", false)
}

func repoWithFiles(t *testing.T, files map[string]string) *attegit.Repo {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, files)
	git.CommitAll(t, "init")
	return openTestRepo(t, git.Dir())
}

func openTestRepo(t *testing.T, dir string) *attegit.Repo {
	t.Helper()
	repo, err := attegit.Open(dir, "HEAD")
	must.NoError(t, err)
	return repo
}

func renderTestGraph(t *testing.T, repo *attegit.Repo, options ...PrintGraphOptions) string {
	t.Helper()
	var output bytes.Buffer
	must.NoError(t, printGraph(&output, repo, "", options...))
	return output.String()
}
