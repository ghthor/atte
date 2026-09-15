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
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

func attachTestTarget[K ~string](kind K, spec TargetKindSpec) error {
	name := string(kind)
	if name == "" {
		return fmt.Errorf("target kind is empty")
	}
	if !hclsyntax.ValidIdentifier(name) {
		return fmt.Errorf("target kind %q is not a valid HCL identifier", kind)
	}
	if spec.Decoder == nil {
		return fmt.Errorf("target kind %q has no decoder", kind)
	}
	key := Kind(kind)
	if _, exists := defaultTargetKinds[key]; exists {
		return fmt.Errorf("target kind %q is already attached", kind)
	}
	if spec.Schema != nil {
		schema := copyBodySchema(*spec.Schema)
		spec.Schema = &schema
	}
	defaultTargetKinds[key] = spec
	return nil
}

type testCapabilities struct {
	functions func(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)
	decodeID  func(graph.EntityID) (graph.Entity, error)
}

func (p testCapabilities) TargetKinds() map[Kind]TargetKindSpec { return defaultTargetKinds }
func (p testCapabilities) HCLFunctions(ctx context.Context, repo *attegit.Repo, file reference.Blob) (map[string]function.Function, error) {
	if p.functions == nil {
		return nil, nil
	}
	return p.functions(ctx, repo, file)
}

func (p testCapabilities) DecodeID(id graph.EntityID) (graph.Entity, error) {
	if p.decodeID != nil {
		return p.decodeID(id)
	}
	kind, _, _, err := DecodeEntityID(id, p)
	if err != nil {
		return graph.Entity{}, err
	}
	return graph.Entity{ID: id, Kind: graph.EntityKind(kind)}, nil
}

func testTargets(
	ctx context.Context,
	repo *attegit.Repo,
	functions func(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error),
) (map[Kind][]Target, error) {
	return Targets(ctx, repo, testCapabilities{functions: functions})
}

func testConfigFor(
	ctx context.Context,
	repo *attegit.Repo,
	relativePath string,
	functions func(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error),
) (Config, error) {
	return ConfigFor(ctx, repo, relativePath, testCapabilities{functions: functions})
}

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

func TestBaseHCLFunctions(t *testing.T) {
	functions := baseHCLFunctions(reference.Blob("nested/atte.hcl"))
	for _, name := range []string{
		"abs", "abspath", "base64decode", "base64encode", "basename", "bcrypt",
		"can", "ceil", "chomp", "chunklist", "cidrhost", "cidrnetmask", "cidrsubnet",
		"cidrsubnets", "coalesce", "coalescelist", "compact", "concat", "contains",
		"convert", "csvdecode", "dirname", "distinct", "element", "file", "filebase64",
		"fileexists", "fileset", "flatten", "floor", "format", "formatdate", "formatlist",
		"indent", "index", "join", "jsondecode", "jsonencode", "keys", "length", "log",
		"lookup", "lower", "max", "md5", "merge", "min", "parseint", "pathexpand", "pow",
		"range", "replace", "regex_replace", "reverse", "rsadecrypt", "setintersection",
		"setproduct", "setunion", "sha1", "sha256", "sha512", "signum", "slice", "sort",
		"split", "strlen", "strrev", "substr", "timeadd", "title", "trim", "trimprefix",
		"trimspace", "trimsuffix", "try", "upper", "urlencode", "uuidv4", "uuidv5", "values",
		"yamldecode", "yamlencode", "zipmap",
	} {
		_, ok := functions[name]
		test.True(t, ok, test.Sprintf("base HCL function %q should be attached", name))
	}
}

func TestBaseHCLFunctionsEvaluateByDefault(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `locals {
  script = upper("echo")
}

test { script = local.script }
`,
	})

	grouped, err := testTargets(t.Context(), repo, nil)
	must.NoError(t, err)
	test.EqOp(t, "ECHO", grouped[KindTest][0].Inline)
}

func TestProviderHCLFunctionsHaveAtteNamespaceAliases(t *testing.T) {
	provider := func(ctx context.Context, repo *attegit.Repo, file reference.Blob) (map[string]function.Function, error) {
		path, err := attegit.PathHCLFunction(ctx, repo, file)
		if err != nil {
			return nil, err
		}
		return map[string]function.Function{
			"path": path,
			"gopkg": function.New(&function.Spec{
				Params: []function.Parameter{{Name: "path", Type: cty.String}},
				Type:   function.StaticReturnType(cty.String),
				Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
					return args[0], nil
				},
			}),
		}, nil
	}

	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
