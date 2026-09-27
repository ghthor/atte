package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ghthor/atte/cmd/runcomp"
	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference/selector"
	fzf "github.com/junegunn/fzf/src"
	"github.com/spf13/cobra"
)

type runOptions struct {
	dryRun bool
	list   bool
}

type runTarget struct {
	id           graphtarget.ID
	selector     string
	presentation selector.Target
	label        string
	dir          string
	argv         []string
}

type loadedRunTargets struct {
	root     string
	cwd      string
	relative string
	scanner  detector.Scanner
	targets  []runTarget
}

func newRunCommand() *cobra.Command {
	options := &runOptions{}
	runCmd := &cobra.Command{
		Use:               "run [selector]",
		Short:             "Run targets discovered in the repository",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: runCmdValidArgs,
		Long: strings.TrimLeftFunc(`
The run command executes runnable targets discovered by attached Sensors. The
built-in Sensors provide scripts declared in atte.hcl and go test -v for Go
packages.

Targets may be selected with a short alias, a path-qualified selector, or a
repository-root-qualified selector:

    atte run test
    atte run test.go
    atte run test.0
    atte run atte.hcl#test.go
    atte run ./atte.hcl#test.go
    atte run //path/to/atte.hcl#test.go
    atte run //path/to#test.go
    atte run go_test

Relative selectors are resolved from the current working directory and may not
leave the repository:

    atte run ..#test
    atte run ../detector/attego#go_test

With no selector, the command opens an fzf picker containing every runnable
repository target:

    atte run

Shell completion supports short aliases and path-qualified selectors. Run
atte completion --help for shell completion installation instructions.
`, unicode.IsSpace),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommand(cmd, args, *options)
		},
	}
	runCmd.Flags().BoolVar(&options.dryRun, "dry-run", false, "Print the resolved target and command without executing it")
	runCmd.Flags().BoolVar(&options.list, "list", false, "List runnable targets")
	return runCmd
}

func loadRunTargets(ctx context.Context) (loadedRunTargets, error) {
	cwd, err := commandWorkingDirectory(ctx)
	if err != nil {
		return loadedRunTargets{}, fmt.Errorf("get working directory: %w", err)
	}
	root, relative, err := repositoryContext(ctx, cwd)
	if err != nil {
		return loadedRunTargets{}, err
	}
	repo, err := openRepository(ctx, root, "HEAD", attegit.WithWorkingTree())
	if err != nil {
		return loadedRunTargets{}, fmt.Errorf("open repository: %w", err)
	}
	scanner, err := scannerForContext(ctx)
	if err != nil {
		return loadedRunTargets{}, err
	}
	targets, err := runTargets(ctx, repo, root, cwd, relative, scanner)
	if err != nil {
		return loadedRunTargets{}, err
	}
	return loadedRunTargets{
		root:     root,
		cwd:      cwd,
		relative: relative,
		scanner:  scanner,
		targets:  targets,
	}, nil
}

func runCommand(cmd *cobra.Command, args []string, options runOptions) error {
	ctx := cmd.Context()
	loaded, err := loadRunTargets(ctx)
	if err != nil {
		return err
	}
	relative := loaded.relative
	scanner := loaded.scanner
	targets := loaded.targets
	if options.list {
		for _, target := range targets {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), target.selector); err != nil {
				return fmt.Errorf("list targets: %w", err)
			}
		}
		return nil
	}
	var input string
	if len(args) == 1 {
		input = args[0]
	} else {
		input, err = selectRunTarget(targets)
		if err != nil {
			return err
		}
	}
	target, err := resolveRunTargetAt(ctx, scanner, input, targets, relative)
	if err != nil {
		return err
	}
	if options.dryRun {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "attr run %s\n%s\n", target.selector, formatRunArgs(target.argv)); err != nil {
			return fmt.Errorf("print dry run: %w", err)
		}
		return nil
	}
	process := exec.CommandContext(ctx, target.argv[0], target.argv[1:]...)
	process.Dir = target.dir
	process.Stdin = cmd.InOrStdin()
	process.Stdout = cmd.OutOrStdout()
	process.Stderr = cmd.ErrOrStderr()
	if err := process.Run(); err != nil {
		return fmt.Errorf("run %q: %w", input, err)
	}
	return nil
}

func formatRunArgs(args []string) string {
	formatted := make([]string, len(args))
	for i, arg := range args {
		if arg == "" || strings.IndexFunc(arg, unicode.IsSpace) >= 0 {
			formatted[i] = strconv.Quote(arg)
		} else {
			formatted[i] = arg
		}
	}
	return strings.Join(formatted, " ")
}

func runCmdValidArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	loaded, err := loadRunTargets(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return runCmdValidArgsFromTargets(args, toComplete, loaded.root, loaded.cwd, loaded.targets)
}

func runCmdValidArgsFromTargets(args []string, toComplete, root, cwd string, targets []runTarget) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	completionTargets := make([]string, 0, len(targets))
	for _, target := range targets {
		completionTargets = append(completionTargets, target.selector)
	}
	matches, err := runcomp.Match(root, cwd, toComplete, completionTargets)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}

