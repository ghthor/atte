package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/attehcltarget"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/hashicorp/hcl/v2"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

type executionRepository struct {
	repo *attegit.Repo
	root string
}

func newExecutionRepository(t *testing.T, files map[string]string) executionRepository {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, files)
	git.CommitAll(t, "init")
	repo, err := attegit.Open(git.Dir(), "HEAD")
	must.NoError(t, err)
	return executionRepository{repo: repo, root: git.Dir()}
}

func executeTestCommand(t *testing.T, repository executionRepository, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return executeTestCommandAt(t, repository, "", args...)
}

func executeTestCommandAt(t *testing.T, repository executionRepository, workingDirectory string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out bytes.Buffer
	var errOut bytes.Buffer
	err = ExecuteWithOptions(t.Context(), args, ExecuteOptions{
		Repository:       repository.repo,
		RepositoryRoot:   repository.root,
		WorkingDirectory: workingDirectory,
		Out:              &out,
		Err:              &errOut,
	})
	return out.String(), errOut.String(), err
}

func TestExecuteWithOptionsIsolatesRunFlags(t *testing.T) {
	repository := newExecutionRepository(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
 test "unit" {
   script = "echo unit"
 }
`, "\n"),
	})

	dryRun, stderr, err := executeTestCommand(t, repository, "run", "--dry-run", "test.unit")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.StrContains(t, dryRun, "attr run")

	list, stderr, err := executeTestCommand(t, repository, "run", "--list")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.StrContains(t, list, "test.unit")
	test.StrNotContains(t, list, "attr run")

	list, stderr, err = executeTestCommand(t, repository, "run", "--list")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.StrContains(t, list, "test.unit")

	dryRun, stderr, err = executeTestCommand(t, repository, "run", "--dry-run", "test.unit")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.StrContains(t, dryRun, "attr run")
}

func TestExecuteWithOptionsIsolatesConfigFormat(t *testing.T) {
	repository := newExecutionRepository(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
 test "unit" {
   script = "echo unit"
 }
`, "\n"),
	})

	hcl, stderr, err := executeTestCommand(t, repository, "config", "show", "--format", "hcl")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.StrContains(t, hcl, "target =")

	json, stderr, err := executeTestCommand(t, repository, "config", "show")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.StrHasPrefix(t, "{\n", json)
	test.StrNotContains(t, json, "target =")
}

func TestExecuteWithOptionsUsesInjectedDetectorWithoutRepositoryOverride(t *testing.T) {
	repository := newExecutionRepository(t, map[string]string{
		"atte.hcl": `custom "unit" {}`,
	})
	builder, err := detector.NewDefaultBuilder()
	must.NoError(t, err)
	must.NoError(t, detector.AttachHCLTargetBlock(builder, "custom", attehcltarget.KindSpec{
		Decoder: func(*hcl.BodyContent, *hcl.EvalContext) (any, error) {
			return struct{}{}, nil
		},
	}))
	detector := compileTestDetector(t, builder)
	t.Chdir(repository.root)

	var out bytes.Buffer
	var errOut bytes.Buffer
	err = ExecuteWithOptions(t.Context(), []string{"config", "show"}, ExecuteOptions{
		Detector: detector,
		Out:      &out,
		Err:      &errOut,
	})
	test.NoError(t, err)
	test.EqOp(t, "", errOut.String())
	test.StrContains(t, out.String(), "custom.unit")
}

func TestExecuteWithOptionsIsolatesRepositoriesAndWorkingDirectories(t *testing.T) {
	first := newExecutionRepository(t, map[string]string{
		"child/atte.hcl": strings.TrimLeft(`
 test "first" {
   script = "echo first"
 }
`, "\n"),
	})
	second := newExecutionRepository(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
 test "second" {
   script = "echo second"
 }
`, "\n"),
	})

	firstOutput, stderr, err := executeTestCommandAt(t, first, "child", "config", "show")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.StrContains(t, firstOutput, "test.first")
	test.StrNotContains(t, firstOutput, "test.second")

	secondOutput, stderr, err := executeTestCommand(t, second, "config", "show")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.StrContains(t, secondOutput, "test.second")
	test.StrNotContains(t, secondOutput, "test.first")
}

type concurrentExecutionResult struct {
	stdout string
	stderr string
	err    error
}

func TestExecuteWithOptionsSupportsConcurrentInvocations(t *testing.T) {
	first := newExecutionRepository(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
 test "first" {
   script = "echo first"
 }
`, "\n"),
	})
	second := newExecutionRepository(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
 test "second" {
   script = "echo second"
 }
`, "\n"),
	})

	results := make(chan concurrentExecutionResult, 2)
	for _, repository := range []executionRepository{first, second} {
		go func(repository executionRepository) {
			var out bytes.Buffer
			var errOut bytes.Buffer
			err := ExecuteWithOptions(t.Context(), []string{"run", "--list"}, ExecuteOptions{
				Repository:     repository.repo,
				RepositoryRoot: repository.root,
				Out:            &out,
				Err:            &errOut,
			})
			results <- concurrentExecutionResult{stdout: out.String(), stderr: errOut.String(), err: err}
		}(repository)
	}

	firstResult := <-results
	secondResult := <-results
	for _, result := range []concurrentExecutionResult{firstResult, secondResult} {
		test.NoError(t, result.err)
		test.EqOp(t, "", result.stderr)
		test.True(
			t,
			strings.Contains(result.stdout, "test.first") != strings.Contains(result.stdout, "test.second"),
			test.Sprintf("result should contain exactly one repository's target: %q", result.stdout),
		)
	}
	test.True(
		t,
		firstResult.stdout != secondResult.stdout,
		test.Sprintf("concurrent invocations should use independent repositories: %q and %q", firstResult.stdout, secondResult.stdout),
	)
}

func TestBuiltInTargetsUseScannerSelectorAndExecutionCapabilities(t *testing.T) {
	repository := newExecutionRepository(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
test "unit" {
  script = "echo hcl"
}
`, "\n"),
		"go.mod":              "module example.com/project\n\ngo 1.23\n",
		"sample/main.go":      "package sample\n",
		"sample/main_test.go": "package sample\nimport \"testing\"\nfunc TestSample(t *testing.T) {}\n",
	})
	builder, err := detector.NewDefaultBuilder()
	must.NoError(t, err)
	scanner := compileTestDetector(t, builder)
	targets, err := scanner.Targets(t.Context(), repository.repo)
	must.NoError(t, err)
	test.EqOp(t, 2, len(targets))

	selectors := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		canonical, ok := scanner.TargetString(target)
		test.True(t, ok, test.Sprintf("built-in target should have a canonical selector: %q", target.ID))
		selectors[canonical] = struct{}{}
		execution, err := scanner.ExecuteTarget(t.Context(), repository.repo, repository.root, target)
		test.NoError(t, err)
		test.True(t, len(execution.Args) > 0, test.Sprintf("discovered target should have execution arguments: %q", target.ID))
		switch string(target.Namespace) {
		case "attehcl":
			test.EqOp(t, "//atte.hcl#test.unit", canonical)
			test.True(t, scanner.TargetMatches(target, "unit", ""), test.Sprintf("HCL display name should remain a selector alias"))
		case "attego":
			test.EqOp(t, "//sample#go_test", canonical)
			test.True(t, scanner.TargetMatches(target, "go_test", ""), test.Sprintf("Go test alias should remain supported"))
		default:
			test.True(t, false, test.Sprintf("unexpected target namespace %q", target.Namespace))
		}
	}
	test.EqOp(t, 2, len(selectors))
}

