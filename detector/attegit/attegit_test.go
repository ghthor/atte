package attegit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ghthor/atte/reference"

	"github.com/ghthor/atte/detector/attegittest"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func testPath(raw string) reference.Path {
	p, err := reference.ParseBlob(raw)
	if err != nil {
		panic(err)
	}
	return p
}

func committedRepo(t *testing.T, files map[string]string) *attegittest.GitRepo {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, files)
	git.CommitAll(t, "initial")
	return git
}

func TestOpenAndShow(t *testing.T) {
	git := committedRepo(t, map[string]string{
		"root.txt":         "root",
		"nested/child.txt": "child",
	})
	dir := git.Dir()

	repo, err := Open(dir, "HEAD")
	must.NoError(t, err)

	root := repo.Tree[""]
	test.Len(t, 2, root)

	nested := repo.Tree["nested"]
	test.Len(t, 1, nested)
	test.True(t, nested[0].Path == reference.Blob("nested/child.txt"))

	got, err := repo.Show(testPath("nested/child.txt"))
	must.NoError(t, err)
	test.EqOp(t, "child\n", string(got))

	_, err = repo.Show(testPath("nested"))
	test.Error(t, err)

	_, err = repo.Show(testPath("missing"))
	test.Error(t, err)
}

func TestWorkingTree(t *testing.T) {
	git := committedRepo(t, map[string]string{
		"tracked.txt": "committed",
	})
	git.WriteFile(t, "tracked.txt", []byte("modified"), 0o644)
	git.WriteFile(t, "nested/untracked.txt", []byte("new"), 0o644)
	git.WriteFile(t, "staged.txt", []byte("staged"), 0o644)
	git.RunGitScript(t, "git add staged.txt")
	dir := git.Dir()

	committed, err := Open(dir, "HEAD")
	must.NoError(t, err)
	got, err := committed.Show(testPath("tracked.txt"))
	must.NoError(t, err)
	test.EqOp(t, "committed\n", string(got))
	test.MapNotContainsKey(t, committed.Obj, testPath("nested/untracked.txt"))

	repo, err := Open(dir, "HEAD", WithWorkingTree())
	must.NoError(t, err)
	got, err = repo.Show(testPath("staged.txt"))
	must.NoError(t, err)
	test.EqOp(t, "staged", string(got))
	test.EqOp(t, WorkingTreeSource, repo.Obj[testPath("staged.txt")].Source)
	got, err = repo.Show(testPath("tracked.txt"))
	must.NoError(t, err)
	test.EqOp(t, "modified", string(got))
	got, err = repo.Show(testPath("nested/untracked.txt"))
	must.NoError(t, err)
	test.EqOp(t, "new", string(got))
	test.EqOp(t, Blob, repo.Obj[testPath("nested/untracked.txt")].Kind)

	// The untracked blob needs synthesized ancestor trees to remain reachable
	// through the repository indexes, and those ancestors belong to the
	// working-tree overlay rather than the committed revision.
	nested := reference.Tree("nested")
	test.EqOp(t, Tree, repo.Obj[nested].Kind)
	test.EqOp(t, WorkingTreeSource, repo.Obj[nested].Source)
	assertTreeChildren(t, repo.Tree[reference.Root], map[reference.Path]Kind{
		testPath("tracked.txt"): Blob,
		nested:                  Tree,
		testPath("staged.txt"):  Blob,
	})
	assertTreeChildren(t, repo.Tree[nested], map[reference.Path]Kind{
		testPath("nested/untracked.txt"): Blob,
	})
}

func TestWorkingTreeRejectsSymlink(t *testing.T) {
	git := committedRepo(t, map[string]string{
		"target.txt": "target",
		"link.txt":   "committed",
	})
	dir := git.Dir()
	must.NoError(t, os.Remove(filepath.Join(dir, "link.txt")))
	must.NoError(t, os.Symlink("target.txt", filepath.Join(dir, "link.txt")))

	_, err := Open(dir, "HEAD", WithWorkingTree())
	test.Error(t, err)
	test.ErrorContains(t, err, "working-tree symlink")
}

func TestGitAlternates(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile(t, "alternate.txt", []byte("from alternate"), 0o644)
	git.RunGitScript(t, `
set -eux
 git add alternate.txt
 git commit -m initial
 git clone --bare . ../objects.git
 git -C ../objects.git repack -a -d --window=0 --depth=0
 printf '%s/objects\n' "$(cd ../objects.git && pwd)" > .git/objects/info/alternates
 find .git/objects -mindepth 1 -maxdepth 1 -type d -regex '.*/[0-9a-f][0-9a-f]' -exec rm -rf {} +
`)
	dir := git.Dir()

	repo, err := Open(dir, "HEAD")
	must.NoError(t, err)
	got, err := repo.Show(testPath("alternate.txt"))
	must.NoError(t, err)
	test.EqOp(t, "from alternate", string(got))
}

func TestTreeIndex(t *testing.T) {
	git := committedRepo(t, map[string]string{
		"root.txt":               "root",
		"nested/child.txt":       "child",
		"nested/deeper/leaf.txt": "leaf",
	})

	repo, err := Open(git.Dir(), "HEAD")
	must.NoError(t, err)

	assertTreeChildren(t, repo.Tree[""], map[reference.Path]Kind{
		testPath("root.txt"):     Blob,
		reference.Tree("nested"): Tree,
	})
	assertTreeChildren(t, repo.Tree["nested"], map[reference.Path]Kind{
		testPath("nested/child.txt"):    Blob,
		reference.Tree("nested/deeper"): Tree,
	})
	assertTreeChildren(t, repo.Tree["nested/deeper"], map[reference.Path]Kind{
		testPath("nested/deeper/leaf.txt"): Blob,
	})

	must.MapLen(t, 0, sliceToMap(repo.Tree["nested/child.txt"]))
	test.SliceEqOp(t, []reference.Path{reference.Tree("nested"), reference.Blob("nested/child.txt"), reference.Tree("nested/deeper"), reference.Blob("nested/deeper/leaf.txt"), reference.Blob("root.txt")}, repo.ObjKeys)
	test.SliceEqOp(t, []reference.Tree{reference.Root, reference.Tree("nested"), reference.Tree("nested/deeper")}, repo.TreeKeys)
}

func assertTreeChildren(t *testing.T, got []Obj, want map[reference.Path]Kind) {
	t.Helper()
	must.MapLen(t, len(want), sliceToMap(got))
	for _, obj := range got {
		test.MapContainsKey(t, want, obj.Path, test.Sprintf("unexpected tree child %q", obj.Path))
		test.EqOp(t, want[obj.Path], obj.Kind, test.Sprintf("tree child %q kind mismatch", obj.Path))
	}
}

func sliceToMap(objs []Obj) map[reference.Path]Obj {
	m := make(map[reference.Path]Obj, len(objs))
	for _, obj := range objs {
		m[obj.Path] = obj
	}
	return m
}
