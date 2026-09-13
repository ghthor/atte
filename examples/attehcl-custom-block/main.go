package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ghthor/atte/cmd"
	"github.com/ghthor/atte/detector/attehcl"

	"github.com/hashicorp/hcl/v2"
)

const KindDeploy attehcl.Kind = "deploy"

func main() {
	err := attehcl.Register(KindDeploy, attehcl.TargetKindSpec{
		Decoder: func(bc *hcl.BodyContent, ec *hcl.EvalContext) (any, error) {
			return nil, nil
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := execute(); err != nil {
		os.Exit(1)
	}
}

func execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cmd.ExecuteContext(ctx)
}
