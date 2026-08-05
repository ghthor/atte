package attehcl

import (
	"strings"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graph/graphtest"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/reference"
	"github.com/shoenig/test"
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

	got, err := Graph(t.Context(), repo, graphset.WithAttachToTree(), WithFunctions(attegit.PathHCLFunctions))
	must.NoError(t, err)

	first := EntityID(TestKind, "atte.hcl", "0")
	unit := EntityID(TestKind, "atte.hcl", "unit")
	test.EqOp(t, TestKind, got.Entities[first].Kind)
	test.EqOp(t, TestKind, got.Entities[unit].Kind)
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

	got, err := Graph(t.Context(), repo, graphset.WithAttachToTree(), WithFunctions(attegit.PathHCLFunctions))
	must.NoError(t, err)

	testTarget := EntityID(TestKind, "atte.hcl", "0")
	testUnit := EntityID(TestKind, "atte.hcl", "unit")
	codegen := EntityID(CodegenKind, "atte.hcl", "0")
	codegenProto := EntityID(CodegenKind, "atte.hcl", "proto")
	lint := EntityID(LintKind, "atte.hcl", "0")
	lintVet := EntityID(LintKind, "atte.hcl", "vet")

	test.EqOp(t, TestKind, got.Entities[testTarget].Kind)
	test.EqOp(t, TestKind, got.Entities[testUnit].Kind)
	test.EqOp(t, CodegenKind, got.Entities[codegen].Kind)
	test.EqOp(t, CodegenKind, got.Entities[codegenProto].Kind)
	test.EqOp(t, LintKind, got.Entities[lint].Kind)
	test.EqOp(t, LintKind, got.Entities[lintVet].Kind)

	for _, id := range []graph.EntityID{testTarget, testUnit, codegen, codegenProto, lint, lintVet} {
		graphtest.MustHaveRelation(t, got, id, attegit.EntityID(testPath("atte.hcl")), SourceFileRelation)
		graphtest.MustHaveRelation(t, got, attegit.EntityID(reference.Root), id, attegit.ContainsRelation)
	}
	graphtest.MustHaveRelation(t, got, testTarget, attegit.EntityID(testPath("test.sh")), ScriptRelation)
	graphtest.MustHaveRelation(t, got, codegen, attegit.EntityID(testPath("codegen.sh")), ScriptRelation)
	graphtest.MustHaveRelation(t, got, lint, attegit.EntityID(testPath("lint.sh")), ScriptRelation)
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
			_, err := Graph(t.Context(), repo)
			test.Error(t, err)
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
	targets, err := Targets(t.Context(), repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.EqOp(t, 2, len(targets))
	test.EqOp(t, reference.Blob("detector/detector.sh"), targets[0].Script)
	test.EqOp(t, reference.Blob("detector/attego/override.sh"), targets[1].Script)
}

func TestGlobalsCannotDependOnLocals(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `
locals {
  script = "./root.sh"
}

globals {
  inherited_script = local.script
}

test { script = global.inherited_script }
`,
		"root.sh": "#!/bin/sh\n",
	})

	_, err := Targets(t.Context(), repo, nil)
	test.Error(t, err)

	_, err = Graph(t.Context(), repo)
	test.Error(t, err)
}

