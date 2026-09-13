package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newHelloCommand(options *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "hello",
		Short: "Greet the selected project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "hello %s\n", options.project)
			if options.verbose {
				fmt.Fprintln(cmd.OutOrStdout(), "verbose: true")
			}
			return nil
		},
	}
}