test {
  script = atte::path("./test.sh")
  depends_on = [
    atte::gopkg("some/path"),
    gopkg("some/other/path"),
    atte::path("some/file"),
  ]
  triggered_by = [atte::path("some/trigger")]
}
`, "\n"),
		"test.sh": "#!/bin/sh\n",
	})

	grouped, err := testTargets(t.Context(), repo, provider)
	must.NoError(t, err)
	decoded := grouped[KindTest][0].Decoded.(decodedTarget)
	test.EqOp(t, DecodingPathPrefix+"test.sh", decoded.Script)
	must.Len(t, 4, decoded.Deps)
	test.EqOp(t, dependencyPath, decoded.Deps[0].kind)
	test.EqOp(t, "some/path", decoded.Deps[0].path)
	test.EqOp(t, dependencyPath, decoded.Deps[1].kind)
	test.EqOp(t, "some/other/path", decoded.Deps[1].path)
	test.EqOp(t, dependencyPath, decoded.Deps[2].kind)
	test.EqOp(t, "some/file", decoded.Deps[2].path)
	test.EqOp(t, dependencyPath, decoded.Deps[3].kind)
	test.EqOp(t, "some/trigger", decoded.Deps[3].path)
}

func TestCrossFileTargetDependenciesWithRootRelativePath(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"path1/atte.hcl": strings.TrimLeft(`
	test "go" {
	  script = "go test"
	}
`, "\n"),
		"path2/atte.hcl": strings.TrimLeft(`
	locals {
	  dependency = target("//path1", "test.go")
	}

	test "py" {
	  script = "pytest"
	  depends_on = [local.dependency]
	}
`, "\n"),
	})

	got, err := Graph(t.Context(), repo, testCapabilities{})
	must.NoError(t, err)
	graphtest.MustHaveRelation(t, got, EntityID(TestKind, "path2/atte.hcl", "py"), EntityID(TestKind, "path1/atte.hcl", "go"), DependsOnRelation)
}

func TestCrossFileTargetDependencyReachesAttegoEntity(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"go.mod": `module example.com/root

go 1.24
`,
		"path1/p.go": `package p
`,
		"path1/p_test.go": `package p_test
`,
		"path1/atte.hcl": strings.TrimLeft(`
	test "go" {
	  script = "go test"
	  depends_on = [atte::gopkg_test(".")]
	}
`, "\n"),
		"path2/atte.hcl": strings.TrimLeft(`
	test "py" {
	  script = "pytest"
	  depends_on = [atte::target("//path1", "test.go")]
	}
`, "\n"),
	})

	functions, err := attego.HCLFunctions(t.Context(), repo, reference.Blob("path1/atte.hcl"))
	must.NoError(t, err)
	value, err := functions["gopkg_test"].Call([]cty.Value{cty.StringVal(".")})
	must.NoError(t, err)
	goTest := graph.EntityID(value.AsString())

	got, err := Graph(t.Context(), repo, testCapabilities{functions: attego.HCLFunctions, decodeID: (attego.Detector{}).DecodeID})
	must.NoError(t, err)
	graphtest.MustHaveRelation(t, got, EntityID(TestKind, "path1/atte.hcl", "go"), goTest, DependsOnRelation)
	graphtest.MustHaveRelation(t, got, EntityID(TestKind, "path2/atte.hcl", "py"), EntityID(TestKind, "path1/atte.hcl", "go"), DependsOnRelation)
}

func TestCrossFileTargetDependenciesWithRelativePaths(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"path1/atte.hcl": strings.TrimLeft(`
	test "go" {
	  script = "go test"
	  depends_on = [target("../path2", "test.py")]
	}
`, "\n"),
		"path2/atte.hcl": strings.TrimLeft(`
	test "py" {
	  script = "pytest"
	  depends_on = [atte::target("../path1", "test.go")]
	}
