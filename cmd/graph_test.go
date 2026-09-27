package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/attehcltarget"
	"github.com/ghthor/atte/detector/scanner"
	"github.com/hashicorp/hcl/v2"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestRepositoryContext(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	subdir := filepath.Join(git.Dir(), "nested", "working")
	must.NoError(t, os.MkdirAll(subdir, 0o755))

	root, relative, err := repositoryContext(t.Context(), git.Dir())
	must.NoError(t, err)
	test.EqOp(t, git.Dir(), root)
	test.EqOp(t, "", relative)

	root, relative, err = repositoryContext(t.Context(), subdir)
	must.NoError(t, err)
	test.EqOp(t, git.Dir(), root)
	test.EqOp(t, "nested/working", relative)
}

func TestRepositoryContextRejectsNonRepository(t *testing.T) {
	_, _, err := repositoryContext(t.Context(), t.TempDir())
	test.ErrorContains(t, err, "resolve repository root")
}

func TestPrintGraphGolden(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"README.md": "read me",
		"atte.hcl": `
			test "default" {
				script = path("./test.sh")
			}

			codegen "go" {
				script = path("./test.sh")
				depends_on = [gopkg("./cmd")]
			}
		`,
		"test.sh": "#!/bin/sh\n",
		"pkg/pkg.go": `
			package pkg
		`,
		"pkg/pkg_test.go": `
			package pkg

			import "testing"

			func TestPackage(t *testing.T) {}
		`,
		"go.mod": `
			module example.com/root

			go 1.20
		`,
		"main.go": `
			package main

			import "example.com/root/cmd"

			var _ = cmd.Run
		`,
		"cmd/cmd.go": `
			package cmd

			import "example.com/root/internal/value"

			var Run = value.Value
		`,
		"internal/value/value.go": `
			package value

			const Value = 1
		`,
	})
	assertGraphGolden(t, "default", renderTestGraph(t, repo))
	assertGraphGolden(t, "run-targets", renderTestGraph(t, repo, PrintGraphOptions{IncludeRunTargets: true}))
}

func TestPrintGraphImportsGolden(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"go.mod": `
			module example.com/root

			go 1.20
		`,
		"main.go": `
			package main

			import (
				"fmt"
				"example.com/root/internal/value"
				"github.com/shoenig/test/must"
			)

			var _ = fmt.Println
			var _ = value.Value
			var _ = must.NoError
		`,
		"main_test.go": `
			package main_test

			import "testing"

			func TestMain(t *testing.T) {}
		`,
		"internal/value/value.go": `
			package value

			const Value = 1
		`,
	})
	assertGraphGolden(t, "imports", renderTestGraph(t, repo, PrintGraphOptions{IncludeExternalImports: true}))
}

func assertGraphGolden(t *testing.T, name, got string) {
	t.Helper()
	goldenPath := filepath.Join(graphTestdataDir(), name+".golden")
	must.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
	got = ansi.Strip(got)
	if os.Getenv("ATTE_CODEGEN") != "" {
		must.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(goldenPath)
	must.NoError(t, err)
	test.EqOp(t, string(want), got)
}

func graphTestdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "testdata", "graph")
}

func TestPrintGraphUsesGoPerspective(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"go.mod": `module example.com/root

go 1.20
`,
		"main.go": `package main

import "fmt"

func main() { fmt.Println("hello") }
`,
	})
	got := renderTestGraph(t, repo)
	test.StrContains(t, got, "go package")
	test.StrContains(t, got, "go.mod")
	test.StrContains(t, got, "main.go")
}

func TestPrintGraphFallsBackToGitPerspective(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"root.txt": "root",
	})
	got := renderTestGraph(t, repo)
	test.StrContains(t, got, "root.txt")
	test.StrNotContains(t, got, "attego:")
}

