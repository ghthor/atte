package cmd

import (
	"bytes"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/shoenig/test/must"
)

func TestPrintGraphUsesGoPerspective(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile("go.mod", []byte(`module example.com/root

go 1.20
`), 0o644)
	git.WriteFile("main.go", []byte(`package main

import "fmt"

func main() { fmt.Println("hello") }
`), 0o644)
	git.RunGitScript("git add . && git commit -qm init")

	repo, err := openTestRepo(git.Dir())
	must.NoError(t, err)
	var got bytes.Buffer
	must.NoError(t, printGraph(&got, repo, ""))
	must.StrContains(t, got.String(), "go package")
	must.StrContains(t, got.String(), "go.mod")
	must.StrContains(t, got.String(), "main.go")
}

func TestPrintGraphFallsBackToGitPerspective(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile("root.txt", []byte("root"), 0o644)
	git.RunGitScript("git add . && git commit -qm init")

	repo, err := openTestRepo(git.Dir())
	must.NoError(t, err)
	var got bytes.Buffer
	must.NoError(t, printGraph(&got, repo, ""))
	must.StrContains(t, got.String(), "root.txt")
	must.StrNotContains(t, got.String(), "attego:")
}

func TestPrintGraphReportsGoParseError(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile("go.mod", []byte(`module example.com/root

go 1.20
`), 0o644)
	git.WriteFile("broken.go", []byte(`package main
import "unterminated
`), 0o644)
	git.RunGitScript("git add . && git commit -qm init")

	repo, err := openTestRepo(git.Dir())
	must.NoError(t, err)
	var got bytes.Buffer
	err = printGraph(&got, repo, "")
	must.ErrorContains(t, err, "build Go graph")
}

func TestPrintGraphLocalImports(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile("go.mod", []byte(`module example.com/root

go 1.20
`), 0o644)
	git.WriteFile("main.go", []byte(`package main

import "example.com/root/cmd"

var _ = cmd.Run
`), 0o644)
	git.WriteFile("cmd/cmd.go", []byte(`package cmd

import (
	"example.com/root/detector"
)

var _ = detector.Value
`), 0o644)
	git.WriteFile("detector/detector.go", []byte(`package detector

var Value = 1
`), 0o644)
	git.RunGitScript("git add . && git commit -qm init")

	repo, err := openTestRepo(git.Dir())
	must.NoError(t, err)
	var got bytes.Buffer
	must.NoError(t, printGraph(&got, repo, ""))
	must.StrContains(t, got.String(), "import example.com/root/detector")
	must.StrNotContains(t, got.String(), "attego:")
}

func TestPrintGraphExternalImports(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile("go.mod", []byte(`module example.com/root

go 1.20
`), 0o644)
	git.WriteFile("main.go", []byte(`package main

import (
	"fmt"

	"github.com/shoenig/test/must"
)

func main() { fmt.Println(must.NoError) }
`), 0o644)
	git.RunGitScript("git add . && git commit -qm init")

	repo, err := openTestRepo(git.Dir())
	must.NoError(t, err)
	var without bytes.Buffer
	must.NoError(t, printGraph(&without, repo, ""))
	must.StrNotContains(t, without.String(), "external import fmt")

	var with bytes.Buffer
	must.NoError(t, printGraph(&with, repo, "", PrintGraphOptions{IncludeExternalImports: true}))
	must.StrContains(t, with.String(), "go package example.com/root")
	must.StrContains(t, with.String(), "std import fmt")
	must.StrNotContains(t, with.String(), "external import fmt")
}

func openTestRepo(dir string) (*attegit.Repo, error) {
	return attegit.Open(dir, "HEAD")
}