`, "\n"),
	})

	got, err := Graph(t.Context(), repo, testCapabilities{})
	must.NoError(t, err)
	graphtest.MustHaveRelation(t, got, EntityID(TestKind, "path1/atte.hcl", "go"), EntityID(TestKind, "path2/atte.hcl", "py"), DependsOnRelation)
	graphtest.MustHaveRelation(t, got, EntityID(TestKind, "path2/atte.hcl", "py"), EntityID(TestKind, "path1/atte.hcl", "go"), DependsOnRelation)
}

func TestCrossFileTargetDependencyRejectsDanglingTarget(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"path1/atte.hcl": `test "go" { script = "go test" }`,
		"path2/atte.hcl": `test "py" {
  script = "pytest"
  depends_on = [atte::target("//path1", "test.missing")]
}`,
	})

	_, err := testTargets(t.Context(), repo, nil)
	test.ErrorContains(t, err, "target test.missing in \"path1/atte.hcl\" was not declared")
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

	got, err := Graph(t.Context(), repo, testCapabilities{functions: attegit.PathHCLFunctions}, graphset.WithAttachToTree())
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

	got, err := Graph(t.Context(), repo, testCapabilities{functions: attegit.PathHCLFunctions}, graphset.WithAttachToTree())
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

	_, err := Graph(t.Context(), repo, testCapabilities{functions: attego.HCLFunctions, decodeID: (attego.Detector{}).DecodeID})
	test.ErrorContains(t, err, `decode HCL "atte.hcl": atte.hcl:3,17-23:`)
	test.ErrorContains(t, err, "  3 |   depends_on = [gopkg(\"./cmd/mis\")]\n")
	test.ErrorContains(t, err, "Call to function \"gopkg\" failed")
}

func TestGraphRejectsNilContext(t *testing.T) {
	var ctx context.Context
	_, err := Graph(ctx, nil, testCapabilities{})
	test.ErrorContains(t, err, "context must not be nil")
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
			file: `test { script = path("../../missing.sh") }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newHCLFixture(t, map[string]string{
				"nested/atte.hcl": tt.file,
			})
			_, err := Graph(t.Context(), repo, testCapabilities{functions: attegit.PathHCLFunctions})
			test.Error(t, err)
		})
	}
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
	_, err := testTargets(t.Context(), repo, attegit.PathHCLFunctions)
	test.Error(t, err)
}

func TestTargetCapabilitiesSupportsCustomKindAndWrapperForm(t *testing.T) {
	type packageTarget struct {
		Command string
	}
	schema := hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "command", Required: true}}}
	must.NoError(t, attachTestTarget("package_test", TargetKindSpec{
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
	config, err := testConfigFor(t.Context(), repo, "", nil)
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
	must.NoError(t, attachTestTarget(kind, TargetKindSpec{
		Schema: &hcl.BodySchema{},
		Decoder: func(*hcl.BodyContent, *hcl.EvalContext) (any, error) {
			return struct{ Value string }{Value: "decoded"}, nil
		},
	}))
	repo := newHCLFixture(t, map[string]string{"atte.hcl": "non_runnable_capability_test \"target\" {}"})
	config, err := testConfigFor(t.Context(), repo, "", nil)
	must.NoError(t, err)
	targets := SortedTargets(config.Targets)
	test.Len(t, 1, targets)
	test.False(t, targets[0].Runnable())
	decoded, ok := targets[0].Decoded.(struct{ Value string })
	test.True(t, ok, test.Sprintf("non-runnable target should preserve its decoded value"))
	test.EqOp(t, "decoded", decoded.Value)
	graph, err := Graph(t.Context(), repo, testCapabilities{})
	must.NoError(t, err)
	test.EqOp(t, 0, len(graph.Entities))
}

func TestTargetCapabilitiesSnapshotsCapabilities(t *testing.T) {
	kind := Kind("registry_snapshot_test")
	repo := newHCLFixture(t, map[string]string{"atte.hcl": "registry_snapshot_test {}"})
	evaluator, err := newEvaluator(t.Context(), repo, nil)
	must.NoError(t, err)
	must.NoError(t, attachTestTarget(kind, TargetKindSpec{
		Schema: &hcl.BodySchema{},
		Decoder: func(*hcl.BodyContent, *hcl.EvalContext) (any, error) {
			return struct{}{}, nil
		},
	}))
	_, err = evaluator.evaluatedTargets()
	test.ErrorContains(t, err, "unknown target kind \"registry_snapshot_test\"")
	_, err = testTargets(t.Context(), repo, nil)
	test.NoError(t, err)
}

func TestTargetCapabilitiesAttachmentValidation(t *testing.T) {
	decoder := func(*hcl.BodyContent, *hcl.EvalContext) (any, error) {
		return struct{}{}, nil
	}
	test.Error(t, attachTestTarget("test", TargetKindSpec{Decoder: decoder}))
	test.Error(t, attachTestTarget("bad name", TargetKindSpec{Decoder: decoder}))
	test.Error(t, attachTestTarget("valid_attachment", TargetKindSpec{}))
}

func TestTargetCapabilitiesCopiesSchema(t *testing.T) {
	schema := hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "command", Required: true}}}
	kind := "schema_copy_test"
	must.NoError(t, attachTestTarget(kind, TargetKindSpec{
		Schema: &schema,
		Decoder: func(content *hcl.BodyContent, _ *hcl.EvalContext) (any, error) {
			return content.Attributes["command"].Name, nil
		},
	}))
	schema.Attributes[0].Name = "changed"

	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `schema_copy_test { command = "kept" }`,
	})
	config, err := testConfigFor(t.Context(), repo, "", nil)
	must.NoError(t, err)
	test.EqOp(t, "command", SortedTargets(config.Targets)[0].Decoded)
}

