package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ghthor/atte/cmd"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

const KindDeploy attehcl.Kind = "deploy"

var deploySchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "env", Required: true}},
}

type deployTarget struct {
	Env string
}

func init() {
	if err := attehcl.Register(KindDeploy, attehcl.TargetKindSpec{
		Schema:  &deploySchema,
		Decoder: decodeDeployTarget,
		Graph:   graphDeployTarget,
	}); err != nil {
		panic(err)
	}
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

func main() {
	if err := execute(); err != nil {
		log.Fatal(err)
	}
}

func execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cmd.ExecuteContext(ctx)
}
