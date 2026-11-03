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
	stdout, _, err := runConeStderr(t, dir, args...)
	return stdout, err
}

// runConeStderr is runCone that also returns what was printed to stderr.
func runConeStderr(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	err := ExecuteWithOptions(t.Context(), append([]string{"cone"}, args...), ExecuteOptions{Out: &stdout, Err: &stderr})
	return stdout.String(), stderr.String(), err
}

// coneAfter runs script in a repository whose second commit edits a file, then
// runs atte cone in it. It returns the stripped output and the comparison
// announced on stderr.
func coneAfter(t *testing.T, script string, args ...string) (string, string) {
	t.Helper()
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))
	f.git.RunGitScript(t, script)
	stdout, stderr, err := runConeStderr(t, f.git.Dir(), args...)
	must.NoError(t, err)
	return ansi.Strip(stdout), stderr
}

func TestSelectComparison(t *testing.T) {
	tests := []struct {
		name    string
		options coneOptions
		dirty   bool
		want    coneComparison
	}{
		{
			name: "clean compares the last commit",
			want: coneComparison{base: "HEAD^", head: "HEAD"},
		},
		{
			name:  "dirty compares the working tree",
			dirty: true,
			want:  coneComparison{base: "HEAD", head: "HEAD", headWorkingTree: true},
		},
		{
			name:    "--working-tree on a clean tree",
			options: coneOptions{workingTree: true},
			want:    coneComparison{base: "HEAD", head: "HEAD", headWorkingTree: true},
		},
		{
			name:    "--base alone keeps the head of a clean tree",
			options: coneOptions{base: "origin/main"},
			want:    coneComparison{base: "origin/main", head: "HEAD"},
		},
		{
			name:    "--base alone compares the working tree of a dirty tree",
			options: coneOptions{base: "origin/main"},
			dirty:   true,
			want:    coneComparison{base: "origin/main", head: "HEAD", headWorkingTree: true},
		},
		{
			name:    "--head alone keeps the base of a clean tree",
			options: coneOptions{head: "feature"},
			want:    coneComparison{base: "HEAD^", head: "feature"},
		},
		{
			name:    "--head alone keeps the base of a dirty tree and ignores its changes",
			options: coneOptions{head: "feature"},
			dirty:   true,
			want:    coneComparison{base: "HEAD", head: "feature"},
		},
		{
			name:    "both flags ignore a dirty tree",
			options: coneOptions{base: "HEAD^", head: "HEAD"},
			dirty:   true,
			want:    coneComparison{base: "HEAD^", head: "HEAD"},
		},
		{
			name:    "--working-tree overlays an explicit head",
			options: coneOptions{head: "feature", workingTree: true},
			want:    coneComparison{base: "HEAD", head: "feature", headWorkingTree: true},
		},
		{
			name:    "--working-tree overlays an explicit head with an explicit base",
			options: coneOptions{base: "HEAD^", head: "HEAD", workingTree: true},
			want:    coneComparison{base: "HEAD^", head: "HEAD", headWorkingTree: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			test.Eq(t, tt.want, selectComparison(tt.options, tt.dirty))
		})
	}
}

func TestConeComparisonString(t *testing.T) {
	test.EqOp(t, "HEAD^..HEAD", coneComparison{base: "HEAD^", head: "HEAD"}.String())
	test.EqOp(t, "HEAD..working tree", coneComparison{base: "HEAD", head: "HEAD", headWorkingTree: true}.String())
	test.EqOp(t, "HEAD^..feature + working tree", coneComparison{base: "HEAD^", head: "feature", headWorkingTree: true}.String())
}

func TestConeOptionsAskGitOnlyWhenTheStateMatters(t *testing.T) {
	test.True(t, coneOptions{}.dependsOnWorkingTreeState())
	test.True(t, coneOptions{base: "a"}.dependsOnWorkingTreeState())
	test.True(t, coneOptions{head: "b"}.dependsOnWorkingTreeState())
	test.False(t, coneOptions{base: "a", head: "b"}.dependsOnWorkingTreeState())
	test.False(t, coneOptions{workingTree: true}.dependsOnWorkingTreeState())
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

func TestConeCommandExplicitRevisionsIgnoreUncommittedChanges(t *testing.T) {
	f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))
	f.git.WriteFiles(t, map[string]string{"README.md": "edited but not committed"})

	got, stderr, err := runConeStderr(t, f.git.Dir(), "--base", "HEAD^", "--head", "HEAD")

	must.NoError(t, err)
	assertGraphGolden(t, "subset-edit", got)
	test.EqOp(t, "cone: HEAD^..HEAD\n", stderr)
}

