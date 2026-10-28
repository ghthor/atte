package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ghthor/atte/cmd"
	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/scanner"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

type acceptanceRepository struct {
	repo     *attegit.Repo
	dir      string
	detector detector.Scanner
}

func newAcceptanceRepository(t *testing.T) acceptanceRepository {
	t.Helper()

	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, mockRepositoryFiles)
	git.CommitAll(t, "add go_test overrides")

	repo, err := attegit.Open(git.Dir(), "HEAD", attegit.WithWorkingTree())
	must.NoError(t, err)
	builder, err := scanner.NewDefault()
	must.NoError(t, err)
	scanner, err := builder.Compile()
	must.NoError(t, err)
	return acceptanceRepository{repo: repo, dir: git.Dir(), detector: scanner}
}

func executeAcceptanceCommand(t *testing.T, repository acceptanceRepository, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return executeAcceptanceCommandIn(t, repository, "", args...)
}

// executeAcceptanceCommandIn runs args with workingDirectory as the
// repository-relative working directory.
func executeAcceptanceCommandIn(t *testing.T, repository acceptanceRepository, workingDirectory string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out bytes.Buffer
	var errOut bytes.Buffer
	err = cmd.ExecuteWithOptions(t.Context(), args, cmd.ExecuteOptions{
		Repository:       repository.repo,
		RepositoryRoot:   repository.dir,
		WorkingDirectory: workingDirectory,
		Detector:         repository.detector,
		Out:              &out,
		Err:              &errOut,
	})
	return out.String(), errOut.String(), err
}

func TestGoTestRunDryRunUsesNearestOverride(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)

	dryRun := func(t *testing.T, selector, wantCommand string) {
		t.Helper()
		stdout, stderr, err := executeAcceptanceCommand(t, repository, "run", "--dry-run", selector)
		test.NoError(t, err)
		test.EqOp(t, "", stderr)
		test.StrContains(t, stdout, "attr run "+selector+"\n")
		test.StrContains(t, stdout, wantCommand+"\n")
	}
	t.Run("module override", func(t *testing.T) {
		dryRun(t, "//api#go_test", "go test -count=1 -tags example")
	})
	t.Run("package override", func(t *testing.T) {
		dryRun(t, "//worker#go_test", "go test -race -run TestWorker")
	})
	t.Run("other module default", func(t *testing.T) {
		dryRun(t, "//nested/tool#go_test", "go test -v")
	})
}

func TestGoTestConfigShow(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)

	stdout, stderr, err := executeAcceptanceCommand(t, repository, "config", "show")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)

	var output struct {
		Target   map[string]any `json:"target"`
		Settings map[string]struct {
			File   string              `json:"file"`
			Name   string              `json:"name"`
			Values map[string][]string `json:"values"`
		} `json:"settings"`
	}
	must.NoError(t, json.Unmarshal([]byte(stdout), &output))
	test.EqOp(t, 0, len(output.Target))
	test.EqOp(t, 1, len(output.Settings))
	goTest := output.Settings["//atte.hcl#go_test"]
	test.EqOp(t, "atte.hcl", goTest.File)
	test.EqOp(t, "go_test", goTest.Name)
	test.SliceEqOp(t, []string{"go", "test", "-count=1", "-tags", "example"}, goTest.Values["command"])
}

func TestGoTestConfigShowInPackageDirectory(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)

	stdout, stderr, err := executeAcceptanceCommandIn(t, repository, "worker", "config", "show", "--format", "hcl")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.StrContains(t, stdout, `"//worker/atte.hcl#go_test"`)
	test.True(
		t,
		strings.Contains(stdout, `command = ["go", "test", "-race", "-run", "TestWorker"]`),
		test.Sprintf("hcl output should render the worker override: %q", stdout),
	)
}
