package cmd

import (
	"bytes"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/shoenig/test/must"
)

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
	must.StrContains(t, got, "go package")
	must.StrContains(t, got, "go.mod")
	must.StrContains(t, got, "main.go")
}

func TestPrintGraphFallsBackToGitPerspective(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"root.txt": "root",
	})
	got := renderTestGraph(t, repo)
	must.StrContains(t, got, "root.txt")
	must.StrNotContains(t, got, "attego:")
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
	must.ErrorContains(t, err, "build Go graph")
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
	must.StrContains(t, got, "import example.com/root/detector")
	must.StrNotContains(t, got, "attego:")
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
	must.StrNotContains(t, without, "external import fmt")

	with := renderTestGraph(t, repo, PrintGraphOptions{IncludeExternalImports: true})
	must.StrContains(t, with, "go package example.com/root")
	must.StrContains(t, with, "std import fmt")
	must.StrNotContains(t, with, "external import fmt")
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
