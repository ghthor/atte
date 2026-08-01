package attego

import (
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/graph"
	"github.com/shoenig/test/must"
)

// newBasicFixture creates a temporary Git repository with a root module
// containing package p, whose tests exercise both an internal ("p") and an
// external ("p_test") import.
func newBasicFixture(t *testing.T) string {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	git.WriteFile("go.mod", []byte(`module example.com/root

go 1.24

require github.com/shoenig/test v1.13.2

require github.com/google/go-cmp v0.7.0 // indirect
`), 0o644)
	git.WriteFile("go.sum", []byte(`github.com/google/go-cmp v0.7.0 h1:wk8382ETsv4JYUZwIsn6YpYiWiBsYLSJiTsyBybVuN8=
github.com/google/go-cmp v0.7.0/go.mod h1:pXiqmnSA92OHEEa9HXL2W4E7lf9JzCmGVUdgjX3N/iU=
github.com/shoenig/test v1.13.2 h1:SaGxHxg7xkRuKuNtuFmHf0LgNGaAgcBT7HN4WHCKfqU=
github.com/shoenig/test v1.13.2/go.mod h1:MKmiRyEeuFl8y9PCoThaRDgYQZeWBhRQlH99poXz5LI=
`), 0o644)

	git.WriteFile("p/p.go", []byte(`package p
import (
	"fmt"

	"github.com/shoenig/test/must"
)
var _ = fmt.Println
var _ = must.NoError
`), 0o644)

	git.WriteFile("p/p_test.go", []byte(`package p_test
import (
	"testing"

	"github.com/shoenig/test/must"

	"example.com/root/p"
)
var _ = p.X
var _ *testing.T
var _ = must.NoError
`), 0o644)
	git.RunGitScript("git add . && git commit -qm init")
	return git.Dir()
}

func TestEntityIDRoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		moduleDir  attegit.Path
		importPath string
	}{
		{name: "root module", moduleDir: "", importPath: "example.com/root/p"},
		{name: "nested module", moduleDir: "sub", importPath: "example.com/sub/p"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := EntityID(PackageKind, c.moduleDir, c.importPath)
			kind, moduleDir, importPath, err := DecodeEntityID(id)
			must.NoError(t, err)
			must.EqOp(t, PackageKind, kind)
			must.EqOp(t, c.moduleDir, moduleDir)
			must.EqOp(t, c.importPath, importPath)
		})
	}
}

func TestEntityIDContainsGoModPath(t *testing.T) {
	must.EqOp(t, graph.EntityID("attego:package:go.mod:ZXhhbXBsZS5jb20vcm9vdC9w"), EntityID(PackageKind, "", "example.com/root/p"))
	must.EqOp(t, graph.EntityID("attego:package:sub/go.mod:ZXhhbXBsZS5jb20vcm9vdC9w"), EntityID(PackageKind, "sub", "example.com/root/p"))
}

func TestDecodeEntityIDRejectsMalformed(t *testing.T) {
	_, _, _, err := DecodeEntityID(graph.EntityID("attego:package:not-a-gomod-path:ZXhhbXBsZS5jb20vcm9vdC9w"))
	must.Error(t, err)
}

func TestGraphMatchesGoList(t *testing.T) {
	dir := newBasicFixture(t)
	r, e := attegit.Open(dir, "HEAD")
	must.NoError(t, e)
	got, e := Graph(r)
	must.NoError(t, e)
	assertGraphsEqual(t, goListGraph(t, dir), got)
}

func TestGraphPackagesAndTests(t *testing.T) {
	dir := newBasicFixture(t)
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
	must.Len(t, 3, g.Out(test))
}
