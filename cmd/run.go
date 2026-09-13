package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/ghthor/atte/cmd/runcomp"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/detector/registry"
	"github.com/ghthor/atte/reference/selector"
	fzf "github.com/junegunn/fzf/src"
	"github.com/spf13/cobra"
)

var (
	runDryRun bool
	runList   bool
)

type runTarget struct {
	selector string
	kind     string
	path     string
	name     string
	index    int
	aliases  []string
	label    string
	dir      string
	argv     []string
}

var runCmd = &cobra.Command{
	Use:               "run [selector]",
	Short:             "Run test, codegen, and lint targets from the repository",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: runCmdValidArgs,
	Long: strings.TrimLeftFunc(`
The run command executes scripts declared in atte.hcl test, codegen, and lint
blocks, or runs go test -v for a repository Go package.

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
	RunE: runCommand,
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Print the resolved target and command without executing it")
	runCmd.Flags().BoolVar(&runList, "list", false, "List runnable targets")
}

func loadRunTargets(ctx context.Context) (root, cwd, relative string, targets []runTarget, err error) {
	cwd, err = commandWorkingDirectory(ctx)
	if err != nil {
		err = fmt.Errorf("get working directory: %w", err)
		return
	}
	root, relative, err = repositoryContext(ctx, cwd)
	if err != nil {
		return
	}
	repo, err := openRepository(ctx, root, "HEAD", attegit.WithWorkingTree())
	if err != nil {
		err = fmt.Errorf("open repository: %w", err)
		return
	}
	targets, err = runTargets(ctx, repo, root, cwd, relative)
	return
}

func runCommand(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	_, _, relative, targets, err := loadRunTargets(ctx)
	if err != nil {
		return err
	}
	if runList {
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
		input, err = selectRunTarget(cmd, targets)
		if err != nil {
			return err
		}
	}
	target, err := resolveRunTargetAt(input, targets, relative)
	if err != nil {
		return err
	}
	if runDryRun {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "attr run %s\n%s\n", target.selector, strings.Join(target.argv, " ")); err != nil {
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

func runCmdValidArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	root, cwd, _, targets, err := loadRunTargets(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return runCmdValidArgsFromTargets(args, toComplete, root, cwd, targets)
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

func runTargets(ctx context.Context, repo *attegit.Repo, root, cwd, relative string) ([]runTarget, error) {
	builtIns, err := registry.NewBuiltIn()
	if err != nil {
		return nil, err
	}
	registeredTargets, err := builtIns.Targets(ctx, repo)
	if err != nil {
		return nil, err
	}
	hclTargets, err := attehcl.Targets(ctx, repo, builtIns.FunctionProvider())
	if err != nil {
		return nil, err
	}
	hclByID := make(map[graph.EntityID]attehcl.Target, len(hclTargets))
	for _, target := range attehcl.SortedTargets(hclTargets) {
		hclByID[target.ID] = target
	}
	targets := make([]runTarget, 0, len(registeredTargets))
	for _, target := range registeredTargets {
		canonical, ok := selector.String(target)
		if !ok {
			continue
		}
		switch target.Namespace {
		case attehcl.Namespace:
			native, ok := hclByID[target.ID]
			if !ok || !native.Runnable() {
				continue
			}
			command, err := native.Execution(root)
			if err != nil {
				return nil, err
			}
			targets = append(targets, runTarget{
				selector: canonical,
				kind:     native.Kind,
				path:     native.File.String(),
				name:     native.DisplayName(),
				index:    native.Index,
				aliases:  native.Aliases,
				label:    native.Label,
				dir:      command.Dir,
				argv:     command.Args,
			})
		case attego.Namespace:
			dir := filepath.Join(root, filepath.FromSlash(target.Path))
			if target.Path == relative {
				// Keep the caller's working-directory path for the convenience target
				// while preserving the repository-wide list.
				dir = cwd
			}
			targets = append(targets, runTarget{
				selector: canonical,
				kind:     target.Kind,
				path:     target.Path,
				name:     target.Name,
				index:    target.Index,
				aliases:  target.Aliases,
				dir:      dir,
				argv:     []string{"go", "test", "-v"},
			})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].selector < targets[j].selector })
	return targets, nil
}

func selectRunTarget(cmd *cobra.Command, targets []runTarget) (string, error) {
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

func resolveRunTargetAt(input string, targets []runTarget, relative string) (runTarget, error) {
	for _, target := range targets {
		if target.selector == input {
			return target, nil
		}
	}
	candidates := make([]runTarget, 0)
	for _, target := range targets {
		if matchesRunTargetAt(input, target, relative) {
			candidates = append(candidates, target)
		}
	}
	if len(candidates) > 1 && !strings.Contains(input, "#") {
		candidates = preferLocalRunTargets(candidates, relative)
	}
	if len(candidates) == 0 {
		for _, target := range targets {
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
	return runTarget{}, fmt.Errorf("target %q not found", input)
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

func ambiguousRunTargetError(input string, candidates []runTarget) error {
	ids := make([]string, len(candidates))
	for i := range candidates {
		ids[i] = candidates[i].selector
	}
	sort.Strings(ids)
	commands := make([]string, len(ids))
	for i, id := range ids {
		commands[i] = "atte run " + id
	}
	return fmt.Errorf("selector %q is ambiguous; possible commands:\n%s", input, strings.Join(commands, "\n"))
}

func runTargetPaths(target runTarget) (string, string) {
	parsed, err := selector.Parse(target.selector)
	if err != nil {
		return "", ""
	}
	return parsed.Path, parsed.Tree().String()
}

func matchesRunTargetAt(input string, target runTarget, relative string) bool {
	canonicalPath, _ := runTargetPaths(target)
	namespace := strings.SplitN(target.kind, ":", 2)[0]
	return selector.Matches(graphtarget.ID{
		Namespace: graphtarget.Namespace(namespace),
		Kind:      target.kind,
		Path:      canonicalPath,
		Name:      target.name,
		Index:     target.index,
		Aliases:   target.aliases,
	}, input, relative)
}

func matchesRunTargetLabel(input string, target runTarget) bool {
	return target.label != "" && !strings.Contains(input, "#") && input == target.label
}
