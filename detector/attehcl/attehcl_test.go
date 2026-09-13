package attehcl

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graph/graphtest"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/hashicorp/hcl/v2"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"github.com/zclconf/go-cty/cty/function"
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

func TestGraphFormatsFunctionDiagnostics(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `
codegen "go" {
  script = "echo"
  depends_on = [gopkg("./cmd/mis")]
}
`,
		"go.mod": "module example.com/root\n",
	})

	_, err := Graph(t.Context(), repo, WithFunctions(attego.HCLFunctions))
	test.ErrorContains(t, err, `decode HCL "atte.hcl": atte.hcl:3,17-23:`)
	test.ErrorContains(t, err, "  3 |   depends_on = [gopkg(\"./cmd/mis\")]\n")
	test.ErrorContains(t, err, "Call to function \"gopkg\" failed")
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

func TestGlobalsAreRejected(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `globals { value = "unsupported" }`,
	})
	_, err := Targets(t.Context(), repo, nil)
	test.ErrorContains(t, err, "globals are not supported")
}

func TestLocalExpressionsAreFileLocal(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `
locals {
  script = path("./root.sh")
}

test { script = local.script }
`,
		"child/atte.hcl": `
test { script = local.script }
`,
		"root.sh": "#!/bin/sh\n",
	})
	_, err := Targets(t.Context(), repo, attegit.PathHCLFunctions)
	test.Error(t, err)
}

func TestTargetRegistrySupportsCustomKindAndWrapperForm(t *testing.T) {
	type packageTarget struct {
		Command string
	}
	schema := hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "command", Required: true}}}
	must.NoError(t, Register("package_test", TargetKindSpec{
		Schema: &schema,
		Decoder: func(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
			value, diagnostics := content.Attributes["command"].Expr.Value(ctx)
			if diagnostics.HasErrors() {
				return nil, fmt.Errorf("command: %s", diagnostics.Error())
			}
			return packageTarget{Command: value.AsString()}, nil
		},
	}))

	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `target "package_test" "go" {
  command = "go test ./..."
}
`,
	})
	config, err := ConfigFor(t.Context(), repo, "", nil)
	must.NoError(t, err)
	test.Len(t, 1, SortedTargets(config.Targets))
	test.EqOp(t, "package_test", strings.TrimPrefix(SortedTargets(config.Targets)[0].Kind, Namespace+":"))
	test.EqOp(t, "go", SortedTargets(config.Targets)[0].Name)
	got, ok := SortedTargets(config.Targets)[0].Decoded.(packageTarget)
	test.True(t, ok, test.Sprintf("custom decoder value should be preserved"))
	test.EqOp(t, "go test ./...", got.Command)
}

func TestTargetKindCapabilitiesAreIndependent(t *testing.T) {
	kind := Kind("non_runnable_capability_test")
	must.NoError(t, Register(kind, TargetKindSpec{
		Schema: &hcl.BodySchema{},
		Decoder: func(*hcl.BodyContent, *hcl.EvalContext) (any, error) {
			return struct{ Value string }{Value: "decoded"}, nil
		},
	}))
	repo := newHCLFixture(t, map[string]string{"atte.hcl": "non_runnable_capability_test \"target\" {}"})
	config, err := ConfigFor(t.Context(), repo, "", nil)
	must.NoError(t, err)
	targets := SortedTargets(config.Targets)
	test.Len(t, 1, targets)
	test.False(t, targets[0].Runnable())
	decoded, ok := targets[0].Decoded.(struct{ Value string })
	test.True(t, ok, test.Sprintf("non-runnable target should preserve its decoded value"))
	test.EqOp(t, "decoded", decoded.Value)
	graph, err := Graph(t.Context(), repo)
	must.NoError(t, err)
	test.EqOp(t, 0, len(graph.Entities))
}

func TestTargetRegistrySnapshotsCapabilities(t *testing.T) {
	kind := Kind("registry_snapshot_test")
	repo := newHCLFixture(t, map[string]string{"atte.hcl": "registry_snapshot_test {}"})
	evaluator, err := newEvaluator(t.Context(), repo, nil)
	must.NoError(t, err)
	must.NoError(t, Register(kind, TargetKindSpec{
		Schema: &hcl.BodySchema{},
		Decoder: func(*hcl.BodyContent, *hcl.EvalContext) (any, error) {
			return struct{}{}, nil
		},
	}))
	_, err = evaluator.evaluatedTargets()
	test.ErrorContains(t, err, "unknown target kind \"registry_snapshot_test\"")
	_, err = Targets(t.Context(), repo, nil)
	test.NoError(t, err)
}

