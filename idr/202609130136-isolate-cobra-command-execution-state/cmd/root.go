package cmd

import (
	"io"
	"os"

	"github.com/spf13/cobra"
)

// rootOptions belongs to one command invocation. Persistent flags write here,
// and every command below receives the same invocation-local options value.
type rootOptions struct {
	project string
	verbose bool
}

// NewRootCommand constructs a complete, fresh Cobra tree.
func NewRootCommand() *cobra.Command {
	options := &rootOptions{}

	rootCmd := &cobra.Command{
		Use:   "example",
		Short: "A small Cobra command tree",
	}
	rootCmd.PersistentFlags().StringVar(&options.project, "project", "demo", "project name")
	rootCmd.PersistentFlags().BoolVar(&options.verbose, "verbose", false, "print extra details")
	rootCmd.AddCommand(
		newHelloCommand(options),
		newConfigCommand(options),
	)
	return rootCmd
}

// Execute constructs a new tree for every invocation. No command, flag value,
// argument list, or output writer is shared with another invocation.
func Execute(args []string, in io.Reader, out, errOut io.Writer) error {
	rootCmd := NewRootCommand()
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	rootCmd.SetArgs(args)
	rootCmd.SetIn(in)
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	return rootCmd.Execute()
}
