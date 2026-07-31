package attegit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/shoenig/test/must"
)

func TestOpenAndShow(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	must.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o755))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0o644))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "nested", "child.txt"), []byte("child"), 0o644))
	runGitScript(t, dir, `
set -x
git add .
git commit -m initial
`)

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

func TestTreeIndex(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	must.NoError(t, os.MkdirAll(filepath.Join(dir, "nested", "deeper"), 0o755))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0o644))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "nested", "child.txt"), []byte("child"), 0o644))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "nested", "deeper", "leaf.txt"), []byte("leaf"), 0o644))
	runGitScript(t, dir, `
set -x
git add .
git commit -m initial
`)

	repo, err := Open(dir, "HEAD")
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
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGitScript(t, dir, `
set -x
git init
git config user.email test@example.com
git config user.name Test
git config commit.gpgSign false
git config tag.gpgSign false
`)
}

func assertTreeChildren(t *testing.T, got []Obj, want map[Path]Kind) {
	t.Helper()
	must.MapLen(t, len(want), sliceToMap(got))
	for _, obj := range got {
		kind, ok := want[obj.Path]
		must.True(t, ok, must.Sprintf("unexpected tree child %q", obj.Path))
		must.EqOp(t, kind, obj.Kind, must.Sprintf("tree child %q kind mismatch", obj.Path))
	}
}

func sliceToMap(objs []Obj) map[Path]Obj {
	m := make(map[Path]Obj, len(objs))
	for _, obj := range objs {
		m[obj.Path] = obj
	}
	return m
}

func runGitScript(t *testing.T, dir, script string) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	must.NoError(t, err, must.Sprintf("git script: %s", output))
}
