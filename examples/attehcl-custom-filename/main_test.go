package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/ghthor/atte/cmd"
	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/graph/graphtest"
	"github.com/ghthor/atte/detector/scanner"
	"github.com/ghthor/atte/reference"
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
	return newAcceptanceRepositoryWith(t, mockRepositoryFiles)
}

func newAcceptanceRepositoryWith(t *testing.T, files map[string]string) acceptanceRepository {
	t.Helper()

	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, files)
	git.CommitAll(t, "configure build.hcl as the configuration filename")

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

func TestRunListDiscoversConfiguredFilename(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)

	stdout, stderr, err := executeAcceptanceCommand(t, repository, "run", "--list")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.SliceEqOp(t, []string{
		"//atte.hcl#test.all",
		"//atte.hcl#test.setup",
		"//sub#go_test",
		"//sub/build.hcl#test.lint",
		"//sub/build.hcl#test.unit",
	}, strings.Fields(stdout))
}

func TestRunSelectsTargetsInConfiguredFile(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)

	dryRun := func(t *testing.T, workingDirectory, selector, wantSelector string) {
		t.Helper()
		stdout, stderr, err := executeAcceptanceCommandIn(t, repository, workingDirectory, "run", "--dry-run", selector)
		test.NoError(t, err, test.Sprintf("selector %q from %q", selector, workingDirectory))
		test.EqOp(t, "", stderr)
		test.StrContains(t, stdout, "atte run "+wantSelector+"\n")
		test.StrContains(t, stdout, `bash -c "echo unit"`+"\n")
	}

	t.Run("file path", func(t *testing.T) {
		dryRun(t, "", "//sub/build.hcl#test.unit", "//sub/build.hcl#test.unit")
	})
	t.Run("directory path", func(t *testing.T) {
		dryRun(t, "", "//sub#test.unit", "//sub/build.hcl#test.unit")
	})
	t.Run("relative to the directory", func(t *testing.T) {
		dryRun(t, "sub", "build.hcl#test.unit", "//sub/build.hcl#test.unit")
	})
	t.Run("short alias", func(t *testing.T) {
		dryRun(t, "sub", "unit", "//sub/build.hcl#test.unit")
	})
}

func TestGoTestOverrideInConfiguredFile(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)

	stdout, stderr, err := executeAcceptanceCommand(t, repository, "run", "--dry-run", "//sub#go_test")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.StrContains(t, stdout, "go test -count=1\n")
}

func TestTargetFunctionResolvesConfiguredFilename(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)

	graph, err := attehcl.Graph(t.Context(), repository.repo, repository.detector)
	must.NoError(t, err)

	depends := func(fromFile, from, toFile, to string) {
		t.Helper()
		graphtest.MustHaveRelation(
			t,
			graph,
			attehcl.EntityID(attehcl.TestKind, reference.Blob(fromFile), from),
			attehcl.EntityID(attehcl.TestKind, reference.Blob(toFile), to),
			attehcl.DependsOnRelation,
		)
	}

	// target("sub", ...) resolves to the directory's build.hcl.
	depends("atte.hcl", "all", "sub/build.hcl", "unit")
	// target("//", ...) resolves to the root atte.hcl.
	depends("sub/build.hcl", "unit", "atte.hcl", "setup")
	// target("", ...) resolves to the declaring file.
	depends("sub/build.hcl", "lint", "sub/build.hcl", "unit")
}

func TestConfigShowUsesConfiguredFilename(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)

	type settings = map[string]struct {
		File   string         `json:"file"`
		Name   string         `json:"name"`
		Values map[string]any `json:"values"`
	}
	var output struct {
		Target   map[string]any `json:"target"`
		Settings settings       `json:"settings"`
	}

	t.Run("root", func(t *testing.T) {
		stdout, stderr, err := executeAcceptanceCommand(t, repository, "config", "show")
		test.NoError(t, err)
		test.EqOp(t, "", stderr)
		must.NoError(t, json.Unmarshal([]byte(stdout), &output))
		test.MapContainsKeys(t, output.Target, []string{"//atte.hcl#test.all", "//atte.hcl#test.setup"})
		test.EqOp(t, 1, len(output.Settings))
		layout := output.Settings["//atte.hcl#atte"]
		test.EqOp(t, "atte.hcl", layout.File)
		test.EqOp(t, "atte", layout.Name)
		test.Eq(t, map[string]any{"filename": "build.hcl"}, layout.Values)
	})

	t.Run("subdirectory", func(t *testing.T) {
		output.Target, output.Settings = nil, nil
		stdout, stderr, err := executeAcceptanceCommandIn(t, repository, "sub", "config", "show")
		test.NoError(t, err)
		test.EqOp(t, "", stderr)
		must.NoError(t, json.Unmarshal([]byte(stdout), &output))
		test.MapContainsKeys(t, output.Target, []string{"//sub/build.hcl#test.unit", "//sub/build.hcl#test.lint"})
		test.EqOp(t, 1, len(output.Settings))
		goTest := output.Settings["//sub/build.hcl#go_test"]
		test.EqOp(t, "sub/build.hcl", goTest.File)
		test.Eq(t, map[string]any{"command": []any{"go", "test", "-count=1"}}, goTest.Values)
	})
}

func TestMisplacedConfigurationFileIsAnError(t *testing.T) {
	t.Parallel()

	files := maps.Clone(mockRepositoryFiles)
	files["other/atte.hcl"] = `test "stray" { script = "echo stray" }`
	repository := newAcceptanceRepositoryWith(t, files)

	_, _, err := executeAcceptanceCommand(t, repository, "run", "--list")
	test.ErrorContains(t, err, `"other/atte.hcl"`)
	test.ErrorContains(t, err, `"build.hcl"`)
}
