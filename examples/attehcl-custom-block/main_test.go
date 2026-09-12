package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ghthor/atte/cmd"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

type acceptanceRepository struct {
	repo *attegit.Repo
	dir  string
}

func newAcceptanceRepository(t *testing.T) acceptanceRepository {
	t.Helper()

	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
deploy "release" {
  env = "dev"
}
`, "\n"),
	})
	git.CommitAll(t, "add deploy target")

	repo, err := attegit.Open(git.Dir(), "HEAD", attegit.WithWorkingTree())
	must.NoError(t, err)
	return acceptanceRepository{repo: repo, dir: git.Dir()}
}

func executeAcceptanceCommand(t *testing.T, repository acceptanceRepository, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out bytes.Buffer
	var errOut bytes.Buffer
	err = cmd.ExecuteWithOptions(t.Context(), args, cmd.ExecuteOptions{
		Repository:       repository.repo,
		WorkingDirectory: repository.dir,
		Out:              &out,
		Err:              &errOut,
	})
	return out.String(), errOut.String(), err
}

func TestDeployRunDryRun(t *testing.T) {
	repository := newAcceptanceRepository(t)

	stdout, stderr, err := executeAcceptanceCommand(t, repository, "run", "--dry-run", "deploy")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.True(t, strings.Contains(stdout, "deploy"), test.Sprintf("dry-run output should identify the deploy target: %q", stdout))
	test.True(t, strings.Contains(stdout, "echo \"deploy release to dev\""), test.Sprintf("dry-run output should contain the deploy command: %q", stdout))
}

func TestDeployConfigShow(t *testing.T) {
	repository := newAcceptanceRepository(t)

	stdout, stderr, err := executeAcceptanceCommand(t, repository, "config", "show")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.EqOp(t, 1, strings.Count(stdout, "\"kind\""))
	test.True(t, strings.Contains(stdout, "deploy"), test.Sprintf("configuration should contain the deploy kind: %q", stdout))
	test.True(t, strings.Contains(stdout, "release"), test.Sprintf("configuration should contain the deploy name: %q", stdout))
	test.True(t, strings.Contains(stdout, "env"), test.Sprintf("configuration should contain the env attribute: %q", stdout))
	test.True(t, strings.Contains(stdout, "dev"), test.Sprintf("configuration should preserve the env value: %q", stdout))
}
