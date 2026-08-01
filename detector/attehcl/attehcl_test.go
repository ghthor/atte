package attehcl

import (
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/graph"
	"github.com/ghthor/atte/reference"
	"github.com/shoenig/test/must"
)

func testPath(raw string) reference.Path {
	if raw == "" {
		return reference.Root
	}
	p, err := reference.ParsePath(raw)
	if err != nil {
		panic(err)
	}
	return p
}

func TestGraphLabeledAndUnlabeledTests(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `

test {
  script = "./first.sh"
  depends_on = ["//config.yaml"]
}

test "unit" {
  script = "./unit.sh"
  triggered_by = ["./trigger.yaml"]
}
`,
		"first.sh":     "#!/bin/sh\n",
		"unit.sh":      "#!/bin/sh\n",
		"config.yaml":  "config\n",
		"trigger.yaml": "trigger\n",
	})

	got, err := GraphWithContainment(repo)
	must.NoError(t, err)

	first := EntityID("atte.hcl", "0")
	unit := EntityID("atte.hcl", "unit")
	must.EqOp(t, TestKind, got.Entities[first].Kind)
	must.EqOp(t, TestKind, got.Entities[unit].Kind)
	must.True(t, hasRelation(got, first, attegit.EntityID(testPath("atte.hcl")), SourceFileRelation))
	must.True(t, hasRelation(got, first, attegit.EntityID(testPath("first.sh")), ScriptRelation))
	must.True(t, hasRelation(got, first, attegit.EntityID(testPath("config.yaml")), DependsOnRelation))
	must.True(t, hasRelation(got, unit, attegit.EntityID(testPath("unit.sh")), ScriptRelation))
	must.True(t, hasRelation(got, unit, attegit.EntityID(testPath("trigger.yaml")), DependsOnRelation))
	must.True(t, hasRelation(got, attegit.EntityID(reference.Root), first, attegit.ContainsRelation))
	must.True(t, hasRelation(got, attegit.EntityID(reference.Root), unit, attegit.ContainsRelation))
}

func TestGraphGopkgTestDependency(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"go.mod": `
module example.com/root

go 1.24
`,
		"p/p.go": `
package p
`,
		"p/p_test.go": `
package p_test

import "testing"

func TestP(t *testing.T) {}
`,
		"atte.hcl": `

test "go" {
  script = "./test.sh"
  depends_on = [gopkg_test("example.com/root/p")]
}
`,
		"test.sh": "#!/bin/sh\n",
	})

	got, err := Graph(repo)
	must.NoError(t, err)

	testID := EntityID("atte.hcl", "go")
	packageTestID := attego.EntityID(attego.PackageTestKind, "", "example.com/root/p")
	must.EqOp(t, attego.PackageTestKind, got.Entities[packageTestID].Kind)
	must.True(t, hasRelation(got, testID, packageTestID, DependsOnRelation))
}

func TestGraphRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{
			name: "malformed HCL",
			file: "test {",
		},
		{
			name: "missing script",
			file: "test {}",
		},
		{
			name: "missing dependency",
			file: `test { script = "./missing.sh" }`,
		},
		{
			name: "path traversal",
			file: `test { script = "../../missing.sh" }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newHCLFixture(t, map[string]string{
				"nested/atte.hcl": tt.file,
			})
			_, err := Graph(repo)
			must.Error(t, err)
		})
	}
}

func TestEntityIDRoundTrip(t *testing.T) {
	id := EntityID("nested/atte.hcl", "unit")
	file, name, err := DecodeEntityID(id)
	must.NoError(t, err)
	must.EqOp(t, reference.Blob("nested/atte.hcl"), file)
	must.EqOp(t, "unit", name)
}

func newHCLFixture(t *testing.T, files map[string]string) *attegit.Repo {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, files, attegittest.WithTrimContent(true))
	git.CommitAll(t, "init")
	repo, err := attegit.Open(git.Dir(), "HEAD")
	must.NoError(t, err)
	return repo
}

func hasRelation(g *graph.Graph, from, to graph.EntityID, kind graph.RelationKind) bool {
	for _, relation := range g.Out(from) {
		if relation.To == to && relation.Kind == kind {
			return true
		}
	}
	return false
}
