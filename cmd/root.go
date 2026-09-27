package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/spf13/cobra"
)

func newRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "atte",
		Short: "Compute the minimum work required after a software change",
		Long: `Atte is a change attenuation engine for software Universes.

It conservatively propagates change through the dependency graph to compute
an affected Cone, then refines Candidates to remove false positives and
produce the smallest provably correct Work Set.

The Work Set can be consumed by build systems, test runners, package managers,
deployment systems, and CI pipelines.`,
	}
	rootCmd.AddCommand(
		newRunCommand(),
		newConfigCommand(),
		newGraphCommand(),
	)
	return rootCmd
}

// ExecuteContext runs the atte command with ctx using the process's arguments and
// standard input, output, and error streams. It discovers the repository from the
// current working directory. Each call uses an isolated Cobra command tree.
func ExecuteContext(ctx context.Context) error {
	return newRootCommand().ExecuteContext(ctx)
}

// ExecuteOptions configures one isolated command execution. When Repository is
// set, RepositoryRoot must identify the injected repository and WorkingDirectory
// is resolved relative to that root.
type ExecuteOptions struct {
	// Repository overrides repository discovery for this command execution.
	Repository *attegit.Repo
	// RepositoryRoot is the filesystem path to the injected repository root. It
	// is required when Repository is set and may be relative or absolute.
	RepositoryRoot string
	// WorkingDirectory is the repository-relative command working directory. An
	// empty value selects the repository root.
	WorkingDirectory string
	// Detector overrides the built-in Scanner for this command execution.
	Detector detector.Scanner
	// In, Out, and Err override the Cobra command streams. Nil values use the
	// process's standard input, output, and error streams, respectively.
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type executionContextKey struct{}

type executionContext struct {
	repository       *attegit.Repo
	root             string
	workingDirectory string
	scanner          detector.Scanner
	relative         string
}

// ExecuteWithOptions runs the atte command with args and options using an
// isolated Cobra command tree. When a repository is injected, args are evaluated
// against that repository and WorkingDirectory; otherwise normal repository
// discovery is used.
func ExecuteWithOptions(ctx context.Context, args []string, options ExecuteOptions) error {
	if options.Repository != nil {
		execution, err := executionContextForOptions(options)
		if err != nil {
			return err
		}
		ctx = context.WithValue(ctx, executionContextKey{}, execution)
	} else if options.Detector != nil {
		ctx = context.WithValue(ctx, executionContextKey{}, executionContext{scanner: options.Detector})
	}

	rootCmd := newRootCommand()
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

func scannerForContext(ctx context.Context) (detector.Scanner, error) {
	if execution, ok := executionFromContext(ctx); ok && execution.scanner != nil {
		return execution.scanner, nil
	}
	builder, err := detector.NewDefaultBuilder()
	if err != nil {
		return nil, err
	}
	return builder.Compile()
}

func executionContextForOptions(options ExecuteOptions) (executionContext, error) {
	if options.RepositoryRoot == "" {
		return executionContext{}, fmt.Errorf("repository root is required when a repository is injected")
	}
	if filepath.IsAbs(options.WorkingDirectory) {
		return executionContext{}, fmt.Errorf("working directory %q must be repository-relative", options.WorkingDirectory)
	}
	root, err := filepath.Abs(options.RepositoryRoot)
	if err != nil {
		return executionContext{}, fmt.Errorf("resolve repository root: %w", err)
	}
	workingDirectory := root
	if options.WorkingDirectory != "" {
		workingDirectory = filepath.Join(root, filepath.FromSlash(options.WorkingDirectory))
	}
	relative, err := filepath.Rel(root, workingDirectory)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || len(relative) > 3 && relative[:3] == ".."+string(filepath.Separator) {
		return executionContext{}, fmt.Errorf("resolve repository-relative working directory %q", options.WorkingDirectory)
	}
	if relative == "." {
		relative = ""
	}
	return executionContext{
		repository:       options.Repository,
		root:             root,
		workingDirectory: workingDirectory,
		scanner:          options.Detector,
		relative:         filepath.ToSlash(relative),
	}, nil
}

func commandWorkingDirectory(ctx context.Context) (string, error) {
	if execution, ok := executionFromContext(ctx); ok && execution.workingDirectory != "" {
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
