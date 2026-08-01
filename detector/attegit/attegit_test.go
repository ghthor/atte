package attegit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ghthor/atte/detector/attegittest"
	"github.com/shoenig/test/must"
)

func TestOpenAndShow(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile("root.txt", []byte("root"), 0o644)
	git.WriteFile("nested/child.txt", []byte("child"), 0o644)
	git.RunGitScript(`
set -x
git add .
git commit -m initial
`)
	dir := git.Dir()

	repo, err := Open(dir, "HEAD")
	must.NoError(t, err)

	root := repo.Tree[""]
	must.Len(t, 2, root)

	nested := repo.Tree["nested"]
	must.Len(t, 1, nested)
	must.EqOp(t, Path("nested/child.txt"), nested[0].Path)

	got, err := repo.Show("nested/child.txt")
	must.NoError(t, err)
	must.EqOp(t, "child", string(got))

	_, err = repo.Show("nested")
	must.Error(t, err)

	_, err = repo.Show("missing")
	must.Error(t, err)
}

func TestWorkingTree(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile("tracked.txt", []byte("committed"), 0o644)
	git.RunGitScript("git add . && git commit -m initial")
	git.WriteFile("tracked.txt", []byte("modified"), 0o644)
	git.WriteFile("nested/untracked.txt", []byte("new"), 0o644)
	git.WriteFile("staged.txt", []byte("staged"), 0o644)
	git.RunGitScript("git add staged.txt")
	dir := git.Dir()

	committed, err := Open(dir, "HEAD")
	must.NoError(t, err)
	got, err := committed.Show("tracked.txt")
	must.NoError(t, err)
	must.EqOp(t, "committed", string(got))
	must.MapNotContainsKey(t, committed.Obj, "nested/untracked.txt")

	repo, err := Open(dir, "HEAD", WithWorkingTree())
	must.NoError(t, err)
	got, err = repo.Show("staged.txt")
	must.NoError(t, err)
	must.EqOp(t, "staged", string(got))
	must.EqOp(t, WorkingTreeSource, repo.Obj["staged.txt"].Source)
	got, err = repo.Show("tracked.txt")
	must.NoError(t, err)
	must.EqOp(t, "modified", string(got))
	got, err = repo.Show("nested/untracked.txt")
	must.NoError(t, err)
	must.EqOp(t, "new", string(got))
	must.EqOp(t, Blob, repo.Obj["nested/untracked.txt"].Kind)
}

func TestWorkingTreeRejectsSymlink(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile("target.txt", []byte("target"), 0o644)
	git.WriteFile("link.txt", []byte("committed"), 0o644)
	git.RunGitScript("git add . && git commit -m initial")
	dir := git.Dir()
	must.NoError(t, os.Remove(filepath.Join(dir, "link.txt")))
	must.NoError(t, os.Symlink("target.txt", filepath.Join(dir, "link.txt")))

	_, err := Open(dir, "HEAD", WithWorkingTree())
	must.Error(t, err)
	must.ErrorContains(t, err, "working-tree symlink")
}

func TestGitAlternates(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile("alternate.txt", []byte("from alternate"), 0o644)
	git.RunGitScript(`
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
	got, err := repo.Show("alternate.txt")
	must.NoError(t, err)
	must.EqOp(t, "from alternate", string(got))
}

func TestTreeIndex(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFile("root.txt", []byte("root"), 0o644)
	git.WriteFile("nested/child.txt", []byte("child"), 0o644)
	git.WriteFile("nested/deeper/leaf.txt", []byte("leaf"), 0o644)
	git.RunGitScript(`
set -x
git add .
git commit -m initial
`)

	repo, err := Open(git.Dir(), "HEAD")
	must.NoError(t, err)

	assertTreeChildren(t, repo.Tree[""], map[Path]Kind{
		"root.txt": Blob,
		"nested":   Tree,
	})
	assertTreeChildren(t, repo.Tree["nested"], map[Path]Kind{
		"nested/child.txt": Blob,
		"nested/deeper":    Tree,
	})
	assertTreeChildren(t, repo.Tree["nested/deeper"], map[Path]Kind{
		"nested/deeper/leaf.txt": Blob,
	})

	must.MapLen(t, 0, sliceToMap(repo.Tree["nested/child.txt"]))
	must.SliceEqOp(t, []Path{"nested", "nested/child.txt", "nested/deeper", "nested/deeper/leaf.txt", "root.txt"}, repo.ObjKeys)
	must.SliceEqOp(t, []Path{"", "nested", "nested/deeper"}, repo.TreeKeys)
}

func assertTreeChildren(t *testing.T, got []Obj, want map[Path]Kind) {
	t.Helper()
	must.MapLen(t, len(want), sliceToMap(got))
	for _, obj := range got {
		must.MapContainsKey(t, want, obj.Path, must.Sprintf("unexpected tree child %q", obj.Path))
		must.EqOp(t, want[obj.Path], obj.Kind, must.Sprintf("tree child %q kind mismatch", obj.Path))
	}
}

func sliceToMap(objs []Obj) map[Path]Obj {
	m := make(map[Path]Obj, len(objs))
	for _, obj := range objs {
		m[obj.Path] = obj
	}
	return m
}
