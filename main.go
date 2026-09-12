package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ghthor/atte/cmd"
)

func main() {
	if err := execute(); err != nil {
		os.Exit(1)
	}
}

func execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cmd.ExecuteContext(ctx)
}