func runTargets(ctx context.Context, repo *attegit.Repo, root, cwd, relative string, scanner detector.Scanner) ([]runTarget, error) {
	discovered, err := scanner.Targets(ctx, repo)
	if err != nil {
		return nil, err
	}
	targets := make([]runTarget, 0, len(discovered))
	for _, id := range discovered {
		presentation, ok := scanner.TargetSelector(id)
		if !ok {
			return nil, fmt.Errorf("target %q in namespace %q has no selector capability", id.ID, id.Namespace)
		}
		canonical, ok := scanner.TargetString(id)
		if !ok {
			return nil, fmt.Errorf("target %q in namespace %q has no canonical selector", id.ID, id.Namespace)
		}
		execution, err := scanner.ExecuteTarget(ctx, repo, root, id)
		if err != nil {
			return nil, err
		}
		dir := execution.Dir
		if selector.ContainingDir(presentation.Path) == relative {
			// Preserve the caller's working-directory path for the local target
			// while retaining repository-wide target discovery.
			dir = cwd
		}
		targets = append(targets, runTarget{
			id:           id,
			selector:     canonical,
			presentation: presentation,
			label:        id.Label,
			dir:          dir,
			argv:         execution.Args,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].selector < targets[j].selector })
	return targets, nil
}

func selectRunTarget(targets []runTarget) (string, error) {
	if len(targets) == 0 {
		return "", fmt.Errorf("no runnable targets found")
	}
	input := make(chan string)
	output := make(chan string, 1)
	go func() {
		for _, target := range targets {
			input <- target.selector
		}
		close(input)
	}()
	opts, err := fzf.ParseOptions(true, nil)
	if err != nil {
		return "", fmt.Errorf("configure target selector: %w", err)
	}
	opts.Input = input
	opts.Output = output
	opts.Inputless = false
	opts.ForceTtyIn = true
	status, err := fzf.Run(opts)
	if err != nil {
		return "", fmt.Errorf("select target: %w", err)
	}
	if status != 0 {
		return "", fmt.Errorf("target selection cancelled")
	}
	select {
	case selected := <-output:
		if selected == "" {
			return "", fmt.Errorf("target selection was empty")
		}
		return selected, nil
	default:
		return "", fmt.Errorf("target selection was empty")
	}
}

func resolveRunTargetAt(ctx context.Context, scanner detector.Scanner, input string, targets []runTarget, relative string) (runTarget, error) {
	if err := ctx.Err(); err != nil {
		return runTarget{}, err
	}
	exact := make([]runTarget, 0)
	for _, target := range targets {
		if target.selector == input {
			exact = append(exact, target)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return runTarget{}, ambiguousRunTargetError(input, exact)
	}
	candidates := make([]runTarget, 0)
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return runTarget{}, err
		}
		if matchesRunTargetAt(scanner, input, target, relative) {
			candidates = append(candidates, target)
		}
	}
	if len(candidates) > 1 && !strings.Contains(input, "#") {
		candidates = preferLocalRunTargets(candidates, relative)
	}
	if len(candidates) == 0 {
		for _, target := range targets {
			if err := ctx.Err(); err != nil {
				return runTarget{}, err
			}
			if matchesRunTargetLabel(input, target) {
				candidates = append(candidates, target)
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return runTarget{}, ambiguousRunTargetError(input, candidates)
	}
	return runTarget{}, noRunTargetError(input)
}

// preferLocalRunTargets narrows candidates to those rooted at relative when a
// pathless alias matches more than one target, so a short alias like "test"
// prefers the current directory's target over targets elsewhere in the
// repository. It falls back to any target within relative (excluding
// relative's own atte.hcl file, whose block-scoped aliases already matched
// directly) when no candidate is rooted exactly at relative.
func preferLocalRunTargets(candidates []runTarget, relative string) []runTarget {
	local := make([]runTarget, 0, len(candidates))
	for _, target := range candidates {
		_, canonicalDir := runTargetPaths(target)
		if canonicalDir == relative {
			local = append(local, target)
		}
	}
	if len(local) > 0 {
		return local
	}
	relativeHCLFile := relative + "/" + attehcl.Filename
	if relative == "" {
		relativeHCLFile = attehcl.Filename
	}
	for _, target := range candidates {
		_, canonicalDir := runTargetPaths(target)
		if selector.PathIsWithin(canonicalDir, relative) && strings.TrimPrefix(target.selector, "//") != relativeHCLFile {
			local = append(local, target)
		}
	}
	if len(local) > 0 {
		return local
	}
	return candidates
}

type runTargetNotFoundError struct {
	*selector.NoMatchError
}

func (e *runTargetNotFoundError) Error() string {
	return fmt.Sprintf("target %q not found", e.Input)
}

func (e *runTargetNotFoundError) Unwrap() error {
	return e.NoMatchError
}

func noRunTargetError(input string) error {
	return &runTargetNotFoundError{NoMatchError: &selector.NoMatchError{Input: input}}
}

func ambiguousRunTargetError(input string, candidates []runTarget) error {
	ordered := append([]runTarget(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].selector != ordered[j].selector {
			return ordered[i].selector < ordered[j].selector
		}
		return ordered[i].id.ID < ordered[j].id.ID
	})
	structured := make([]selector.AmbiguousCandidate, 0, len(ordered))
	commands := make([]string, 0, len(ordered))
	for _, candidate := range ordered {
		structured = append(structured, selector.AmbiguousCandidate{
			TargetID: string(candidate.id.ID),
			Selector: candidate.selector,
		})
		commands = append(commands, "atte run "+candidate.selector)
	}
	return fmt.Errorf("%w; possible commands:\n%s", &selector.AmbiguousError{Input: input, Candidates: structured}, strings.Join(commands, "\n"))
}

func runTargetPaths(target runTarget) (string, string) {
	path := target.presentation.Path
	return path, selector.ContainingDir(path)
}

func matchesRunTargetAt(scanner detector.Scanner, input string, target runTarget, relative string) bool {
	return scanner.TargetMatches(target.id, input, relative)
}

func matchesRunTargetLabel(input string, target runTarget) bool {
	return target.label != "" && !strings.Contains(input, "#") && input == target.label
}
