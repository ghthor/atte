package attego

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/graph"
	"github.com/shoenig/test/must"
)

func TestGraphPackagesAndTests(t *testing.T) {
	dir := t.TempDir()
	attegittest.InitGitRepo(t, dir)
	must.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/root\n\ngo 1.20\n"), 0o644))
	must.NoError(t, os.MkdirAll(filepath.Join(dir, "p"), 0o755))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "p", "p.go"), []byte("package p\nimport \"fmt\"\nvar _ = fmt.Println\n"), 0o644))
	must.NoError(t, os.WriteFile(filepath.Join(dir, "p", "p_test.go"), []byte("package p_test\nimport (\"example.com/root/p\"; \"testing\")\nvar _ = p.X\nvar _ *testing.T\n"), 0o644))
	attegittest.RunGitScript(t, dir, "git add . && git commit -qm init")
	r, e := attegit.Open(dir, "HEAD")
	must.NoError(t, e)
	g, e := Graph(r)
	must.NoError(t, e)
	var normal, test graph.EntityID
	for _, id := range g.EntityKeys {
		if g.Entities[id].Kind == PackageKind && string(id) != "" {
			normal = id
		}
		if g.Entities[id].Kind == PackageTestKind {
			test = id
		}
	}
	must.NotEq(t, graph.EntityID(""), normal, must.Sprintf("missing normal node: %#v", g.EntityKeys))
	must.NotEq(t, graph.EntityID(""), test, must.Sprintf("missing test node: %#v", g.EntityKeys))
	must.Len(t, 2, g.Out(test))
}
