package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/graph"
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
	var selector string
	if len(args) == 1 {
		selector = args[0]
	} else {
		selector, err = selectRunTarget(cmd, targets)
		if err != nil {
			return err
		}
	}
	target, err := resolveRunTargetAt(selector, targets, relative)
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
		return fmt.Errorf("run %q: %w", selector, err)
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
	if resolvedPrefix != "" && strings.HasPrefix(canonicalPath, resolvedPrefix+"/") {
		completionPath = strings.TrimPrefix(canonicalPath, resolvedPrefix+"/")
	} else if resolvedPrefix == canonicalPath {
		completionPath = ""
	}
	return pathPrefix + completionPath + "#" + parts[1]
}

func matchesRunTargetCompletion(prefix string, target runTarget, relative string) bool {
	if strings.Contains(prefix, "#") && relative != "" {
		parts := strings.SplitN(prefix, "#", 2)
		prefix = "//" + resolveSelectorPath(strings.TrimPrefix(parts[0], "//"), relative) + "#" + parts[1]
	}
	if strings.HasPrefix(target.selector, prefix) || matchesRunTargetAt(prefix, target, relative) {
		return true
	}
	if strings.Contains(prefix, "#") {
		return false
	}

	pathPart := strings.TrimPrefix(prefix, "//")
	if pathPart == "" {
		return true
	}
	resolved := resolveSelectorPath(pathPart, relative)
	canonicalPath, canonicalDir := runTargetPaths(target)
	if resolved == "" {
		return canonicalDir == ""
	}
	return resolved == canonicalPath || resolved == canonicalDir || strings.HasPrefix(canonicalPath, resolved) || strings.HasPrefix(canonicalDir, resolved)
}

