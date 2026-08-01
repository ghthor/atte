package attehcl

import (
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/graph"
	"github.com/ghthor/atte/graph/graphtest"
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

	first := EntityID(TestKind, "atte.hcl", "0")
	unit := EntityID(TestKind, "atte.hcl", "unit")
	must.EqOp(t, TestKind, got.Entities[first].Kind)
	must.EqOp(t, TestKind, got.Entities[unit].Kind)
	graphtest.MustHaveRelation(t, got, first, attegit.EntityID(testPath("atte.hcl")), SourceFileRelation)
	graphtest.MustHaveRelation(t, got, first, attegit.EntityID(testPath("first.sh")), ScriptRelation)
	graphtest.MustHaveRelation(t, got, first, attegit.EntityID(testPath("config.yaml")), DependsOnRelation)
	graphtest.MustHaveRelation(t, got, unit, attegit.EntityID(testPath("unit.sh")), ScriptRelation)
	graphtest.MustHaveRelation(t, got, unit, attegit.EntityID(testPath("trigger.yaml")), DependsOnRelation)
	graphtest.MustHaveRelation(t, got, attegit.EntityID(reference.Root), first, attegit.ContainsRelation)
	graphtest.MustHaveRelation(t, got, attegit.EntityID(reference.Root), unit, attegit.ContainsRelation)
}

func TestGraphLabeledAndUnlabeledCodegenAndLintBlocks(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `

test {
  script = "./test.sh"
}

test "unit" {
  script = "./test.sh"
}

codegen {
  script = "./codegen.sh"
}

codegen "proto" {
  script = "./codegen.sh"
}

lint {
  script = "./lint.sh"
}

lint "vet" {
  script = "./lint.sh"
}
`,
		"test.sh":    "#!/bin/sh\n",
		"codegen.sh": "#!/bin/sh\n",
		"lint.sh":    "#!/bin/sh\n",
	})

	got, err := GraphWithContainment(repo)
	must.NoError(t, err)

	test := EntityID(TestKind, "atte.hcl", "0")
	testUnit := EntityID(TestKind, "atte.hcl", "unit")
	codegen := EntityID(CodegenKind, "atte.hcl", "0")
	codegenProto := EntityID(CodegenKind, "atte.hcl", "proto")
	lint := EntityID(LintKind, "atte.hcl", "0")
	lintVet := EntityID(LintKind, "atte.hcl", "vet")

	must.EqOp(t, TestKind, got.Entities[test].Kind)
	must.EqOp(t, TestKind, got.Entities[testUnit].Kind)
	must.EqOp(t, CodegenKind, got.Entities[codegen].Kind)
	must.EqOp(t, CodegenKind, got.Entities[codegenProto].Kind)
	must.EqOp(t, LintKind, got.Entities[lint].Kind)
	must.EqOp(t, LintKind, got.Entities[lintVet].Kind)

	for _, id := range []graph.EntityID{test, testUnit, codegen, codegenProto, lint, lintVet} {
		graphtest.MustHaveRelation(t, got, id, attegit.EntityID(testPath("atte.hcl")), SourceFileRelation)
		graphtest.MustHaveRelation(t, got, attegit.EntityID(reference.Root), id, attegit.ContainsRelation)
	}
	graphtest.MustHaveRelation(t, got, test, attegit.EntityID(testPath("test.sh")), ScriptRelation)
	graphtest.MustHaveRelation(t, got, codegen, attegit.EntityID(testPath("codegen.sh")), ScriptRelation)
	graphtest.MustHaveRelation(t, got, lint, attegit.EntityID(testPath("lint.sh")), ScriptRelation)
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

	testID := EntityID(TestKind, "atte.hcl", "go")
	packageTestID := attego.EntityID(attego.PackageTestKind, "", "example.com/root/p")
	must.EqOp(t, attego.PackageTestKind, got.Entities[packageTestID].Kind)
	graphtest.MustHaveRelation(t, got, testID, packageTestID, DependsOnRelation)
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
	for _, kind := range []string{TestKind, CodegenKind, LintKind} {
		t.Run(kind, func(t *testing.T) {
			id := EntityID(kind, "nested/atte.hcl", "unit")
			gotKind, file, name, err := DecodeEntityID(id)
			must.NoError(t, err)
			must.EqOp(t, kind, gotKind)
			must.EqOp(t, reference.Blob("nested/atte.hcl"), file)
			must.EqOp(t, "unit", name)
		})
	}
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
