package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// config is a subcommand with its own subcommands. Each leaf receives the
// invocation-local options so it can read the root persistent flags.
func newConfigCommand(options *rootOptions) *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect project configuration",
	}
	configCmd.AddCommand(
		newConfigShowCommand(options),
		newConfigCheckCommand(options),
	)
	return configCmd
}

func newConfigShowCommand(options *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the selected project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "project: %s\n", options.project)
			if options.verbose {
				fmt.Fprintln(cmd.OutOrStdout(), "verbose: true")
			}
			return nil
		},
	}
}

func newConfigCheckCommand(options *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check the selected project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.verbose {
				fmt.Fprintf(cmd.OutOrStdout(), "checked project %s (verbose)\n", options.project)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "checked project %s\n", options.project)
			return nil
		},
	}
}
