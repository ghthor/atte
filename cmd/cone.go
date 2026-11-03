package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/ghthor/atte/cone"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/spf13/cobra"
)

type coneOptions struct {
	base            string
	head            string
	workingTree     bool
	quiet           bool
	externalImports bool
	goFiles         bool
	runTargets      bool
	contentHash     bool
	goVersions      bool
}

const coneLongDescription = `Print the entities whose content changed between two revisions.

The Cone of a change is every entity that could be affected by it: an entity is
in the Cone when it is new, or when its content hash differs between the base
and the head. A content hash covers an entity and everything it depends on, so
the Cone includes the files that were edited and everything that depends on
them, directly or not. Output is the head graph pruned to the Cone, starting at
the working directory like atte graph.

Both revisions are read from Git and passed as given to Git, so anything it
resolves works: HEAD~3, origin/main, a tag or a sha.

With no flags the command answers "what does my current work affect?" when Git
reports uncommitted changes (staged, unstaged, or untracked and not ignored),
and "what did the last commit affect?" otherwise:

  uncommitted changes, or --working-tree   HEAD..HEAD plus the working tree
  clean                                    HEAD^..HEAD

--base and --head each replace only their own side, and the other keeps the
default from the table. An explicit --head is compared as committed unless
--working-tree is given. The comparison that was made is printed to stderr,
unless --quiet is given.

Entities that were removed are not listed; their parent trees are shown because
their contents changed.

The Cone over-approximates by design. A hash sees bytes, not meaning, so a
comment edit is in the Cone along with everything that depends on the file.
Refining the Cone is left to later analysis. An entity that should be in the
Cone and is not is a defect, but the inputs the graph does not model yet cannot
be seen by this command:

  - the script a target runs (the script edge is not a dependency)
  - //go:embed files, .s, .c and .h files, and testdata
  - the Go toolchain, which is read from go.mod
  - a module upgrade that is only reached indirectly
  - go.work files, which are ignored`

func newConeCommand() *cobra.Command {
	options := &coneOptions{}
	coneCmd := &cobra.Command{
		Use:   "cone",
		Short: "Print the entities that changed between two revisions",
		Long:  coneLongDescription,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			ctx := cmd.Context()
			cwd, err := commandWorkingDirectory(ctx)
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			repoRoot, relative, err := repositoryContext(ctx, cwd)
			if err != nil {
				return err
			}
			dirty := false
			if options.dependsOnWorkingTreeState() {
				dirty, err = attegit.HasUncommittedChanges(repoRoot)
				if err != nil {
					return fmt.Errorf("check for uncommitted changes: %w", err)
				}
			}
			comparison := selectComparison(*options, dirty)
			if !options.quiet {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "cone: %s\n", comparison); err != nil {
					return fmt.Errorf("write comparison: %w", err)
				}
			}
			base, err := openRevision(repoRoot, "base", comparison.base)
			if err != nil {
				return err
			}
			var headOptions []attegit.OpenOption
			if comparison.headWorkingTree {
				headOptions = append(headOptions, attegit.WithWorkingTree())
			}
			head, err := openRevision(repoRoot, "head", comparison.head, headOptions...)
			if err != nil {
				return err
			}
			return printCone(ctx, cmd.OutOrStdout(), base, head, relative, PrintGraphOptions{
				IncludeExternalImports: options.externalImports,
				IncludeGoFiles:         options.goFiles,
				IncludeRunTargets:      options.runTargets,
				IncludeContentHash:     options.contentHash,
				IncludeGoVersions:      options.goVersions,
			})
		},
	}
	coneCmd.Flags().StringVar(&options.base, "base", "", `Base revision (default "HEAD" with uncommitted changes, otherwise "HEAD^")`)
	coneCmd.Flags().StringVar(&options.head, "head", "", `Head revision (default "HEAD", plus the working tree when there are uncommitted changes)`)
	coneCmd.Flags().
		BoolVar(&options.workingTree, "working-tree", false, "Include modified and non-ignored untracked files in the head revision, even if there are none")
	coneCmd.Flags().BoolVar(&options.quiet, "quiet", false, "Do not print the comparison that was made to stderr")
	coneCmd.Flags().BoolVar(&options.externalImports, "external-imports", false, "Include external Go imports")
	coneCmd.Flags().BoolVar(&options.goFiles, "go-files", false, "Include Go source files")
	coneCmd.Flags().BoolVar(&options.runTargets, "run-targets", false, "Render runnable entities as atte run selectors")
	coneCmd.Flags().BoolVar(&options.contentHash, "content-hash", false, "Show the content hash of each entity as <base>..<head>, or new")
	coneCmd.Flags().BoolVar(&options.goVersions, "go-versions", false, "Show the Go version each package's module declares")
	return coneCmd
}