func TestPrintGraphReportsHCLFunctionErrorGolden(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
codegen "go" {
  script = "echo"
  depends_on = [gopkg("./cmd/mis")]
}
`, "\n"),
		"go.mod": "module example.com/root\n",
	})
	var output bytes.Buffer
	err := printGraph(t.Context(), &output, repo, "")
	test.Error(t, err)
	assertGraphErrorGolden(t, "hcl-function-error", err.Error())
}

func assertGraphErrorGolden(t *testing.T, name, got string) {
	t.Helper()
	goldenPath := filepath.Join(graphTestdataDir(), name+".golden")
	if os.Getenv("ATTE_CODEGEN") != "" {
		must.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(goldenPath)
	must.NoError(t, err)
	test.EqOp(t, string(want), got)
}

func TestPrintGraphReportsDetectorParseError(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"go.mod": `module example.com/root

go 1.20
`,
		"broken.go": `package main
import "unterminated
`,
	})
	var got bytes.Buffer
	err := printGraph(t.Context(), &got, repo, "")
	test.ErrorContains(t, err, "build detector graph")
}

func TestPrintGraphLocalImports(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"go.mod": `module example.com/root

go 1.20
`,
		"main.go": `package main

import "example.com/root/cmd"

var _ = cmd.Run
`,
		"cmd/cmd.go": `package cmd

import (
	"example.com/root/detector"
)

var _ = detector.Value
`,
		"detector/detector.go": `package detector

var Value = 1
`,
	})
	got := renderTestGraph(t, repo)
	test.StrContains(t, got, "import example.com/root/detector")
	test.StrNotContains(t, got, "attego:")
}

func TestPrintGraphIncludesCustomHCLTargets(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"atte.hcl": "deploy \"release\" {}",
	})
	builder, err := scanner.NewDefault()
	must.NoError(t, err)
	must.NoError(t, detector.AttachHCLTargetBlock(builder, "deploy", attehcltarget.KindSpec{
		Decoder: func(*hcl.BodyContent, *hcl.EvalContext) (any, error) {
			return struct{}{}, nil
		},
		Graph: func(
			_ context.Context,
			_ *attegit.Repo,
			target attehcltarget.Target,
			_ attehcltarget.GraphContext,
			attachToTree bool,
		) (attehcltarget.Graph, error) {
			return target.GraphProjectionBase(attachToTree), nil
		},
	}))

	detector := compileTestDetector(t, builder)
	got := renderTestGraphWithDetector(t, repo, detector)
	test.StrContains(t, got, "deploy attehcl:deploy:atte.hcl:release")
}

func TestPrintGraphTargetDependencies(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"atte.hcl": `
codegen "go" {
  script = path("./codegen.sh")
  depends_on = [gopkg("./pkg")]
}
`,
		"codegen.sh": "#!/bin/sh\n",
		"go.mod": `module example.com/root

go 1.20
`,
		"pkg/pkg.go": `package pkg

import "example.com/root/dep"

var _ = dep.Value
`,
		"dep/dep.go": `package dep

const Value = 1
`,
	})

	got := ansi.Strip(renderTestGraph(t, repo))
	test.StrContains(t, got, "codegen attehcl:codegen:atte.hcl:go")
	test.StrContains(t, got, "go package example.com/root/pkg")
	test.StrContains(t, got, "import example.com/root/dep")
	test.StrNotContains(t, got, "depends on")
}

func TestPrintGraphExternalImports(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"go.mod": `module example.com/root

go 1.20
`,
		"main.go": `package main

import (
	"fmt"

	"github.com/shoenig/test/must"
)

func main() { fmt.Println(must.NoError) }
`,
	})
	without := renderTestGraph(t, repo)
	test.StrNotContains(t, without, "external import fmt")

	with := renderTestGraph(t, repo, PrintGraphOptions{IncludeExternalImports: true})
	test.StrContains(t, with, "go package example.com/root")
	test.StrContains(t, with, "std import fmt")
	test.StrNotContains(t, with, "external import fmt")
}

func TestIsGraphChild(t *testing.T) {
	childKind := func(kind string, want bool) {
		t.Helper()
		test.EqOp(t, want, isGraphChild(kind))
	}

	childKind(attego.PackageKind, true)
	childKind(attego.PackageTestKind, true)
	childKind(attehcl.TestKind, true)
	childKind(attehcl.CodegenKind, true)
	childKind(attehcl.LintKind, true)
	childKind("attehcl:deploy", true)
	childKind(attegit.TreeKind, false)
	childKind(attegit.BlobKind, false)
	childKind("unknown", false)
}

func TestIsRunTarget(t *testing.T) {
	tests := []struct {
		name              string
		includeRunTargets bool
		targetSelector    string
		want              bool
	}{
		{
			name:              "included discovered target",
			includeRunTargets: true,
			targetSelector:    "//custom#deploy.release",
			want:              true,
		},
		{
			name:              "run targets disabled",
			includeRunTargets: false,
			targetSelector:    "//custom#deploy.release",
			want:              false,
		},
		{
			name:              "missing selector",
			includeRunTargets: true,
			want:              false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			test.EqOp(t, tt.want, isRunTarget(tt.includeRunTargets, tt.targetSelector))
		})
	}
}

func repoWithFiles(t *testing.T, files map[string]string) *attegit.Repo {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, files)
	git.CommitAll(t, "init")
	return openTestRepo(t, git.Dir())
}

func openTestRepo(t *testing.T, dir string) *attegit.Repo {
	t.Helper()
	repo, err := attegit.Open(dir, "HEAD")
	must.NoError(t, err)
	return repo
}

func renderTestGraph(t *testing.T, repo *attegit.Repo, options ...PrintGraphOptions) string {
	t.Helper()
	var output bytes.Buffer
	must.NoError(t, printGraph(t.Context(), &output, repo, "", options...))
	return output.String()
}

func renderTestGraphWithDetector(t *testing.T, repo *attegit.Repo, scanner detector.Scanner, options ...PrintGraphOptions) string {
	t.Helper()
	ctx := context.WithValue(t.Context(), executionContextKey{}, executionContext{scanner: scanner})
	var output bytes.Buffer
	must.NoError(t, printGraph(ctx, &output, repo, "", options...))
	return output.String()
}