func TestTargetRegistryRegistrationValidation(t *testing.T) {
	decoder := func(*hcl.BodyContent, *hcl.EvalContext) (any, error) {
		return struct{}{}, nil
	}
	test.Error(t, Register("test", TargetKindSpec{Decoder: decoder}))
	test.Error(t, Register("bad name", TargetKindSpec{Decoder: decoder}))
	test.Error(t, Register("valid_registration", TargetKindSpec{}))
}

func TestTargetRegistryCopiesSchema(t *testing.T) {
	schema := hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "command", Required: true}}}
	kind := "schema_copy_test"
	must.NoError(t, Register(kind, TargetKindSpec{
		Schema: &schema,
		Decoder: func(content *hcl.BodyContent, _ *hcl.EvalContext) (any, error) {
			return content.Attributes["command"].Name, nil
		},
	}))
	schema.Attributes[0].Name = "changed"

	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `schema_copy_test { command = "kept" }`,
	})
	config, err := ConfigFor(t.Context(), repo, "", nil)
	must.NoError(t, err)
	test.EqOp(t, "command", SortedTargets(config.Targets)[0].Decoded)
}

func TestTargetRegistryRejectsInvalidTargetNames(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `test "0" { script = "echo" }`,
	})
	_, err := Targets(t.Context(), repo, nil)
	test.ErrorContains(t, err, "must not be numeric")
}

func TestTargetRegistrySupportsShortAndWrapperForms(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `
test "short" { script = "echo short" }
target "test" "wrapper" { script = "echo wrapper" }
`,
	})
	got, err := ConfigFor(t.Context(), repo, "", nil)
	must.NoError(t, err)
	test.Len(t, 2, SortedTargets(got.Targets))
	test.EqOp(t, "short", SortedTargets(got.Targets)[0].Name)
	test.EqOp(t, "wrapper", SortedTargets(got.Targets)[1].Name)
	test.EqOp(t, 0, SortedTargets(got.Targets)[0].Index)
	test.EqOp(t, 1, SortedTargets(got.Targets)[1].Index)
}

func TestTargetRegistryRejectsUnknownAndMalformedBlocks(t *testing.T) {
	reject := func(file, want string) {
		t.Helper()
		repo := newHCLFixture(t, map[string]string{"atte.hcl": file})
		_, err := Targets(t.Context(), repo, nil)
		test.ErrorContains(t, err, want)
	}

	reject(`package { script = "echo" }`, `unknown target kind "package"`)
	reject(`target { script = "echo" }`, "target block must have one or two labels")
	reject(`target "test" "one" "two" { script = "echo" }`, "target block must have one or two labels")
	reject(`test "one" "two" { script = "echo" }`, "target test has too many labels")
}