// coneComparison is the pair of revisions atte cone compares.
type coneComparison struct {
	base            string
	head            string
	headWorkingTree bool
}

func (c coneComparison) String() string {
	head := c.head
	switch {
	case c.headWorkingTree && c.head == "HEAD":
		head = "working tree"
	case c.headWorkingTree:
		head += " + working tree"
	}
	return c.base + ".." + head
}

// dependsOnWorkingTreeState reports whether the flags leave anything for the
// state of the working tree to decide. When they do not, Git is not asked.
func (o coneOptions) dependsOnWorkingTreeState() bool {
	return !o.workingTree && (o.base == "" || o.head == "")
}

// selectComparison chooses the revisions to compare from the flags and from
// whether the repository has uncommitted changes. An empty base or head means
// that the flag was not given.
func selectComparison(options coneOptions, dirty bool) coneComparison {
	comparison := coneComparison{
		base:            options.base,
		head:            options.head,
		headWorkingTree: options.workingTree || (dirty && options.head == ""),
	}
	if comparison.base == "" {
		comparison.base = "HEAD^"
		if options.workingTree || dirty {
			comparison.base = "HEAD"
		}
	}
	if comparison.head == "" {
		comparison.head = "HEAD"
	}
	return comparison
}

// openRevision opens revision of the repository at root. It does not use an
// injected repository, which stands for a single revision. Errors name the
// side so that a bad --base is told apart from a bad --head.
func openRevision(root, side, revision string, options ...attegit.OpenOption) (*attegit.Repo, error) {
	repo, err := attegit.Open(root, revision, options...)
	if err != nil {
		return nil, fmt.Errorf("resolve %s revision %q: %w", side, revision, err)
	}
	return repo, nil
}

// printCone prints the head graph pruned to the entities that changed from
// base to head, starting from relativePath. It prints nothing when nothing
// below relativePath changed.
func printCone(ctx context.Context, w io.Writer, base, head *attegit.Repo, relativePath string, options PrintGraphOptions) error {
	if relativePath != "" {
		if _, err := reference.ParseTree(relativePath); err != nil {
			return fmt.Errorf("invalid repository working directory %q: %w", relativePath, err)
		}
	}
	scanner, err := scannerForContext(ctx)
	if err != nil {
		return fmt.Errorf("register detectors: %w", err)
	}
	// The hashes of the two revisions are what is compared, so both are
	// always computed, whatever is shown.
	baseSnapshot, err := cone.Capture(ctx, scanner, base, cone.WithContentHashes())
	if err != nil {
		return fmt.Errorf("capture base: %w", err)
	}
	headSnapshot, err := cone.Capture(ctx, scanner, head, cone.WithContentHashes())
	if err != nil {
		return fmt.Errorf("capture head: %w", err)
	}
	delta, err := cone.Diff(baseSnapshot, headSnapshot)
	if err != nil {
		return fmt.Errorf("compare revisions: %w", err)
	}
	var runSelectors map[graph.EntityID]string
	if options.IncludeRunTargets {
		runSelectors, err = graphRunTargetSelectors(ctx, head)
		if err != nil {
			return fmt.Errorf("discover run targets: %w", err)
		}
	}
	render := renderOptions{
		PrintGraphOptions: options,
		hashes:            headSnapshot.Hashes,
		complete:          headSnapshot.Graph,
		hashLabel:         rangeHashLabel(baseSnapshot, headSnapshot),
	}
	return printGitGraph(w, head, delta.Graph, relativePath, render, scanner, runSelectors)
}

// rangeHashLabel labels an entity with its abbreviated hash in base and in
// head, as <base>..<head>, or "new" when base does not have the entity.
func rangeHashLabel(base, head *cone.Snapshot) func(graph.EntityID) (string, error) {
	return func(id graph.EntityID) (string, error) {
		headSum, err := head.Hashes.Sum(id)
		if err != nil {
			return "", fmt.Errorf("head content hash of %q: %w", id, err)
		}
		if !base.Graph.Has(id) {
			return "new", nil
		}
		baseSum, err := base.Hashes.Sum(id)
		if err != nil {
			return "", fmt.Errorf("base content hash of %q: %w", id, err)
		}
		return baseSum.String()[:contentHashLength] + ".." + headSum.String()[:contentHashLength], nil
	}
}
