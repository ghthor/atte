package attegit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/reference"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// gitIn runs git in dir and returns its combined output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	must.NoError(t, err, must.Sprintf("git %v: %s", args, output))
	return string(output)
}

// showAt opens dir at HEAD and asserts that name can be read with want as its
// contents.
func showAt(t *testing.T, dir, name, want string) {
	t.Helper()
	repo, err := Open(dir, "HEAD")
	must.NoError(t, err)
	got, err := repo.Show(testPath(name))
	must.NoError(t, err)
	test.EqOp(t, want, string(got))
}

func initialRepo(t *testing.T) *attegittest.GitRepo {
	t.Helper()
	return committedRepo(t, map[string]string{"initial.go": "package main"})
}

func TestOpenGitDirectories(t *testing.T) {
	const want = "package main\n"

	t.Run("linked worktree", func(t *testing.T) {
		main := initialRepo(t)
		wt := filepath.Join(t.TempDir(), "wt")
		gitIn(t, main.Dir(), "worktree", "add", "--detach", wt)
		showAt(t, wt, "initial.go", want)
	})

	t.Run("linked worktree of repository with absolute alternates", func(t *testing.T) {
		src := initialRepo(t)
		clone := filepath.Join(t.TempDir(), "clone")
		gitIn(t, src.Dir(), "clone", "--shared", src.Dir(), clone)
		wt := filepath.Join(t.TempDir(), "wt")
		gitIn(t, clone, "worktree", "add", "--detach", wt)
		// The blob only exists in the alternate.
		showAt(t, wt, "initial.go", want)
		showAt(t, clone, "initial.go", want)
	})

	t.Run("relative alternates", func(t *testing.T) {
		src := initialRepo(t)
		// Canonicalize the temp dir so the relative alternate path is computed
		// between two real paths (macOS: /tmp -> /private/tmp).
		tmp, err := filepath.EvalSymlinks(t.TempDir())
		must.NoError(t, err)
		clone := filepath.Join(tmp, "clone")
		gitIn(t, src.Dir(), "clone", "--shared", src.Dir(), clone)
		objects := filepath.Join(clone, ".git", "objects")
		rel, err := filepath.Rel(objects, filepath.Join(src.Dir(), ".git", "objects"))
		must.NoError(t, err)
		must.NoError(t, os.WriteFile(filepath.Join(objects, "info", "alternates"), []byte("# comment\n"+rel+"\n"), 0o644))
		gitIn(t, clone, "fsck") // git itself can resolve the relative alternate
		showAt(t, clone, "initial.go", want)
	})

	t.Run("nested alternates", func(t *testing.T) {
		src := initialRepo(t)
		mid := filepath.Join(t.TempDir(), "mid")
		gitIn(t, src.Dir(), "clone", "--shared", src.Dir(), mid)
		clone := filepath.Join(t.TempDir(), "clone")
		gitIn(t, mid, "clone", "--shared", mid, clone)
		showAt(t, clone, "initial.go", want)
	})

	t.Run("bare repository", func(t *testing.T) {
		src := initialRepo(t)
		bare := filepath.Join(t.TempDir(), "bare.git")
		gitIn(t, src.Dir(), "clone", "--bare", src.Dir(), bare)
		showAt(t, bare, "initial.go", want)
	})

	t.Run("missing alternate does not fail loading", func(t *testing.T) {
		git := initialRepo(t)
		git.WriteFile(t, ".git/objects/info/alternates", []byte("/does/not/exist/objects\n"), 0o644)
		showAt(t, git.Dir(), "initial.go", want)
	})
}

func TestShowMissingObject(t *testing.T) {
	git := initialRepo(t)
	repo, err := Open(git.Dir(), "HEAD")
	must.NoError(t, err)
	ghost := testPath("ghost.go")
	repo.Obj[ghost] = Obj{
		Path:   ghost,
		Hash:   plumbing.NewHash("0123456789012345678901234567890123456789"),
		Kind:   Blob,
		Source: GitSource,
	}

	_, err = repo.Show(ghost)
	test.Error(t, err)
}

