package attehcl

import (
	"context"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func experimentRepo(t *testing.T, files map[string]string) *attegit.Repo {
	t.Helper()
	gitRepo := attegittest.NewGitRepo(t)
	gitRepo.WriteFiles(t, files)
	gitRepo.CommitAll(t, "fixture")
	repo, err := attegit.Open(gitRepo.Dir(), "HEAD")
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

func TestDeclaredTargetsAreIndependentOfBodyEvaluation(t *testing.T) {
	repo := experimentRepo(t, map[string]string{
		"atte.hcl": `
test "broken" {
  script = local.missing
  unknown = missing_function()
}

test {}

target "codegen" "named" {
  another_unknown_attribute = another_missing_function()
}
`,
		"child/atte.hcl": `lint "child" {}`,
	})

	got, err := DeclaredTargets(t.Context(), repo)
	must.NoError(t, err)
	test.Len(t, 4, got)
	test.EqOp(t, reference.Blob("atte.hcl"), got[0].File)
	test.EqOp(t, "broken", got[0].Name)
	test.EqOp(t, 0, got[0].Index)
	test.EqOp(t, EntityID(TestKind, "atte.hcl", "broken"), got[0].ID)
	test.EqOp(t, reference.Blob("atte.hcl"), got[1].File)
	test.EqOp(t, "", got[1].Name)
	test.EqOp(t, 1, got[1].Index)
	test.EqOp(t, reference.Blob("child/atte.hcl"), got[3].File)
}

func TestDeclaredTargetsRejectsDeclarationErrors(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{name: "globals", file: `globals { value = "unsupported" }`},
		{name: "duplicate locals", file: `locals { value = "one" }
locals { value = "two" }
test {}`},
		{name: "unknown kind", file: `unknown {}`},
		{name: "numeric name", file: `test "123" {}`},
		{name: "duplicate name", file: `test "same" {}
test "same" {}`},
		{name: "malformed HCL", file: `test {`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := experimentRepo(t, map[string]string{"atte.hcl": tt.file})
			_, err := DeclaredTargets(t.Context(), repo)
			test.Error(t, err)
		})
	}
}

func TestConfigForEvaluatesOnlyRequestedFile(t *testing.T) {
	repo := experimentRepo(t, map[string]string{
		"atte.hcl": `
locals { script = path("./root.sh") }
test { script = local.script }
`,
		"child/atte.hcl": `test { script = local.missing }`,
		"root.sh":        "#!/bin/sh\n",
	})

	config, err := ConfigFor(t.Context(), repo, ".", attegit.PathHCLFunctions)
	must.NoError(t, err)
	test.Len(t, 1, config.Targets[KindTest])
	test.EqOp(t, reference.Blob("root.sh"), config.Targets[KindTest][0].Script)

	_, err = ConfigFor(t.Context(), repo, "child", attegit.PathHCLFunctions)
	test.Error(t, err)
}

func TestGraphResolvesSameFileTraversalsAndEntityIDs(t *testing.T) {
	repo := experimentRepo(t, map[string]string{
		"atte.hcl": `
codegen "generate" { script = path("./generate.sh") }
test "unit" {
  script = path("./test.sh")
  depends_on = [codegen.generate]
}
`,
		"child/atte.hcl": `test "cross" {
  script = path("./test.sh")
  depends_on = ["attehcl-id:attehcl:codegen:atte.hcl:generate"]
}
`,
		"generate.sh":   "#!/bin/sh\n",
		"test.sh":       "#!/bin/sh\n",
		"child/test.sh": "#!/bin/sh\n",
	})

	got, err := Graph(t.Context(), repo, WithFunctions(attegit.PathHCLFunctions))
	must.NoError(t, err)
	generate := EntityID(CodegenKind, "atte.hcl", "generate")
	unit := EntityID(TestKind, "atte.hcl", "unit")
	cross := EntityID(TestKind, "child/atte.hcl", "cross")
	test.True(t, hasRelation(got, unit, generate, DependsOnRelation))
	test.True(t, hasRelation(got, cross, generate, DependsOnRelation))
}

func TestGraphRejectsMissingSameFileTraversal(t *testing.T) {
	repo := experimentRepo(t, map[string]string{
		"atte.hcl": `test "unit" {
  script = "echo"
  depends_on = [codegen.missing]
}`,
	})
	_, err := Graph(t.Context(), repo)
	test.ErrorContains(t, err, "codegen.missing")
}

func TestGraphAllowsCustomKindsWithoutScripts(t *testing.T) {
	schema := hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "command", Required: true}}}
	must.NoError(t, Register("experimental_package", func(*hcl.BodyContent, *hcl.EvalContext) (any, error) {
		return struct{ Command string }{Command: "go test"}, nil
	}, &schema))
	repo := experimentRepo(t, map[string]string{
		"atte.hcl": `target "experimental_package" "unit" { command = "go test ./..." }`,
	})
	got, err := Graph(t.Context(), repo)
	must.NoError(t, err)
	test.True(t, got.Has(EntityID(Namespace+":experimental_package", "atte.hcl", "unit")))
}

func TestDetectorListsScriptlessDeclarations(t *testing.T) {
	repo := experimentRepo(t, map[string]string{"atte.hcl": "test {}"})
	got, err := NewDetector(nil).Targets(t.Context(), repo)
	must.NoError(t, err)
	test.Len(t, 1, got)
	test.EqOp(t, EntityID(TestKind, "atte.hcl", "0"), got[0].ID)
}

func TestDeclaredTargetsHonorsCancellation(t *testing.T) {
	repo := experimentRepo(t, map[string]string{"atte.hcl": "test {}"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := DeclaredTargets(ctx, repo)
	test.ErrorIs(t, err, context.Canceled)
}