func TestConeCommandDefaultsFollowTheWorkingTree(t *testing.T) {
	const lastCommit = "cone: HEAD^..HEAD\n"
	const workingTree = "cone: HEAD..working tree\n"
	lastCommitOutput := func() string {
		f := newChangeFixture(t, subsetFiles("1"), subsetFiles("2"))
		return ansi.Strip(f.render(t, "", PrintGraphOptions{}))
	}()

	tests := []struct {
		name       string
		script     string
		wantStderr string
		want       string
	}{
		{name: "clean", script: "true", wantStderr: lastCommit, want: lastCommitOutput},
		{name: "only touched", script: "touch README.md internal/value/value.go", wantStderr: lastCommit, want: lastCommitOutput},
		{name: "ignored file", script: "echo x > ignored.txt && echo ignored.txt > .git/info/exclude", wantStderr: lastCommit, want: lastCommitOutput},
		{
			name:       "unstaged edit",
			script:     "echo 'edited' > README.md",
			wantStderr: workingTree,
			want:       ".\n└── README.md\n",
		},
		{
			name:       "staged edit",
			script:     "echo 'edited' > README.md && git add README.md",
			wantStderr: workingTree,
			want:       ".\n└── README.md\n",
		},
		{
			name:       "untracked file",
			script:     "echo 'notes' > NOTES.md",
			wantStderr: workingTree,
			want:       ".\n└── NOTES.md\n",
		},
		{
			name:       "deleted file",
			script:     "rm README.md",
			wantStderr: workingTree,
			want:       ".\n",
		},
		{
			name:       "staged edit reverted on disk",
			script:     "echo 'edited' > README.md && git add README.md && git show HEAD:README.md > README.md",
			wantStderr: workingTree,
			want:       "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, stderr := coneAfter(t, tt.script)

			test.EqOp(t, tt.want, got)
			test.EqOp(t, tt.wantStderr, stderr)
		})
	}
}

func TestConeCommandWorkingTreeFlag(t *testing.T) {
	t.Run("on a clean tree compares HEAD with itself", func(t *testing.T) {
		got, stderr := coneAfter(t, "true", "--working-tree")

		test.EqOp(t, "", got)
		test.EqOp(t, "cone: HEAD..working tree\n", stderr)
	})
	t.Run("overlays an explicit head", func(t *testing.T) {
		got, stderr := coneAfter(t, "echo edited > README.md", "--base", "HEAD^", "--head", "HEAD", "--working-tree")

		test.StrContains(t, got, "README.md")
		test.StrContains(t, got, "value.go")
		test.EqOp(t, "cone: HEAD^..working tree\n", stderr)
	})
}

func TestConeCommandQuietSilencesTheComparison(t *testing.T) {
	got, stderr := coneAfter(t, "echo edited > README.md", "--quiet")

	test.EqOp(t, ".\n└── README.md\n", got)
	test.EqOp(t, "", stderr)
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
	t.Run("a clean repository with one commit has no parent", func(t *testing.T) {
		_, err := runCone(t, single.Dir())

		test.ErrorContains(t, err, `resolve base revision "HEAD^"`)
	})
	t.Run("a dirty repository with one commit compares with HEAD", func(t *testing.T) {
		single.WriteFiles(t, map[string]string{"README.md": "edited"})

		got, err := runCone(t, single.Dir())

		must.NoError(t, err)
		test.StrContains(t, ansi.Strip(got), "README.md")
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
	for _, limitation := range []string{"the script a target runs", "//go:embed", "go.work", "over-approximates", "uncommitted changes"} {
		test.StrContains(t, got, limitation)
	}
}
