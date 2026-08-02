package attehcl

import (
	"strings"
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
  script = path("./first.sh")
  depends_on = ["//config.yaml"]
}

test "unit" {
  script = path("./unit.sh")
  triggered_by = ["./trigger.yaml"]
}
`,
		"first.sh":     "#!/bin/sh\n",
		"unit.sh":      "#!/bin/sh\n",
		"config.yaml":  "config\n",
		"trigger.yaml": "trigger\n",
	})

	got, err := GraphWithContainmentAndFunctions(repo, attegit.PathHCLFunctions)
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
  script = path("./test.sh")
}

test "unit" {
  script = path("./test.sh")
}

codegen {
  script = path("./codegen.sh")
}

codegen "proto" {
  script = path("./codegen.sh")
}

lint {
  script = path("./lint.sh")
}

lint "vet" {
  script = path("./lint.sh")
}
`,
		"test.sh":    "#!/bin/sh\n",
		"codegen.sh": "#!/bin/sh\n",
		"lint.sh":    "#!/bin/sh\n",
	})

	got, err := GraphWithContainmentAndFunctions(repo, attegit.PathHCLFunctions)
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
  script = path("./test.sh")
  depends_on = [gopkg_test("example.com/root/p")]
}
`,
		"test.sh": "#!/bin/sh\n",
	})

	got, err := GraphWithFunctions(repo, attegit.PathHCLFunctions)
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
			file: `test { script = path("./missing.sh") }`,
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

func TestGlobalInheritanceAndLocalScope(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `
globals {
  go_ver = "1.26"
  script = path("./root.sh")
}
`,
		"detector/atte.hcl": `
locals {
  script = path("./detector.sh")
  version = global.go_ver
}

test "nested" {
  script = local.script
  depends_on = ["./go.mod"]
}
`,
		"detector/attego/atte.hcl": `
globals {
  go_ver = "1.27"
}

test "override" {
  script = path("./override.sh")
}
`,
		"detector/dummy.sh":           "#!/bin/sh\n",
		"detector/detector.sh":        "#!/bin/sh\n",
		"detector/attego/override.sh": "#!/bin/sh\n",
		"go.mod":                      "module example.com/root\n",
	})
	targets, err := TargetsWithFunctions(repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	must.EqOp(t, 2, len(targets))
	must.EqOp(t, reference.Blob("detector/detector.sh"), targets[0].Script)
	must.EqOp(t, reference.Blob("detector/attego/override.sh"), targets[1].Script)
}

func TestLocalDoesNotPropagate(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `
locals { script = path("./root.sh") }
`,
		"child/atte.hcl": `
test { script = local.script }
`,
		"root.sh": "#!/bin/sh\n",
	})
	_, err := Targets(repo)
	must.Error(t, err)
}

func TestLocalExpressionsAcrossBlockKindsAndGraphConsistency(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
			globals { base = "./shared.sh" }
			locals {
				prefix = "./"
				test_script = "${local.prefix}test.sh"
				codegen_script = "${local.prefix}codegen.sh"
				lint_script = "${local.prefix}lint.sh"
				shared = global.base
			}
			test "one" {
				script = local.test_script
				depends_on = [local.shared]
			}
			codegen "two" {
				script = local.codegen_script
				triggered_by = [local.shared]
			}
			lint "three" {
				script = local.lint_script
			}
		`, "\t"),
		"shared.sh":  "#!/bin/sh\n",
		"test.sh":    "#!/bin/sh\n",
		"codegen.sh": "#!/bin/sh\n",
		"lint.sh":    "#!/bin/sh\n",
	})
	targets, err := TargetsWithFunctions(repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	must.Len(t, 3, targets)
	graphWithoutContainment, err := GraphWithFunctions(repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	graphWithContainment, err := GraphWithContainmentAndFunctions(repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	for _, target := range targets {
		_, inGraph := graphWithoutContainment.Entities[target.ID]
		must.True(t, inGraph, must.Sprint("target should be present in graph"))
		_, inContainedGraph := graphWithContainment.Entities[target.ID]
		must.True(t, inContainedGraph, must.Sprint("target should be present in containment graph"))
	}
}

func TestRepeatedDeclarationsAndDuplicateDeclarationErrors(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
			globals { script = path("./one.sh") }
			globals { version = "one" }
			locals { selected = global.script }
			locals { version = "local" }
			test { script = local.selected }
			`, "\t"),
		"one.sh": "#!/bin/sh\n",
	})
	_, err := TargetsWithFunctions(repo, attegit.PathHCLFunctions)
	must.NoError(t, err)

	duplicate := newHCLFixture(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
			globals { value = "one" }
			globals { value = "two" }
			test { script = path("./one.sh") }
			`, "\t"),
		"one.sh": "#!/bin/sh\n",
	})
	_, err = Targets(duplicate)
	must.Error(t, err)
}

func TestTargetScriptsFromInheritedGlobals(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `

globals {
  version = "root"
  shared_path = path("./root.sh")
}

test "root" { script = path("./root.sh") }
`,
		"child/atte.hcl": `

globals {
  version = "child"
  shared_path = path("./child.sh")
}

test "inherited" { script = path("./child.sh") }
`,
		"child/deeper/atte.hcl": `

globals {
  version = "deep"
  shared_path = path("./deep.sh")
}

test "overridden" { script = path("./deep.sh") }
`,
		"root.sh":              "#!/bin/sh\\n",
		"child/child.sh":       "#!/bin/sh\\n",
		"child/deeper/deep.sh": "#!/bin/sh\\n",
	})
	targets, err := TargetsWithFunctions(repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	must.EqOp(t, 3, len(targets))
	must.EqOp(t, reference.Blob("root.sh"), targets[0].Script)
	must.EqOp(t, reference.Blob("child/child.sh"), targets[1].Script)
	must.EqOp(t, reference.Blob("child/deeper/deep.sh"), targets[2].Script)
	root, err := ConfigForWithFunctions(repo, "", attegit.PathHCLFunctions)
	must.NoError(t, err)
	must.EqOp(t, "root", root.Global["version"].AsString())
	must.EqOp(t, reference.Blob("root.sh"), *root.Global["shared_path"].EncapsulatedValue().(*reference.Blob))
	child, err := ConfigForWithFunctions(repo, "child", attegit.PathHCLFunctions)
	must.NoError(t, err)
	must.EqOp(t, "child", child.Global["version"].AsString())
	must.EqOp(t, reference.Blob("child/child.sh"), *child.Global["shared_path"].EncapsulatedValue().(*reference.Blob))
	deep, err := ConfigForWithFunctions(repo, "child/deeper", attegit.PathHCLFunctions)
	must.NoError(t, err)
	must.EqOp(t, "deep", deep.Global["version"].AsString())
	must.EqOp(t, reference.Blob("child/deeper/deep.sh"), *deep.Global["shared_path"].EncapsulatedValue().(*reference.Blob))
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