func TestTargetCapabilitiesRejectsInvalidTargetNames(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `test "0" { script = "echo" }`,
	})
	_, err := testTargets(t.Context(), repo, nil)
	test.ErrorContains(t, err, "must not be numeric")
}

func TestTargetCapabilitiesSupportsShortAndWrapperForms(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `
test "short" { script = "echo short" }
target "test" "wrapper" { script = "echo wrapper" }
`,
	})
	got, err := testConfigFor(t.Context(), repo, "", nil)
	must.NoError(t, err)
	test.Len(t, 2, SortedTargets(got.Targets))
	test.EqOp(t, "short", SortedTargets(got.Targets)[0].Name)
	test.EqOp(t, "wrapper", SortedTargets(got.Targets)[1].Name)
	test.EqOp(t, 0, SortedTargets(got.Targets)[0].Index)
	test.EqOp(t, 1, SortedTargets(got.Targets)[1].Index)
}

func TestTargetCapabilitiesRejectsUnknownAndMalformedBlocks(t *testing.T) {
	reject := func(file, want string) {
		t.Helper()
		repo := newHCLFixture(t, map[string]string{"atte.hcl": file})
		_, err := testTargets(t.Context(), repo, nil)
		test.ErrorContains(t, err, want)
	}

	reject(`package { script = "echo" }`, `unknown target kind "package"`)
	reject(`target { script = "echo" }`, "target block must have one or two labels")
	reject(`target "test" "one" "two" { script = "echo" }`, "target block must have one or two labels")
	reject(`test "one" "two" { script = "echo" }`, "target test has too many labels")
}

func TestTargetDependenciesEvaluateLocalsAndConcat(t *testing.T) {
	const config = `
test "go" {
  script = "go test ./.."
}

test "py" {
  script = "pytest"
}

locals {
  test_group = [
    test.go,
    test.py,
  ]
}

test {
  script = "echo test all"

  depends_on = flatten([
    concat(
      local.test_group,
      [
        path("some/file"),
      ],
    ),
  ])
}
`
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl":  strings.TrimLeft(config, "\n"),
		"some/file": "dependency\n",
	})

	grouped, err := testTargets(t.Context(), repo, attegit.PathHCLFunctions)
	must.NoError(t, err)
	decoded := grouped[KindTest][2].Decoded.(decodedTarget)
	must.Len(t, 3, decoded.Deps)
	for index, name := range []string{"go", "py"} {
		test.EqOp(t, dependencyTarget, decoded.Deps[index].kind)
		test.Len(t, 2, decoded.Deps[index].traversal)
		test.EqOp(t, "test", decoded.Deps[index].traversal.RootName())
		test.EqOp(t, name, decoded.Deps[index].traversal[1].(hcl.TraverseAttr).Name)
	}
	test.EqOp(t, dependencyPath, decoded.Deps[2].kind)
	test.EqOp(t, "some/file", decoded.Deps[2].path)
}

func TestGraphProjectsLocalPathDependency(t *testing.T) {
	const config = `
locals {
  dependency = "./config.yaml"
}

test {
  script = path("./test.sh")
  depends_on = [local.dependency]
}
`
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl":    strings.TrimLeft(config, "\n"),
		"test.sh":     "#!/bin/sh\n",
		"config.yaml": "config\n",
	})

	got, err := Graph(t.Context(), repo, testCapabilities{functions: attegit.PathHCLFunctions})
	must.NoError(t, err)
	testTarget := EntityID(TestKind, "atte.hcl", "0")
	graphtest.MustHaveRelation(t, got, testTarget, attegit.EntityID(testPath("config.yaml")), DependsOnRelation)
}

