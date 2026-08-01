package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/shoenig/test/must"
)

func TestPrintGraphUsesGoPerspective(t *testing.T) {
	dir := t.TempDir()
	attegittest.InitGitRepo(t, dir)
	must.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/root\n\ngo 1.20\n"), 0o644))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }\n"), 0o644))
	attegittest.RunGitScript(t, dir, "git add . && git commit -qm init")

	repo, err := openTestRepo(dir)
	must.NoError(t, err)
	var got bytes.Buffer
	must.NoError(t, printGraph(&got, repo, ""))
	must.True(t, strings.Contains(got.String(), "go package"))
	must.True(t, strings.Contains(got.String(), "go.mod"))
	must.True(t, strings.Contains(got.String(), "main.go"))
	must.True(t, strings.Contains(got.String(), "go package"))
}

func TestPrintGraphFallsBackToGitPerspective(t *testing.T) {
	dir := t.TempDir()
	attegittest.InitGitRepo(t, dir)
	must.NoError(t, os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0o644))
	attegittest.RunGitScript(t, dir, "git add . && git commit -qm init")

	repo, err := openTestRepo(dir)
	must.NoError(t, err)
	var got bytes.Buffer
	must.NoError(t, printGraph(&got, repo, ""))
	must.True(t, strings.Contains(got.String(), "root.txt"))
	must.False(t, strings.Contains(got.String(), "attego:"))
}

func TestPrintGraphReportsGoParseError(t *testing.T) {
	dir := t.TempDir()
	attegittest.InitGitRepo(t, dir)
	must.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/root\n\ngo 1.20\n"), 0o644))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package main\nimport \"unterminated\n"), 0o644))
	attegittest.RunGitScript(t, dir, "git add . && git commit -qm init")

	repo, err := openTestRepo(dir)
	must.NoError(t, err)
	var got bytes.Buffer
	err = printGraph(&got, repo, "")
	must.ErrorContains(t, err, "build Go graph")
}

func TestPrintGraphLocalImports(t *testing.T) {
	dir := t.TempDir()
	attegittest.InitGitRepo(t, dir)
	must.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/root\n\ngo 1.20\n"), 0o644))
	must.NoError(t, os.MkdirAll(filepath.Join(dir, "cmd"), 0o755))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport \"example.com/root/cmd\"\n\nvar _ = cmd.Run\n"), 0o644))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "cmd", "cmd.go"), []byte("package cmd\n\nimport (\n\t\"example.com/root/detector\"\n)\n\nvar _ = detector.Value\n"), 0o644))
	must.NoError(t, os.MkdirAll(filepath.Join(dir, "detector"), 0o755))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "detector", "detector.go"), []byte("package detector\n\nvar Value = 1\n"), 0o644))
	attegittest.RunGitScript(t, dir, "git add . && git commit -qm init")

	repo, err := openTestRepo(dir)
	must.NoError(t, err)
	var got bytes.Buffer
	must.NoError(t, printGraph(&got, repo, ""))
	must.True(t, strings.Contains(got.String(), "import example.com/root/detector"))
	must.False(t, strings.Contains(got.String(), "attego:"))
}

func TestPrintGraphExternalImports(t *testing.T) {
	dir := t.TempDir()
	attegittest.InitGitRepo(t, dir)
	must.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/root\n\ngo 1.20\n"), 0o644))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import "fmt"

func main() { fmt.Println("hello") }
`), 0o644))
	attegittest.RunGitScript(t, dir, "git add . && git commit -qm init")

	repo, err := openTestRepo(dir)
	must.NoError(t, err)
	var without bytes.Buffer
	must.NoError(t, printGraph(&without, repo, ""))
	must.False(t, strings.Contains(without.String(), "external import fmt"))

	var with bytes.Buffer
	must.NoError(t, printGraph(&with, repo, "", PrintGraphOptions{IncludeExternalImports: true}))
	must.True(t, strings.Contains(with.String(), "go package example.com/root"))
	must.True(t, strings.Contains(with.String(), "external import fmt"))
}

func openTestRepo(dir string) (*attegit.Repo, error) {
	return attegit.Open(dir, "HEAD")
}
