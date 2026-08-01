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
	git.WriteFiles(t, map[string]string{
		"go.mod": `
module example.com/root

go 1.24

require github.com/shoenig/test v1.13.2

require github.com/google/go-cmp v0.7.0 // indirect
`,
		"go.sum": `
github.com/google/go-cmp v0.7.0 h1:wk8382ETsv4JYUZwIsn6YpYiWiBsYLSJiTsyBybVuN8=
github.com/google/go-cmp v0.7.0/go.mod h1:pXiqmnSA92OHEEa9HXL2W4E7lf9JzCmGVUdgjX3N/iU=
github.com/shoenig/test v1.13.2 h1:SaGxHxg7xkRuKuNtuFmHf0LgNGaAgcBT7HN4WHCKfqU=
github.com/shoenig/test v1.13.2/go.mod h1:MKmiRyEeuFl8y9PCoThaRDgYQZeWBhRQlH99poXz5LI=
`,
		"p/p.go": `
package p
import (
	"fmt"

	"github.com/shoenig/test/must"
)
var _ = fmt.Println
var _ = must.NoError
`,
		"p/p_test.go": `
package p_test
import (
	"testing"

	"github.com/shoenig/test/must"

	"example.com/root/p"
)
var _ = p.X
var _ *testing.T
var _ = must.NoError
`,
	})
	git.CommitAll(t, "init")
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

func openTestGraph(t *testing.T, dir string) *graph.Graph {
	t.Helper()
	repo, err := attegit.Open(dir, "HEAD")
	must.NoError(t, err)
	got, err := Graph(repo)
	must.NoError(t, err)
	return got
}

func openTestGraphWithContainment(t *testing.T, dir string) *graph.Graph {
	t.Helper()
	repo, err := attegit.Open(dir, "HEAD")
	must.NoError(t, err)
	got, err := GraphWithContainment(repo)
	must.NoError(t, err)
	return got
}

func TestGraphMatchesGoList(t *testing.T) {
	dir := newBasicFixture(t)
	assertGraphsEqual(t, goListGraph(t, dir), openTestGraph(t, dir))
}

func TestGraphPackagesAndTests(t *testing.T) {
	repo := newBasicFixture(t)
	g := openTestGraphWithContainment(t, repo)
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
	packageID := EntityID(PackageKind, "", "example.com/root/p")
	normalFile := attegit.EntityID("p/p.go")
	testFile := attegit.EntityID("p/p_test.go")
	must.True(t, hasRelation(g, test, packageID, attegit.ContainsRelation))
	must.True(t, hasRelation(g, normal, normalFile, SourceFileRelation))
	must.False(t, hasRelation(g, normal, testFile, SourceFileRelation))
	must.True(t, hasRelation(g, test, testFile, SourceFileRelation))
	must.False(t, hasRelation(g, test, normalFile, SourceFileRelation))
	treeID := attegit.EntityID("p")
	must.True(t, hasRelation(g, treeID, normal, attegit.ContainsRelation))
	must.True(t, hasRelation(g, treeID, test, attegit.ContainsRelation))
	must.False(t, hasRelation(g, normal, treeID, attegit.ContainsRelation))
}

func hasRelation(g *graph.Graph, from, to graph.EntityID, kind graph.RelationKind) bool {
	for _, relation := range g.Out(from) {
		if relation.To == to && relation.Kind == kind {
			return true
		}
	}
	return false
}
