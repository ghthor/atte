package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/graph"
	"github.com/ghthor/atte/reference/selector"
	"github.com/ghthor/atte/registry"
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

func runCommand(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	root, relative, err := repositoryContext(cwd)
	if err != nil {
		return err
	}
	repo, err := attegit.Open(root, "HEAD", attegit.WithWorkingTree())
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}
	targets, err := runTargets(repo, root, cwd, relative)
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
	process := exec.Command(target.argv[0], target.argv[1:]...)
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

	cwd, err := os.Getwd()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	root, relative, err := repositoryContext(cwd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	repo, err := attegit.Open(root, "HEAD", attegit.WithWorkingTree())
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	targets, err := runTargets(repo, root, cwd, relative)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return runCmdValidArgsFromTargets(args, toComplete, relative, targets)
}

func runCmdValidArgsFromTargets(args []string, toComplete, relative string, targets []runTarget) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	matches := make([]string, 0, len(targets))
	for _, target := range targets {
		if matchesRunTargetCompletion(toComplete, target, relative) {
			matches = append(matches, formatRunTargetCompletion(toComplete, target, relative))
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}

func formatRunTargetCompletion(prefix string, target runTarget, relative string) string {
	for _, alias := range runTargetCompletionAliases(target, relative) {
		if strings.HasPrefix(alias, prefix) {
			return alias
		}
	}
	if !strings.HasPrefix(prefix, ".") || relative == "" {
		return target.selector
	}
	parts := strings.SplitN(strings.TrimPrefix(target.selector, "//"), "#", 2)
	if len(parts) != 2 {
		return target.selector
	}
	selectorParts := strings.SplitN(prefix, "#", 2)
	pathPrefix := selectorParts[0]
	canonicalPath := parts[0]
	if target.kind != attego.PackageTestKind && !strings.HasSuffix(canonicalPath, "/"+attehcl.Filename) && canonicalPath != attehcl.Filename {
		canonicalPath += "/" + attehcl.Filename
	}
	resolvedPrefix := resolveSelectorPath(pathPrefix, relative)
	completionPath := canonicalPath
	if rel, ok := selector.RelativePath(canonicalPath, resolvedPrefix); ok {
		completionPath = rel
	}
	if completionPath == "" {
		return pathPrefix + "#" + parts[1]
	}
	if pathPrefix != "" && strings.HasSuffix(pathPrefix, "/") {
		return pathPrefix + completionPath + "#" + parts[1]
	}
	if strings.HasPrefix(pathPrefix, "../") {
		return pathPrefix + strings.TrimPrefix(completionPath, strings.TrimPrefix(pathPrefix, "../")) + "#" + parts[1]
	}
	return pathPrefix + completionPath + "#" + parts[1]
}

func runTargetCompletionAliases(target runTarget, relative string) []string {
	kindAlias, blockName := runTargetAliases(target)
	_, canonicalDir := runTargetPaths(target)
	if canonicalDir == relative {
		if target.kind == attego.PackageTestKind && relative != "" {
			blockName = strings.TrimPrefix(blockName, kindAlias+".")
		}
		aliases := []string{blockName}
		if target.kind == attego.PackageTestKind {
			aliases = append(aliases, fmt.Sprintf("%s.%d", kindAlias, target.index), kindAlias)
		}
		return aliases
	}
	if relative == "" {
		return nil
	}
	dir, ok := selector.RelativePath(canonicalDir, relative)
	if !ok || dir == "" {
		return nil
	}
	if target.kind != attego.PackageTestKind {
		blockName = strings.TrimPrefix(blockName, kindAlias+".")
		return []string{dir + "#" + blockName}
	}
	return []string{dir + "#" + blockName, dir + "#" + fmt.Sprintf("%s.%d", kindAlias, target.index), dir + "#" + kindAlias}
}

func matchesRunTargetCompletion(prefix string, target runTarget, relative string) bool {
	for _, alias := range runTargetCompletionAliases(target, relative) {
		if strings.HasPrefix(alias, prefix) {
			return true
		}
	}
	if strings.Contains(prefix, "#") && relative != "" {
		parts := strings.SplitN(prefix, "#", 2)
		prefix = "//" + resolveSelectorPath(strings.TrimPrefix(parts[0], "//"), relative) + "#" + parts[1]
	}
	if strings.HasPrefix(target.selector, prefix) && (relative == "" || selector.PathIsWithin(strings.TrimPrefix(target.selector, "//"), relative) || strings.HasPrefix(prefix, "//")) {
		return true
	}
	if strings.Contains(prefix, "#") && matchesRunTargetAt(prefix, target, relative) {
		return true
	}
	if strings.Contains(prefix, "#") {
		return false
	}
	if relative != "" && !strings.Contains(prefix, "/") && !strings.HasPrefix(prefix, "..") && !strings.Contains(prefix, "#") {
		return false
	}

	pathPart := strings.TrimPrefix(prefix, "//")
	if pathPart == "" {
		if relative == "" {
			return true
		}
		_, canonicalDir := runTargetPaths(target)
		return selector.PathIsWithin(canonicalDir, relative)
	}
	resolved := resolveSelectorPath(pathPart, relative)
	canonicalPath, canonicalDir := runTargetPaths(target)
	if resolved == "" {
		return selector.IsImmediateChild(canonicalPath, resolved)
	}
	return selector.PathHasSegmentPrefix(canonicalPath, resolved) || selector.PathHasSegmentPrefix(canonicalDir, resolved)
}

func buildCompleteGraph(repo *attegit.Repo) (*graph.Graph, error) {
	g, err := repo.Graph()
	if err != nil {
		return nil, fmt.Errorf("build Git graph: %w", err)
	}
	registry, err := registry.NewBuiltIn()
	if err != nil {
		return nil, fmt.Errorf("register detectors: %w", err)
	}
	detectorGraph, err := registry.Graph(repo, detector.WithAttachToTree())
	if err != nil {
		return nil, fmt.Errorf("build detector graph: %w", err)
	}
	if detectorGraph != nil {
		if err := g.Absorb(detectorGraph); err != nil {
			return nil, fmt.Errorf("merge detector graph: %w", err)
		}
	}
	return g, nil
}

func runTargets(repo *attegit.Repo, root, cwd, relative string) ([]runTarget, error) {
	if _, err := buildCompleteGraph(repo); err != nil {
		return nil, err
	}
	builtIns, err := registry.NewBuiltIn()
	if err != nil {
		return nil, err
	}
	registeredTargets, err := builtIns.Targets(repo)
	if err != nil {
		return nil, err
	}
	hclTargets, err := attehcl.Targets(repo, builtIns.FunctionProvider())
	if err != nil {
		return nil, err
	}
	hclByID := make(map[graph.EntityID]attehcl.Target, len(hclTargets))
	for _, target := range hclTargets {
		hclByID[target.ID] = target
	}
	targets := make([]runTarget, 0, len(registeredTargets))
	for _, target := range registeredTargets {
		canonical, ok := builtIns.Selector(target)
		if !ok {
			continue
		}
		switch target.Namespace {
		case attehcl.Namespace:
			native, ok := hclByID[target.ID]
			if !ok || native.Script == "" {
				continue
			}
			file := filepath.Join(root, filepath.FromSlash(native.Script.String()))
			dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(native.File.String())))
			targets = append(targets, runTarget{
				selector: canonical.String(),
				kind:     native.Kind,
				path:     native.File.String(),
				name:     native.Name,
				index:    native.Index,
				label:    native.Label,
				dir:      dir,
				argv:     []string{"/usr/bin/env", "bash", file},
			})
		case attego.Namespace:
			dir := filepath.Join(root, filepath.FromSlash(target.Path))
			if target.Path == relative {
				// Keep the caller's working-directory path for the convenience target
				// while preserving the repository-wide list.
				dir = cwd
			}
			targets = append(targets, runTarget{
				selector: canonical.String(),
				kind:     target.Kind,
				path:     target.Path,
				name:     target.Name,
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

func resolveRunTarget(input string, targets []runTarget) (runTarget, error) {
	return resolveRunTargetAt(input, targets, "")
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
	for i, target := range candidates {
		ids[i] = target.selector
	}
	sort.Strings(ids)
	commands := make([]string, len(ids))
	for i, id := range ids {
		commands[i] = "atte run " + id
	}
	return fmt.Errorf("selector %q is ambiguous; possible commands:\n%s", input, strings.Join(commands, "\n"))
}

func matchesRunTarget(input string, target runTarget) bool {
	return matchesRunTargetAt(input, target, "")
}

func runTargetAliases(target runTarget) (string, string) {
	kind := strings.TrimPrefix(target.kind, attehcl.Namespace+":")
	kindAlias := kind
	blockName := kind + "." + target.name
	if target.kind == attego.PackageTestKind {
		kindAlias = "go_test"
		blockName = "go_test"
	}
	return kindAlias, blockName
}

func runTargetPaths(target runTarget) (string, string) {
	canonicalPath := strings.TrimPrefix(target.selector, "//")
	canonicalPath = strings.SplitN(canonicalPath, "#", 2)[0]
	return canonicalPath, selector.ContainingDir(canonicalPath)
}

func resolveSelectorPath(pathPart, relative string) string {
	resolved, err := selector.ResolvePath(pathPart, relative)
	if err != nil {
		return ""
	}
	return resolved
}

func (target runTarget) selectorTarget() selector.Target {
	kindAlias, _ := runTargetAliases(target)
	canonicalPath, _ := runTargetPaths(target)
	name := target.name
	if target.kind == attego.PackageTestKind {
		name = ""
	}
	return selector.Target{Path: canonicalPath, Kind: kindAlias, Name: name, Index: target.index}
}

func matchesRunTargetAt(input string, target runTarget, relative string) bool {
	return target.selectorTarget().Matches(input, relative)
}

func matchesRunTargetLabel(input string, target runTarget) bool {
	return target.label != "" && !strings.Contains(input, "#") && input == target.label
}
