package cmd

import (
	"context"

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