func TestGlobalInheritanceAcrossDirectories(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
			globals { base = "root" }
			test { script = path("./root.sh") }
		`, "\t"),
		"child/atte.hcl": strings.TrimLeft(`
			globals { child = "${global.base}-child" }
		`, "\t"),
		"child/no-atte/marker.txt": "directory without atte.hcl\n",
		"child/deeper/atte.hcl": strings.TrimLeft(`
			globals { deep = "${global.child}-deep" }
		`, "\t"),
		"root.sh": "#!/bin/sh\n",
	})

	root, err := ConfigFor(t.Context(), repo, "", attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.EqOp(t, "root", root.Global["base"].AsString())

	child, err := ConfigFor(t.Context(), repo, "child", attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.EqOp(t, "root", child.Global["base"].AsString())
	test.EqOp(t, "root-child", child.Global["child"].AsString())

	inherited, err := ConfigFor(t.Context(), repo, "child/no-atte", attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.EqOp(t, "root-child", inherited.Global["child"].AsString())

	deep, err := ConfigFor(t.Context(), repo, "child/deeper", attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.EqOp(t, "root-child-deep", deep.Global["deep"].AsString())

	targets, err := Targets(t.Context(), repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.Len(t, 1, targets)
	_, err = Graph(t.Context(), repo, WithFunctions(attegit.PathHCLFunctions))
	must.NoError(t, err)
}

func TestGlobalEvaluationRejectsUnresolvedDeclarations(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `
globals {
  first = global.second
  second = global.first
}

test { script = path("./root.sh") }
`,
		"root.sh": "#!/bin/sh\n",
	})

	_, err := Targets(t.Context(), repo, nil)
	test.Error(t, err)
	test.ErrorContains(t, err, "global")

	_, err = Graph(t.Context(), repo)
	test.Error(t, err)
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
	_, err := Targets(t.Context(), repo, nil)
	test.Error(t, err)
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
	targets, err := Targets(t.Context(), repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.Len(t, 3, targets)
	graphWithoutContainment, err := Graph(t.Context(), repo, WithFunctions(attegit.PathHCLFunctions))
	must.NoError(t, err)
	graphWithContainment, err := Graph(t.Context(), repo, graphset.WithAttachToTree(), WithFunctions(attegit.PathHCLFunctions))
	must.NoError(t, err)
	for _, target := range targets {
		_, inGraph := graphWithoutContainment.Entities[target.ID]
		test.True(t, inGraph, test.Sprintf("target should be present in graph"))
		_, inContainedGraph := graphWithContainment.Entities[target.ID]
		test.True(t, inContainedGraph, test.Sprintf("target should be present in containment graph"))
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
	_, err := Targets(t.Context(), repo, attegit.PathHCLFunctions)
	must.NoError(t, err)

	duplicate := newHCLFixture(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
			globals { value = "one" }
			globals { value = "two" }
			test { script = path("./one.sh") }
			`, "\t"),
		"one.sh": "#!/bin/sh\n",
	})
	_, err = Targets(t.Context(), duplicate, nil)
	test.Error(t, err)
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
	targets, err := Targets(t.Context(), repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.EqOp(t, 3, len(targets))
	test.EqOp(t, reference.Blob("root.sh"), targets[0].Script)
	test.EqOp(t, reference.Blob("child/child.sh"), targets[1].Script)
	test.EqOp(t, reference.Blob("child/deeper/deep.sh"), targets[2].Script)
	root, err := ConfigFor(t.Context(), repo, "", attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.EqOp(t, "root", root.Global["version"].AsString())
	test.EqOp(t, reference.Blob("root.sh"), *root.Global["shared_path"].EncapsulatedValue().(*reference.Blob))
	child, err := ConfigFor(t.Context(), repo, "child", attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.EqOp(t, "child", child.Global["version"].AsString())
	test.EqOp(t, reference.Blob("child/child.sh"), *child.Global["shared_path"].EncapsulatedValue().(*reference.Blob))
	deep, err := ConfigFor(t.Context(), repo, "child/deeper", attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.EqOp(t, "deep", deep.Global["version"].AsString())
	test.EqOp(t, reference.Blob("child/deeper/deep.sh"), *deep.Global["shared_path"].EncapsulatedValue().(*reference.Blob))
}

func TestEntityIDRoundTrip(t *testing.T) {
	for _, kind := range []string{TestKind, CodegenKind, LintKind} {
		t.Run(kind, func(t *testing.T) {
			id := EntityID(kind, "nested/atte.hcl", "unit")
			gotKind, file, name, err := DecodeEntityID(id)
			must.NoError(t, err)
			test.EqOp(t, kind, gotKind)
			test.EqOp(t, reference.Blob("nested/atte.hcl"), file)
			test.EqOp(t, "unit", name)
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