func buildCompleteGraph(repo *attegit.Repo) (*graph.Graph, error) {
	g, err := repo.Graph()
	if err != nil {
		return nil, fmt.Errorf("build Git graph: %w", err)
	}
	registry, err := builtInDetectorRegistry()
	if err != nil {
		return nil, fmt.Errorf("register detectors: %w", err)
	}
	detectorGraph, err := registry.Graph(repo)
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
	hclTargets, err := attehcl.Targets(repo)
	if err != nil {
		return nil, err
	}
	goTargets, err := attego.Targets(repo)
	if err != nil {
		return nil, err
	}
	targets := make([]runTarget, 0, len(hclTargets)+len(goTargets))
	for _, target := range hclTargets {
		if target.Script == "" {
			continue
		}
		file := filepath.Join(root, filepath.FromSlash(target.Script.String()))
		dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(target.File.String())))
		filePath := target.File.String()
		rendered := attehcl.Selector(target).String()
		targets = append(targets, runTarget{
			selector: rendered,
			kind:     target.Kind,
			path:     filePath,
			name:     target.Name,
			index:    target.Index,
			label:    target.Label,
			dir:      dir,
			argv:     []string{"/usr/bin/env", "bash", file},
		})
	}
	for _, target := range goTargets {
		packageDir := target.PackageDir.String()
		dir := filepath.Join(root, filepath.FromSlash(packageDir))
		if packageDir == relative {
			// Keep the caller's working-directory path for the convenience target
			// while preserving the repository-wide list.
			dir = cwd
		}
		targets = append(targets, runTarget{
			selector: attego.Selector(target).String(),
			kind:     target.Kind,
			path:     packageDir,
			name:     "go_test",
			dir:      dir,
			argv:     []string{"go", "test", "-v"},
		})
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

func resolveRunTarget(selector string, targets []runTarget) (runTarget, error) {
	return resolveRunTargetAt(selector, targets, "")
}

func resolveRunTargetAt(selector string, targets []runTarget, relative string) (runTarget, error) {
	for _, target := range targets {
		if target.selector == selector {
			return target, nil
		}
	}
	candidates := make([]runTarget, 0)
	for _, target := range targets {
		if matchesRunTargetAt(selector, target, relative) {
			candidates = append(candidates, target)
		}
	}
	if len(candidates) == 0 {
		for _, target := range targets {
			if matchesRunTargetLabel(selector, target) {
				candidates = append(candidates, target)
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		ids := make([]string, len(candidates))
		for i, target := range candidates {
			ids[i] = target.selector
		}
		sort.Strings(ids)
		return runTarget{}, fmt.Errorf("selector %q is ambiguous; choose one of: %s", selector, strings.Join(ids, ", "))
	}
	return runTarget{}, fmt.Errorf("target %q not found", selector)
}

func matchesRunTarget(selector string, target runTarget) bool {
	return matchesRunTargetAt(selector, target, "")
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
	canonicalDir := strings.TrimSuffix(canonicalPath, attehcl.Filename)
	canonicalDir = strings.TrimSuffix(canonicalDir, "/")
	return canonicalPath, canonicalDir
}

func resolveSelectorPath(pathPart, relative string) string {
	resolved := filepath.ToSlash(filepath.Clean(filepath.Join(relative, filepath.FromSlash(pathPart))))
	if resolved == "." {
		return ""
	}
	return resolved
}

func matchesRunTargetPath(pathPart, canonicalPath, canonicalDir, relative string) bool {
	if pathPart == "" || pathPart == "." || pathPart == canonicalPath || pathPart == canonicalDir {
		return true
	}
	if relative == "" {
		return false
	}
	resolved := resolveSelectorPath(pathPart, relative)
	if strings.HasSuffix(pathPart, "/"+attehcl.Filename) || pathPart == attehcl.Filename {
		if resolved != canonicalPath {
			return false
		}
	} else if resolved != canonicalPath && resolved != canonicalDir {
		return false
	}
	return !strings.HasPrefix(resolved, "../") && resolved != ".."
}

func matchesRunTargetAt(selector string, target runTarget, relative string) bool {
	if matchesSelector(selector, target.selector) {
		return true
	}
	kindAlias, blockName := runTargetAliases(target)
	indexedName := fmt.Sprintf("%s.%d", kindAlias, target.index)
	if selector == kindAlias || selector == blockName || selector == indexedName {
		return true
	}
	selector = strings.TrimPrefix(selector, "//")
	parts := strings.SplitN(selector, "#", 2)
	if len(parts) != 2 {
		return false
	}
	pathPart, block := parts[0], parts[1]
	canonicalPath, canonicalDir := runTargetPaths(target)
	if !matchesRunTargetPath(pathPart, canonicalPath, canonicalDir, relative) {
		return false
	}
	return block == kindAlias || block == blockName || block == indexedName || block == strings.TrimPrefix(target.kind, attehcl.Namespace+":")
}

func matchesRunTargetLabel(selector string, target runTarget) bool {
	return target.label != "" && !strings.Contains(selector, "#") && selector == target.label
}

func matchesSelector(selector, canonical string) bool {
	selector = strings.TrimPrefix(selector, "//")
	canonical = strings.TrimPrefix(canonical, "//")
	parts := strings.SplitN(canonical, "#", 2)
	if len(parts) != 2 {
		return false
	}
	pathPart, targetName := parts[0], parts[1]
	if selector == canonical {
		return true
	}
	selectorParts := strings.SplitN(selector, "#", 2)
	if len(selectorParts) == 1 {
		return selector == targetName || selector == strings.TrimPrefix(targetName, strings.SplitN(targetName, ".", 2)[0]+".")
	}
	selectorPath, selectorName := selectorParts[0], selectorParts[1]
	if selectorName == targetName || selectorName == strings.TrimPrefix(targetName, strings.SplitN(targetName, ".", 2)[0]+".") {
		return selectorPath == pathPart || selectorPath == strings.TrimSuffix(pathPart, "/"+attehcl.Filename)
	}
	return false
} // TODO: resolve indexed and label aliases from runTarget metadata.
