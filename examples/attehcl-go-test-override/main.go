// Command attehcl-go-test-override demonstrates overriding the command that
// runs go_test targets through the go_test setting block in atte.hcl.
//
// The go_test block is provided by the built-in Go Sensor, so nothing is
// registered here: the example only supplies a mock repository whose atte.hcl
// files declare the overrides, then delegates to the normal command system.
//
// The mock repository contains a root module with packages "api" and "worker",
// plus a nested module with package "tool":
//
//   - atte.hcl at the root overrides every go_test target in the root module.
//   - worker/atte.hcl overrides only the worker package.
//   - the nested module has no atte.hcl, so its package keeps the default.
//
// Try it from this directory:
//
//	go run . run --list
//	go run . run --dry-run //api#go_test
//	go run . run --dry-run //worker#go_test
//	go run . run --dry-run //nested/tool#go_test
//	go run . config show
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
	"github.com/ghthor/atte/detector/attegitmock"
	"github.com/ghthor/atte/detector/scanner"
)

// mockRepositoryFiles is the mock repository's content. The go_test blocks are
// ordinary setting blocks evaluated with the declaring file's locals.
var mockRepositoryFiles = map[string]string{
	"atte.hcl": `
locals {
  tags = "example"
}

# Applies to every go_test target in the root module that has no closer block.
go_test {
  command = ["go", "test", "-count=1", "-tags", local.tags]
}
`,
	"worker/atte.hcl": `
# Applies to the worker package only.
go_test {
  command = ["go", "test", "-race", "-run", "TestWorker"]
}
`,
	"go.mod":                   "module example.com/mock\n\ngo 1.24\n",
	"api/api.go":               "package api\n",
	"api/api_test.go":          "package api\n\nimport \"testing\"\n\nfunc TestAPI(*testing.T) {}\n",
	"worker/worker.go":         "package worker\n",
	"worker/worker_test.go":    "package worker\n\nimport \"testing\"\n\nfunc TestWorker(*testing.T) {}\n",
	"nested/go.mod":            "module example.com/nested\n\ngo 1.24\n",
	"nested/tool/tool.go":      "package tool\n",
	"nested/tool/tool_test.go": "package tool\n\nimport \"testing\"\n\nfunc TestTool(*testing.T) {}\n",
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

	builtIns, err := scanner.NewDefault()
	if err != nil {
		return fmt.Errorf("create built-in Scanner: %w", err)
	}
	scanner, err := builtIns.Compile()
	if err != nil {
		return fmt.Errorf("compile detector Scanner: %w", err)
	}
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
	for name, contents := range mockRepositoryFiles {
		if err := mock.WriteFile(name, []byte(contents), 0o644); err != nil {
			cleanup()
			return nil, "", func() {}, err
		}
	}
	if err := mock.CommitAll("add go_test overrides"); err != nil {
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
