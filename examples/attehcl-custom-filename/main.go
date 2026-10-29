// Command attehcl-custom-filename demonstrates renaming the per-directory HCL
// configuration file with the atte block in the repository root's atte.hcl.
//
// The root atte.hcl always keeps its name so it can be found without any
// configuration. Its atte block names the file every other directory uses; here
// that is build.hcl.
//
// The mock repository contains:
//
//   - atte.hcl at the root, declaring atte { filename = "build.hcl" } and two
//     test targets.
//   - sub/build.hcl, declaring test targets that reference the root and each
//     other with target(), plus a go_test override for the sub package.
//
// Within a target() call, "" names the declaring file's own directory and
// "//" names the repository root, so the root is always reached as "//" and
// resolves to atte.hcl while every other directory resolves to build.hcl.
//
// Try it from this directory:
//
//	go run . run --list
//	go run . run --dry-run //sub/build.hcl#test.unit
//	go run . run --dry-run //sub#go_test
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

// mockRepositoryFiles is the mock repository's content.
var mockRepositoryFiles = map[string]string{
	"atte.hcl": `
atte {
  filename = "build.hcl"
}

test "setup" {
  script = "echo setup"
}

# A path names a directory; it resolves to that directory's build.hcl.
test "all" {
  script     = "echo all"
  depends_on = [target("sub", "test.unit")]
}
`,
	"sub/build.hcl": `
test "unit" {
  script = "echo unit"

  # "//" is the repository root, whose configuration file is always atte.hcl.
  depends_on = [target("//", "test.setup")]
}

test "lint" {
  script = "echo lint"

  # An empty path is the declaring file's own directory.
  depends_on = [target("", "test.unit")]
}

go_test {
  command = ["go", "test", "-count=1"]
}
`,
	"go.mod":          "module example.com/mock\n\ngo 1.24\n",
	"sub/sub.go":      "package sub\n",
	"sub/sub_test.go": "package sub\n\nimport \"testing\"\n\nfunc TestSub(*testing.T) {}\n",
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
	if err := mock.CommitAll("configure build.hcl as the configuration filename"); err != nil {
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
