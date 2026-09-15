package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/plugin"
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
	builder, err := plugin.NewDefaultBuilder()
	must.NoError(t, err)
	must.NoError(t, plugin.RegisterHCLBlock(builder, "custom", attehcl.TargetKindSpec{
		Decoder: func(*hcl.BodyContent, *hcl.EvalContext) (any, error) {
			return struct{}{}, nil
		},
	}))
	detector := builder.Compile()
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
