package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "atte",
	Short: "Compute the minimum work required after a software change",
	Long: `Atte is a change attenuation engine for software Universes.

It conservatively propagates change through the dependency graph to compute
an affected Cone, then refines Candidates to remove false positives and
produce the smallest provably correct Work Set.

The Work Set can be consumed by build systems, test runners, package managers,
deployment systems, and CI pipelines.`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// ExecuteContext runs the atte command with ctx and exits with status 1 if it fails.
func ExecuteContext(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

// ExecuteOptions configures one command execution.
type ExecuteOptions struct {
	// Repository overrides repository discovery for this command execution.
	Repository *attegit.Repo
	// WorkingDirectory is the repository-relative command working directory.
	WorkingDirectory string
	// In, Out, and Err override the Cobra command streams.
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type executionContextKey struct{}

type executionContext struct {
	repository       *attegit.Repo
	root             string
	workingDirectory string
	relative         string
}

// ExecuteWithOptions runs the atte command with args and the supplied execution options.
// It is the embeddable command entry point used by hosts and acceptance tests.
func ExecuteWithOptions(ctx context.Context, args []string, options ExecuteOptions) error {
	if options.Repository != nil {
		workingDirectory := options.WorkingDirectory
		if workingDirectory == "" {
			var err error
			workingDirectory, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
		}
		ctx = context.WithValue(ctx, executionContextKey{}, executionContext{
			repository:       options.Repository,
			root:             workingDirectory,
			workingDirectory: workingDirectory,
		})
	}

	rootCmd.SetArgs(args)
	if options.In == nil {
		rootCmd.SetIn(os.Stdin)
	} else {
		rootCmd.SetIn(options.In)
	}
	if options.Out == nil {
		rootCmd.SetOut(os.Stdout)
	} else {
		rootCmd.SetOut(options.Out)
	}
	if options.Err == nil {
		rootCmd.SetErr(os.Stderr)
	} else {
		rootCmd.SetErr(options.Err)
	}
	return rootCmd.ExecuteContext(ctx)
}

func commandWorkingDirectory(ctx context.Context) (string, error) {
	if execution, ok := executionFromContext(ctx); ok {
		return execution.workingDirectory, nil
	}
	return os.Getwd()
}

func executionFromContext(ctx context.Context) (executionContext, bool) {
	execution, ok := ctx.Value(executionContextKey{}).(executionContext)
	return execution, ok
}

func openRepository(ctx context.Context, root, ref string, options ...attegit.OpenOption) (*attegit.Repo, error) {
	if execution, ok := executionFromContext(ctx); ok && execution.repository != nil {
		return execution.repository, nil
	}
	return attegit.Open(root, ref, options...)
}
