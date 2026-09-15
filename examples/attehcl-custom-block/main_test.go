package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ghthor/atte/cmd"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/plugin"
	"github.com/ghthor/atte/reference"
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
		Repository:     repository.repo,
		RepositoryRoot: repository.dir,
		Out:            &out,
		Err:            &errOut,
	})
	return out.String(), errOut.String(), err
}

func TestDeployRunDryRun(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)

	stdout, stderr, err := executeAcceptanceCommand(t, repository, "run", "--dry-run", "deploy")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)
	test.True(t, strings.Contains(stdout, "deploy"), test.Sprintf("dry-run output should identify the deploy target: %q", stdout))
	test.True(t, strings.Contains(stdout, "echo \"deploy release to dev\""), test.Sprintf("dry-run output should contain the deploy command: %q", stdout))
}

func TestDeployTargetIdentityAndGraph(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)
	builtIns, err := plugin.NewBuiltIn()
	must.NoError(t, err)

	config, err := attehcl.ConfigFor(t.Context(), repository.repo, "", builtIns.FunctionProvider())
	test.NoError(t, err)
	if err != nil {
		return
	}
	targets := attehcl.SortedTargets(config.Targets)
	test.Len(t, 1, targets)
	if len(targets) == 0 {
		return
	}
	target := targets[0]
	test.EqOp(t, "attehcl:deploy", target.Kind)
	test.EqOp(t, "release", target.Name)
	test.EqOp(t, "//atte.hcl#deploy.release", attehcl.Selector(target).String())
	decoded, ok := target.Decoded.(deployTarget)
	test.True(t, ok, test.Sprintf("decoded deploy target should use deployTarget: %#v", target.Decoded))
	if !ok {
		return
	}
	test.EqOp(t, "dev", decoded.Env)

	graph, err := attehcl.Graph(t.Context(), repository.repo, graphset.WithAttachToTree(), attehcl.WithFunctions(builtIns.FunctionProvider()))
	test.NoError(t, err)
	if err != nil {
		return
	}
	targetID := attehcl.EntityID("attehcl:deploy", reference.Blob("atte.hcl"), "release")
	test.True(t, graph.Has(targetID), test.Sprintf("graph should contain deploy target %q", targetID))
	test.EqOp(t, attegit.BlobKind, graph.Entities[attegit.EntityID(reference.Blob("atte.hcl"))].Kind)
	out := graph.Out(targetID)
	test.Len(t, 1, out)
	if len(out) == 0 {
		return
	}
	test.EqOp(t, attegit.EntityID(reference.Blob("atte.hcl")), out[0].To)
	test.EqOp(t, attehcl.SourceFileRelation, out[0].Kind)
}

func TestDeployConfigShow(t *testing.T) {
	t.Parallel()

	repository := newAcceptanceRepository(t)

	stdout, stderr, err := executeAcceptanceCommand(t, repository, "config", "show")
	test.NoError(t, err)
	test.EqOp(t, "", stderr)

	var output struct {
		Target map[string]struct {
			Kind string            `json:"kind"`
			File string            `json:"file"`
			Name string            `json:"name"`
			Meta map[string]string `json:"meta"`
		} `json:"target"`
	}
	must.NoError(t, json.Unmarshal([]byte(stdout), &output))
	test.EqOp(t, 1, len(output.Target))
	target := output.Target["//atte.hcl#deploy.release"]
	test.EqOp(t, "attehcl:deploy", target.Kind)
	test.EqOp(t, "atte.hcl", target.File)
	test.EqOp(t, "release", target.Name)
	test.EqOp(t, "dev", target.Meta["env"])
}
