package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/ghthor/atte/cone"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/graph"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// subsetFiles is the repository of TestPrintGraphGolden without the packages
// that play no part in these tests.
func subsetFiles(value string) map[string]string {
	return map[string]string{
		"README.md": "read me",
		"atte.hcl": `
			test "default" {
				script = path("./test.sh")
			}

			codegen "go" {
				script = path("./test.sh")
				depends_on = [gopkg("./cmd")]
			}
		`,
		"test.sh":    "#!/bin/sh\n",
		"pkg/pkg.go": "package pkg\n",
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
		"internal/value/value.go": "package value\n\nconst Value = " + value + "\n",
	}
}

// changeFixture is a repository with two commits, the second of which writes
// the files of an edit over the first.
type changeFixture struct {
	git  *attegittest.GitRepo
	base *attegit.Repo
	head *attegit.Repo
}

func newChangeFixture(t *testing.T, before, after map[string]string) changeFixture {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, before)
	git.CommitAll(t, "base")
	git.WriteFiles(t, after)
	// The second commit may change nothing.
	git.RunGitScript(t, "git add -A && git commit -q --allow-empty -m head")
	base, err := attegit.Open(git.Dir(), "HEAD^")
	must.NoError(t, err)
	head, err := attegit.Open(git.Dir(), "HEAD")
	must.NoError(t, err)
	return changeFixture{git: git, base: base, head: head}
}

// render prints the Cone of the change from relativePath, labelling hashes as
// base..head.
func (f changeFixture) render(t *testing.T, relativePath string, options PrintGraphOptions) string {
	t.Helper()
	scanner, err := scannerForContext(t.Context())
	must.NoError(t, err)
	base, err := cone.Capture(t.Context(), scanner, f.base, cone.WithContentHashes())
	must.NoError(t, err)
	head, err := cone.Capture(t.Context(), scanner, f.head, cone.WithContentHashes())
	must.NoError(t, err)
	delta, err := cone.Diff(base, head)
	must.NoError(t, err)

	render := renderOptions{
		PrintGraphOptions: options,
		hashes:            head.Hashes,
		complete:          head.Graph,
		hashLabel: func(id graph.EntityID) (string, error) {
			headSum, err := head.Hashes.Sum(id)
			if err != nil {
				return "", err
			}
			if !base.Graph.Has(id) {
				return "new", nil
			}
			baseSum, err := base.Hashes.Sum(id)
			if err != nil {
				return "", err
			}
			return baseSum.String()[:contentHashLength] + ".." + headSum.String()[:contentHashLength], nil
		},
	}
	var output bytes.Buffer
	must.NoError(t, printGitGraph(&output, f.head, delta.Graph, relativePath, render, scanner))
	return output.String()
}

func TestPrintGraphSubsetGolden(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))

	assertGraphGolden(t, "subset-edit", f.render(t, "", PrintGraphOptions{}))
	assertGraphGolden(t, "subset-edit-content-hash", f.render(t, "", PrintGraphOptions{IncludeContentHash: true}))
}

func TestPrintGraphSubsetKeepsImportsOfPackagesWithoutChangedFiles(t *testing.T) {
	// Only the leaf file changes. The cmd and main packages are in the Cone
	// through their imports, so neither has a changed source file in the
	// subset, and each must still list the import below it.
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))

	got := f.render(t, "", PrintGraphOptions{})

	test.StrContains(t, got, "import example.com/root/cmd")
	test.StrContains(t, got, "import example.com/root/internal/value")
}

func TestPrintGraphSubsetShowsNewEntitiesAsNew(t *testing.T) {
	after := subsetFiles("1")
	after["internal/extra/extra.go"] = "package extra\n"
	f := newChangeFixture(t, subsetFiles("1"), after)

	got := ansi.Strip(f.render(t, "", PrintGraphOptions{IncludeContentHash: true}))

	for line := range strings.Lines(got) {
		if strings.Contains(line, "extra.go") {
			test.StrContains(t, line, " new")
		}
	}
	test.StrContains(t, got, "extra.go")
	test.StrContains(t, got, "..")
}

func TestPrintGraphSubsetPrintsNothingBelowAnUnchangedTree(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))

	test.EqOp(t, "", f.render(t, "pkg", PrintGraphOptions{}))
	test.NotEqOp(t, "", f.render(t, "internal", PrintGraphOptions{}))
}

func TestPrintGraphSubsetPrintsNothingWhenNothingChanged(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("1"))

	test.EqOp(t, "", f.render(t, "", PrintGraphOptions{}))
}

func TestPrintGraphOfACompleteGraphStillRejectsAMissingTree(t *testing.T) {
	repo := repoWithFiles(t, subsetFiles("1"))

	err := printGraph(t.Context(), &bytes.Buffer{}, repo, "nowhere")

	test.ErrorContains(t, err, "not found")
}