func TestCustomTargetSensorRunsAndRendersThroughScanner(t *testing.T) {
	repository := newExecutionRepository(t, map[string]string{"go.mod": "module example.com/project\n\ngo 1.23\n"})
	builder, err := detector.NewDefaultBuilder()
	must.NoError(t, err)
	must.NoError(t, builder.AttachSensor(customExecutableSensor{}))
	scanner := compileTestDetector(t, builder)

	var dryRun bytes.Buffer
	var stderr bytes.Buffer
	err = ExecuteWithOptions(t.Context(), []string{"run", "--dry-run", "deploy.release"}, ExecuteOptions{
		Repository:     repository.repo,
		RepositoryRoot: repository.root,
		Detector:       scanner,
		Out:            &dryRun,
		Err:            &stderr,
	})
	test.NoError(t, err)
	test.EqOp(t, "", stderr.String())
	test.StrContains(t, dryRun.String(), "//custom#deploy.release")
	test.StrContains(t, dryRun.String(), "echo custom-executed")

	var output bytes.Buffer
	err = ExecuteWithOptions(t.Context(), []string{"run", "deploy.release"}, ExecuteOptions{
		Repository:     repository.repo,
		RepositoryRoot: repository.root,
		Detector:       scanner,
		Out:            &output,
		Err:            &stderr,
	})
	test.NoError(t, err)
	test.EqOp(t, "custom-executed\n", output.String())

	var graphOutput bytes.Buffer
	err = ExecuteWithOptions(t.Context(), []string{"graph", "--run-targets"}, ExecuteOptions{
		Repository:     repository.repo,
		RepositoryRoot: repository.root,
		Detector:       scanner,
		Out:            &graphOutput,
		Err:            &stderr,
	})
	test.NoError(t, err)
	test.StrContains(t, graphOutput.String(), "//custom#deploy.release")
}

type customExecutableSensor struct{}

func (customExecutableSensor) Namespace() string { return "custom" }

func (customExecutableSensor) target() graphtarget.ID {
	return graphtarget.ID{
		ID:        "custom:deploy:release",
		Namespace: "custom",
		Kind:      "custom:deploy",
		Path:      "custom",
		Name:      "release",
		Aliases:   []string{"deploy.release", "release"},
	}
}

func (sensor customExecutableSensor) Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
	return []graphtarget.ID{sensor.target()}, nil
}

func (customExecutableSensor) TargetSelector(target graphtarget.ID) selector.Target {
	return selector.Target{Path: target.Path, Kind: "deploy", Name: target.Name, Aliases: target.Aliases}
}

func (customExecutableSensor) ExecuteTarget(_ context.Context, _ *attegit.Repo, root string, _ graphtarget.ID) (graphtarget.Execution, error) {
	return graphtarget.Execution{Dir: root, Args: []string{"echo", "custom-executed"}}, nil
}

func (sensor customExecutableSensor) Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error) {
	root := attegit.EntityID(reference.Root)
	target := sensor.target()
	return graph.New(
		[]graph.Entity{
			{ID: root, Kind: attegit.TreeKind},
			{ID: target.ID, Kind: graph.EntityKind(target.Kind)},
		},
		[]graph.Relationship{{From: root, To: target.ID, Kind: attegit.ContainsRelation}},
	)
}

var (
	_ detector.TargetSensor = customExecutableSensor{}
	_ detector.GraphSensor  = customExecutableSensor{}
)