func TestTargetEvaluationPreservesTraversalsAndProjectsConsistently(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `test {
  script = path("./test.sh")
  depends_on = [codegen.generate]
}

target "codegen" "generate" {
  script = path("./codegen.sh")
}

lint {
  script = path("./lint.sh")
}
`,
		"test.sh":    "#!/bin/sh\n",
		"codegen.sh": "#!/bin/sh\n",
		"lint.sh":    "#!/bin/sh\n",
	})
	grouped, err := Targets(t.Context(), repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	targets := SortedTargets(grouped)
	test.Len(t, 3, targets)
	test.EqOp(t, "", grouped[KindTest][0].Name)
	test.EqOp(t, 0, grouped[KindTest][0].Index)
	test.EqOp(t, "generate", grouped[KindCodegen][0].Name)
	test.EqOp(t, 0, grouped[KindCodegen][0].Index)
	test.EqOp(t, "", grouped[KindLint][0].Name)
	test.EqOp(t, 0, grouped[KindLint][0].Index)
	decoded := grouped[KindTest][0].Decoded.(decodedTarget)
	must.Len(t, 1, decoded.Deps)
	test.Len(t, 2, decoded.Deps[0].traversal)

	config, err := ConfigFor(t.Context(), repo, "", attegit.PathHCLFunctions)
	must.NoError(t, err)
	configTargets := SortedTargets(config.Targets)
	test.Len(t, len(targets), configTargets)
	for index := range targets {
		test.EqOp(t, targets[index].ID, configTargets[index].ID)
		test.EqOp(t, targets[index].Kind, configTargets[index].Kind)
		test.EqOp(t, targets[index].Name, configTargets[index].Name)
		test.EqOp(t, targets[index].Index, configTargets[index].Index)
	}

	graph, err := Graph(t.Context(), repo, WithFunctions(attegit.PathHCLFunctions))
	must.NoError(t, err)
	graphtest.MustHaveRelation(t, graph, grouped[KindTest][0].ID, grouped[KindCodegen][0].ID, DependsOnRelation)
	for _, target := range targets {
		test.EqOp(t, target.Kind, graph.Entities[target.ID].Kind)
		canonical := Selector(target).String()
		test.EqOp(t, canonical, selector.Render(target.File.String(), strings.TrimPrefix(target.Kind, Namespace+":")+"."+target.DisplayName()))
		test.True(t, Selector(target).Matches(canonical, ""), test.Sprintf("canonical selector should match %s", canonical))
		test.True(t, contains(target.Aliases, target.DisplayName()), test.Sprintf("display name should be an alias for %s", canonical))
	}
}

func TestTargetEvaluationProviderIsFileLocal(t *testing.T) {
	calls := make([]reference.Blob, 0, 1)
	provider := func(_ context.Context, _ *attegit.Repo, file reference.Blob) (map[string]function.Function, error) {
		calls = append(calls, file)
		return nil, nil
	}
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl":         `test { script = "root" }`,
		"child/atte.hcl":   `test { script = "child" }`,
		"sibling/atte.hcl": `test { script = "sibling" }`,
	})
	_, err := ConfigFor(t.Context(), repo, "child", provider)
	must.NoError(t, err)
	test.Len(t, 1, calls)
	test.EqOp(t, reference.Blob("child/atte.hcl"), calls[0])
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

func TestDeclaredTargetsDoesNotEvaluateBodies(t *testing.T) {
	calls := 0
	repo := newHCLFixture(t, map[string]string{
		"z/atte.hcl": `test {
  script = missing_function(local.missing)
  unknown = missing_function()
  depends_on = [local.missing]
}
`,
		"atte.hcl": `codegen {}
`,
		"z/script.sh": "#!/bin/sh\n",
	})
	provider := func(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error) {
		calls++
		return nil, nil
	}

	declarations, err := DeclaredTargets(t.Context(), repo)
	must.NoError(t, err)
	test.Len(t, 2, declarations)
	test.EqOp(t, "atte.hcl", declarations[0].Path)
	test.EqOp(t, Namespace+":"+string(KindCodegen), declarations[0].Kind)
	test.EqOp(t, "0", declarations[0].Name)
	test.EqOp(t, 0, declarations[0].Index)
	test.EqOp(t, EntityID(CodegenKind, "atte.hcl", "0"), declarations[0].ID)
	test.EqOp(t, "z/atte.hcl", declarations[1].Path)
	test.EqOp(t, Namespace+":"+string(KindTest), declarations[1].Kind)
	test.EqOp(t, "0", declarations[1].Name)
	test.EqOp(t, 0, declarations[1].Index)

	detectorTargets, err := NewDetector(provider).Targets(t.Context(), repo)
	must.NoError(t, err)
	test.Len(t, 2, detectorTargets)
	test.EqOp(t, 0, calls)
}

func TestDeclaredTargetsRejectsDeclarationErrors(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{name: "malformed HCL", file: `test {`},
		{name: "globals", file: `globals { value = "unsupported" }`},
		{name: "unknown kind", file: `package {}`},
		{name: "numeric name", file: `test "123" {}`},
		{name: "duplicate name", file: `test "same" {}
test "same" {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newHCLFixture(t, map[string]string{"atte.hcl": tt.file})
			_, err := DeclaredTargets(t.Context(), repo)
			test.Error(t, err)
		})
	}
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
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