func TestGraphProjectsEvaluatedLocalDependencies(t *testing.T) {
	const config = `
test "go" {
  script = path("./go.sh")
}

test "py" {
  script = path("./py.sh")
}

locals {
  test_group = [
    test.go,
    test.py,
  ]
}

test {
  script = path("./all.sh")
  depends_on = flatten([
    concat(
      local.test_group,
      [
        path("some/file"),
      ],
    ),
  ])
}
`
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl":  strings.TrimLeft(config, "\n"),
		"go.sh":     "#!/bin/sh\n",
		"py.sh":     "#!/bin/sh\n",
		"all.sh":    "#!/bin/sh\n",
		"some/file": "dependency\n",
	})

	got, err := Graph(t.Context(), repo, testCapabilities{functions: attegit.PathHCLFunctions})
	must.NoError(t, err)

	all := EntityID(TestKind, "atte.hcl", "2")
	graphtest.MustHaveRelation(t, got, all, EntityID(TestKind, "atte.hcl", "go"), DependsOnRelation)
	graphtest.MustHaveRelation(t, got, all, EntityID(TestKind, "atte.hcl", "py"), DependsOnRelation)
	graphtest.MustHaveRelation(t, got, all, attegit.EntityID(testPath("some/file")), DependsOnRelation)
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
	grouped, err := testTargets(t.Context(), repo, attegit.PathHCLFunctions)
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

	config, err := testConfigFor(t.Context(), repo, "", attegit.PathHCLFunctions)
	must.NoError(t, err)
	configTargets := SortedTargets(config.Targets)
	test.Len(t, len(targets), configTargets)
	for index := range targets {
		test.EqOp(t, targets[index].ID, configTargets[index].ID)
		test.EqOp(t, targets[index].Kind, configTargets[index].Kind)
		test.EqOp(t, targets[index].Name, configTargets[index].Name)
		test.EqOp(t, targets[index].Index, configTargets[index].Index)
	}

	graph, err := Graph(t.Context(), repo, testCapabilities{functions: attegit.PathHCLFunctions})
	must.NoError(t, err)
	graphtest.MustHaveRelation(t, graph, grouped[KindTest][0].ID, grouped[KindCodegen][0].ID, DependsOnRelation)
	for _, target := range targets {
		test.EqOp(t, target.Kind, string(graph.Entities[target.ID].Kind))
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
	_, err := testConfigFor(t.Context(), repo, "child", provider)
	must.NoError(t, err)
	test.Len(t, 1, calls)
	test.EqOp(t, reference.Blob("child/atte.hcl"), calls[0])
}

func TestConfigForReturnsOnlyRequestedFile(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl":         `test "root" { script = "root" }`,
		"child/atte.hcl":   `test "child" { script = "child" }`,
		"sibling/atte.hcl": `test "sibling" { script = "sibling" }`,
	})
	config, err := testConfigFor(t.Context(), repo, "child", nil)
	must.NoError(t, err)
	targets := SortedTargets(config.Targets)
	test.Len(t, 1, targets)
	test.EqOp(t, reference.Blob("child/atte.hcl"), targets[0].File)
	test.EqOp(t, "child", targets[0].Name)
}

func TestEntityIDRoundTrip(t *testing.T) {
	for _, kind := range []string{TestKind, CodegenKind, LintKind} {
		t.Run(kind, func(t *testing.T) {
			id := EntityID(kind, "nested/atte.hcl", "unit")
			gotKind, file, name, err := DecodeEntityID(id, testCapabilities{})
			must.NoError(t, err)
			test.EqOp(t, kind, gotKind)
			test.EqOp(t, reference.Blob("nested/atte.hcl"), file)
			test.EqOp(t, "unit", name)
		})
	}
}

func TestDecodeEntityIDRejectsUnknownKind(t *testing.T) {
	id := EntityID(Namespace+":unknown", "nested/atte.hcl", "unit")
	_, _, _, err := DecodeEntityID(id, testCapabilities{})
	test.ErrorContains(t, err, "invalid attehcl entity ID")
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

	declarations, err := DeclaredTargets(t.Context(), repo, testCapabilities{})
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

	detectorTargets, err := NewDetector(testCapabilities{functions: provider}).Targets(t.Context(), repo)
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
		{name: "unknown kind", file: `package {}`},
		{name: "numeric name", file: `test "123" {}`},
		{name: "duplicate name", file: `test "same" {}
test "same" {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newHCLFixture(t, map[string]string{"atte.hcl": tt.file})
			_, err := DeclaredTargets(t.Context(), repo, testCapabilities{})
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