// The directories read by Show are modified concurrently by other processes,
// such as the shared git mirror used as an object alternate on CI hosts, which
// is repacked while a Repo is in use. go-git caches the list of packs it saw
// at its first read, so Show must re-read the pack directory and retry.
func TestShowSurvivesConcurrentRepack(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, map[string]string{"a.txt": "a", "b.txt": "b"})
	git.CommitAll(t, "init")
	// Everything into a single pack, with no loose objects left.
	git.RunGitScript(t, "git repack -a -d -q && git prune-packed -q")
	packsBefore := gitIn(t, git.Dir(), "count-objects", "-v")
	must.StrContains(t, packsBefore, "packs: 1")

	repo, err := Open(git.Dir(), "HEAD")
	must.NoError(t, err)
	// The first read makes go-git list and cache the pack .idx files.
	got, err := repo.Show(testPath("a.txt"))
	must.NoError(t, err)
	test.EqOp(t, "a\n", string(got))

	// Another process adds objects and repacks: a pack with a different name is
	// written and the old one, which go-git still has indexed, is deleted.
	git.WriteFile(t, "c.txt", []byte("c"), 0o644)
	git.CommitAll(t, "more")
	git.RunGitScript(t, "git repack -a -d -q && git prune-packed -q")
	packsAfter := gitIn(t, git.Dir(), "count-objects", "-v")
	must.StrContains(t, packsAfter, "packs: 1")
	must.NotEq(t, packsBefore, packsAfter)

	got, err = repo.Show(testPath("b.txt"))
	must.NoError(t, err)
	test.EqOp(t, "b\n", string(got))
}

// Small fetches leave loose objects. go-git checks loose objects and then only
// the packs it listed earlier, so an object that was loose at that time and has
// since been packed is reported as missing rather than as a vanished pack.
func TestShowSurvivesLooseObjectsBeingPacked(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile(t, "a.txt", []byte("a"), 0o644)
	git.CommitAll(t, "init")
	git.RunGitScript(t, "git repack -a -d -q && git prune-packed -q")
	// A second commit leaves its objects loose next to the existing pack.
	git.WriteFile(t, "b.txt", []byte("b"), 0o644)
	git.CommitAll(t, "more")
	counts := gitIn(t, git.Dir(), "count-objects", "-v")
	must.StrContains(t, counts, "packs: 1")
	must.StrNotContains(t, counts, "count: 0")

	repo, err := Open(git.Dir(), "HEAD")
	must.NoError(t, err)
	// Reading a packed object makes go-git cache the pack list, which does not
	// include b.txt.
	got, err := repo.Show(testPath("a.txt"))
	must.NoError(t, err)
	test.EqOp(t, "a", string(got))

	// Another process packs the loose objects and deletes the loose copies.
	git.RunGitScript(t, "git repack -d -q && git prune-packed -q")
	counts = gitIn(t, git.Dir(), "count-objects", "-v")
	must.StrContains(t, counts, "count: 0")
	must.StrContains(t, counts, "packs: 2")

	got, err = repo.Show(testPath("b.txt"))
	must.NoError(t, err)
	test.EqOp(t, "b", string(got))
}

func TestWorkingTreeHashes(t *testing.T) {
	git := initialRepo(t)
	git.WriteFile(t, "initial.go", []byte("package main\n// modified\n"), 0o644)
	git.WriteFile(t, "untracked.go", []byte("package main\n// untracked\n"), 0o644)
	git.WriteFile(t, "kept.go", []byte("package main\n"), 0o644)
	git.RunGitScript(t, "git add kept.go && git commit -qm kept")

	repo, err := Open(git.Dir(), "HEAD", WithWorkingTree())
	must.NoError(t, err)

	// Modified and untracked files hash as git would hash them if committed.
	for _, name := range []string{"initial.go", "untracked.go"} {
		want := strings.TrimSpace(gitIn(t, git.Dir(), "hash-object", name))
		test.EqOp(t, want, repo.Obj[testPath(name)].Hash.String(), test.Sprintf("hash of %s", name))
	}
	// Unmodified files keep their committed hash and source.
	want := strings.TrimSpace(gitIn(t, git.Dir(), "rev-parse", "HEAD:kept.go"))
	test.EqOp(t, want, repo.Obj[testPath("kept.go")].Hash.String())
	test.EqOp(t, GitSource, repo.Obj[testPath("kept.go")].Source)
}

