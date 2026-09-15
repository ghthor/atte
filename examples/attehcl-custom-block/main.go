package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ghthor/atte/cmd"
	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegitmock"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

const KindDeploy attehcl.Kind = "deploy"

const mockAtteHCL = `

deploy "release" {
  env = "dev"
}

`

var deploySchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "env", Required: true}},
}

type deployTarget struct {
	Env string
}

func registerDeployBlock(builder *detector.Builder) error {
	return detector.RegisterHCLBlock(builder, KindDeploy, attehcl.TargetKindSpec{
		Schema:    &deploySchema,
		Decoder:   decodeDeployTarget,
		Graph:     graphDeployTarget,
		Execution: executeDeployTarget,
		Config:    configDeployTarget,
	})
}

func decodeDeployTarget(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
	attribute := content.Attributes["env"]
	value, diagnostics := attribute.Expr.Value(ctx)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("evaluate env: %s", diagnostics.Error())
	}
	if !value.IsKnown() || value.IsNull() || value.Type() != cty.String {
		return nil, fmt.Errorf("env must be a known string")
	}
	return deployTarget{Env: value.AsString()}, nil
}

func graphDeployTarget(
	ctx context.Context,
	_ *attegit.Repo,
	target attehcl.Target,
	_ attehcl.TargetGraphContext,
	attachToTree bool,
) (attehcl.TargetGraph, error) {
	if err := ctx.Err(); err != nil {
		return attehcl.TargetGraph{}, err
	}
	if _, ok := target.Decoded.(deployTarget); !ok {
		return attehcl.TargetGraph{}, fmt.Errorf("target %q has an invalid deploy decoded value", target.ID)
	}
	return target.GraphProjectionBase(attachToTree), nil
}

func configDeployTarget(target attehcl.Target) (map[string]any, error) {
	decoded, ok := target.Decoded.(deployTarget)
	if !ok {
		return nil, fmt.Errorf("target %q has an invalid deploy decoded value", target.ID)
	}
	return map[string]any{"env": decoded.Env}, nil
}

func executeDeployTarget(target attehcl.Target, root string) (attehcl.TargetCommand, error) {
	decoded, ok := target.Decoded.(deployTarget)
	if !ok {
		return attehcl.TargetCommand{}, fmt.Errorf("target %q has an invalid deploy decoded value", target.ID)
	}
	return attehcl.TargetCommand{
		Dir:  root,
		Args: []string{"echo", fmt.Sprintf("deploy %s to %s", target.Name, decoded.Env)},
	}, nil
}

func main() {
	if err := execute(); err != nil {
		log.Fatal(err)
	}
}

func execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	repository, directory, cleanup, err := newMockRepository(ctx)
	if err != nil {
		return fmt.Errorf("create mock repository: %w", err)
	}
	defer cleanup()

	builtIns, err := detector.NewDefaultBuilder()
	if err != nil {
		return fmt.Errorf("create built-in Scanner: %w", err)
	}
	if err := registerDeployBlock(builtIns); err != nil {
		return fmt.Errorf("register deploy target: %w", err)
	}
	scanner := builtIns.Compile()
	return cmd.ExecuteWithOptions(ctx, os.Args[1:], cmd.ExecuteOptions{
		Repository:     repository,
		RepositoryRoot: directory,
		Detector:       scanner,
	})
}

func newMockRepository(ctx context.Context) (*attegit.Repo, string, func(), error) {
	mock, err := attegitmock.New(ctx)
	if err != nil {
		return nil, "", func() {}, err
	}
	cleanup := func() { _ = mock.Cleanup() }
	if err := mock.WriteFile(attehcl.Filename, []byte(mockAtteHCL), 0o644); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	if err := mock.CommitAll("add deploy target"); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	repository, err := attegit.Open(mock.Dir(), "HEAD", attegit.WithWorkingTree())
	if err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	return repository, mock.Dir(), cleanup, nil
}
