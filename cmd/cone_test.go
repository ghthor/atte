package cmd

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// runCone runs atte cone in dir and returns what it printed.
func runCone(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	err := ExecuteWithOptions(t.Context(), append([]string{"cone"}, args...), ExecuteOptions{Out: &stdout, Err: &stderr})
	return stdout.String(), err
}

func TestConeCommandComparesHeadWithItsParent(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))

	got, err := runCone(t, f.git.Dir())

	must.NoError(t, err)
	assertGraphGolden(t, "subset-edit", got)
}

func TestConeCommandContentHashShowsTheRange(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))

	got, err := runCone(t, f.git.Dir(), "--content-hash")

	must.NoError(t, err)
	assertGraphGolden(t, "subset-edit-content-hash", got)
}

func TestConeCommandIgnoresUncommittedChanges(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))
	f.git.WriteFiles(t, map[string]string{"README.md": "edited but not committed"})

	got, err := runCone(t, f.git.Dir())

	must.NoError(t, err)
	assertGraphGolden(t, "subset-edit", got)
}

func TestConeCommandSelectsRevisions(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))
	f.git.WriteFiles(t, map[string]string{"README.md": "a third revision"})
	f.git.CommitAll(t, "readme")

	t.Run("defaults to the last commit", func(t *testing.T) {
		got, err := runCone(t, f.git.Dir())

		must.NoError(t, err)
		test.EqOp(t, ".\n└── README.md\n", ansi.Strip(got))
	})
	t.Run("head keeps the default base", func(t *testing.T) {
		// The base is HEAD^ whatever the head is, so this compares HEAD^ with
		// itself.
		got, err := runCone(t, f.git.Dir(), "--head", "HEAD^")

		must.NoError(t, err)
		test.EqOp(t, "", got)
	})
	t.Run("base keeps the default head", func(t *testing.T) {
		got, err := runCone(t, f.git.Dir(), "--base", "HEAD~2")

		must.NoError(t, err)
		test.StrContains(t, ansi.Strip(got), "value.go")
		test.StrContains(t, ansi.Strip(got), "README.md")
	})
	t.Run("base and head", func(t *testing.T) {
		got, err := runCone(t, f.git.Dir(), "--base", "HEAD~2", "--head", "HEAD^")

		must.NoError(t, err)
		assertGraphGolden(t, "subset-edit", got)
	})
	t.Run("the same revision is an empty Cone", func(t *testing.T) {
		got, err := runCone(t, f.git.Dir(), "--base", "HEAD", "--head", "HEAD")

		must.NoError(t, err)
		test.EqOp(t, "", got)
	})
}

func TestConeCommandStartsAtTheWorkingDirectory(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))

	t.Run("a directory with changes", func(t *testing.T) {
		got, err := runCone(t, filepath.Join(f.git.Dir(), "internal"))

		must.NoError(t, err)
		test.StrContains(t, got, "value.go")
		test.StrNotContains(t, got, "atte.hcl")
	})
	t.Run("a directory without", func(t *testing.T) {
		got, err := runCone(t, filepath.Join(f.git.Dir(), "pkg"))

		must.NoError(t, err)
		test.EqOp(t, "", got)
	})
}

func TestConeCommandErrors(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))
	single := attegittest.NewGitRepo(t)
	single.WriteFiles(t, subsetFiles("1"))
	single.CommitAll(t, "only")

	t.Run("unknown base", func(t *testing.T) {
		_, err := runCone(t, f.git.Dir(), "--base", "nonsense")

		test.ErrorContains(t, err, `resolve base revision "nonsense"`)
	})
	t.Run("unknown head", func(t *testing.T) {
		_, err := runCone(t, f.git.Dir(), "--head", "nonsense")

		test.ErrorContains(t, err, `resolve head revision "nonsense"`)
	})
	t.Run("a repository with one commit has no parent", func(t *testing.T) {
		_, err := runCone(t, single.Dir())

		test.ErrorContains(t, err, `resolve base revision "HEAD^"`)
	})
	t.Run("a repository with one commit works with an explicit base", func(t *testing.T) {
		got, err := runCone(t, single.Dir(), "--base", "HEAD", "--head", "HEAD")

		must.NoError(t, err)
		test.EqOp(t, "", got)
	})
	t.Run("positional arguments", func(t *testing.T) {
		_, err := runCone(t, f.git.Dir(), "HEAD^..HEAD")

		test.ErrorContains(t, err, "unknown command")
	})
}

func TestConeHelpListsTheInheritedLimitations(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))

	got, err := runCone(t, f.git.Dir(), "--help")

	must.NoError(t, err)
	for _, limitation := range []string{"the script a target runs", "//go:embed", "go.work", "over-approximates", "Uncommitted changes are\nnot read"} {
		test.StrContains(t, got, limitation)
	}
}