func TestWorkingTreeDeletions(t *testing.T) {
	open := func(t *testing.T, git *attegittest.GitRepo) *Repo {
		t.Helper()
		repo, err := Open(git.Dir(), "HEAD", WithWorkingTree())
		must.NoError(t, err)
		return repo
	}

	for name, remove := range map[string]string{
		"unstaged": "rm initial.go",
		"staged":   "git rm -q initial.go",
	} {
		t.Run(name, func(t *testing.T) {
			git := committedRepo(t, map[string]string{"initial.go": "package main", "keep.go": "package keep"})
			git.RunGitScript(t, remove)

			repo := open(t, git)
			test.MapNotContainsKey(t, repo.Obj, testPath("initial.go"))
			test.MapContainsKey(t, repo.Obj, testPath("keep.go"))
			assertTreeChildren(t, repo.Tree[reference.Root], map[reference.Path]Kind{testPath("keep.go"): Blob})
			test.SliceNotContains(t, repo.BlobKeys, reference.Blob("initial.go"))
		})
	}

	t.Run("last file in a directory removes the directory", func(t *testing.T) {
		git := committedRepo(t, map[string]string{"keep.go": "package keep", "mod/deep/go.mod": "module m"})
		git.RunGitScript(t, "rm mod/deep/go.mod")

		repo := open(t, git)
		test.MapNotContainsKey(t, repo.Obj, reference.Path(reference.Tree("mod")))
		test.MapNotContainsKey(t, repo.Obj, reference.Path(reference.Tree("mod/deep")))
		test.SliceEqOp(t, []reference.Tree{reference.Root}, repo.TreeKeys)
		test.SliceEqOp(t, []reference.Path{testPath("keep.go")}, repo.ObjKeys)
	})

	t.Run("rename removes the old path", func(t *testing.T) {
		git := initialRepo(t)
		git.RunGitScript(t, "git mv initial.go renamed.go")

		repo := open(t, git)
		test.MapNotContainsKey(t, repo.Obj, testPath("initial.go"))
		got, err := repo.Show(testPath("renamed.go"))
		must.NoError(t, err)
		test.EqOp(t, "package main\n", string(got))
	})

	t.Run("unusual file names", func(t *testing.T) {
		// Git C-quotes these names without -z.
		names := []string{"é.go", "with space.go", " padded .go", "quo\"te.go", "日本.go"}
		for _, committed := range []bool{false, true} {
			git := initialRepo(t)
			for _, name := range names {
				git.WriteFile(t, name, []byte("package main // "+name), 0o644)
			}
			if committed {
				git.CommitAll(t, "names")
				for _, name := range names {
					git.WriteFile(t, name, []byte("modified "+name), 0o644)
				}
			}

			repo := open(t, git)
			for _, name := range names {
				want := "package main // " + name
				if committed {
					want = "modified " + name
				}
				got, err := repo.Show(testPath(name))
				test.NoError(t, err)
				test.EqOp(t, want, string(got), test.Sprintf("contents of %q", name))
			}
		}
	})

	t.Run("deleted file with an unusual name", func(t *testing.T) {
		git := committedRepo(t, map[string]string{"é.go": "package main", "keep.go": "package keep"})
		git.RunGitScript(t, "rm é.go")

		repo := open(t, git)
		test.MapNotContainsKey(t, repo.Obj, testPath("é.go"))
	})
}
