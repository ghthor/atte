package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/reference"
	"github.com/spf13/cobra"
	"github.com/xlab/treeprint"
)

type graphOptions struct {
	ref             string
	workingTree     bool
	externalImports bool
	goFiles         bool
	runTargets      bool
}

// PrintGraphOptions controls the details included in graph output.
type PrintGraphOptions struct {
	// IncludeExternalImports includes imports that cannot be resolved inside the repository.
	IncludeExternalImports bool
	// IncludeGoFiles includes Go source files linked to packages and package tests.
	IncludeGoFiles bool
	// IncludeRunTargets renders runnable entities using atte run selectors.
	IncludeRunTargets bool
}

func newGraphCommand() *cobra.Command {
	options := &graphOptions{}
	graphCmd := &cobra.Command{
		Use:   "graph",
		Short: "Print the graph for the current directory",
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
			var openOptions []attegit.OpenOption
			if options.workingTree {
				openOptions = append(openOptions, attegit.WithWorkingTree())
			}
			repo, err := openRepository(ctx, repoRoot, options.ref, openOptions...)
			if err != nil {
				return err
			}
			return printGraph(
				ctx,
				cmd.OutOrStdout(),
				repo,
				relative,
				PrintGraphOptions{IncludeExternalImports: options.externalImports, IncludeGoFiles: options.goFiles, IncludeRunTargets: options.runTargets},
			)
		},
	}
	graphCmd.Flags().StringVarP(&options.ref, "ref", "r", "HEAD", "Git revision to print")
	graphCmd.Flags().BoolVar(&options.workingTree, "working-tree", false, "Include modified and non-ignored untracked files")
	graphCmd.Flags().BoolVar(&options.externalImports, "external-imports", false, "Include external Go imports")
	graphCmd.Flags().BoolVar(&options.goFiles, "go-files", false, "Include Go source files")
	graphCmd.Flags().BoolVar(&options.runTargets, "run-targets", false, "Render runnable entities as atte run selectors")
	return graphCmd
}

func repositoryContext(ctx context.Context, cwd string) (string, string, error) {
	if execution, ok := executionFromContext(ctx); ok && execution.repository != nil {
		return execution.root, execution.relative, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("resolve repository root: %w", err)
	}
	root := strings.TrimSpace(out.String())
	relative, err := filepath.Rel(root, cwd)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("resolve repository-relative working directory")
	}
	if relative == "." {
		relative = ""
	}
	return root, filepath.ToSlash(relative), nil
}

func printGraph(ctx context.Context, w io.Writer, repo *attegit.Repo, relativePath string, options ...PrintGraphOptions) error {
	if relativePath != "" {
		if _, err := reference.ParseTree(relativePath); err != nil {
			return fmt.Errorf("invalid repository working directory %q: %w", relativePath, err)
		}
	}
	var printOptions PrintGraphOptions
	if len(options) > 0 {
		printOptions = options[0]
	}
	gitGraph, err := repo.Graph()
	if err != nil {
		return fmt.Errorf("build Git graph: %w", err)
	}
	detector, err := scannerForContext(ctx)
	if err != nil {
		return fmt.Errorf("register detectors: %w", err)
	}
	detectorGraph, err := detector.Graph(ctx, repo, graphset.WithAttachToTree())
	if err != nil {
		return fmt.Errorf("build detector graph: %w", err)
	}
	if detectorGraph != nil {
		if err := gitGraph.Absorb(detectorGraph); err != nil {
			return fmt.Errorf("merge detector graph: %w", err)
		}
	}
	var runSelectors map[graph.EntityID]string
	if printOptions.IncludeRunTargets {
		runSelectors, err = graphRunTargetSelectors(ctx, repo)
		if err != nil {
			return fmt.Errorf("discover run targets: %w", err)
		}
	}
	return printGitGraph(w, repo, gitGraph, relativePath, printOptions, detector, runSelectors)
}

func graphRunTargetSelectors(ctx context.Context, repo *attegit.Repo) (map[graph.EntityID]string, error) {
	selectors := make(map[graph.EntityID]string)
	detector, err := scannerForContext(ctx)
	if err != nil {
		return nil, err
	}
	targets, err := detector.Targets(ctx, repo)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		value, ok := detector.TargetString(target)
		if ok {
			selectors[target.ID] = value
		}
	}
	return selectors, nil
}

var (
	workingTreeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	plainStyle       = lipgloss.NewStyle().Faint(true)
	atteHCLStyle     = lipgloss.NewStyle().Bold(true)
	goPackageStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ADD8"))
	stdlibStyle      = lipgloss.NewStyle().Faint(true)
)

func printGitGraph(
	w io.Writer,
	repo *attegit.Repo,
	g *graph.Graph,
	relativePath string,
	options PrintGraphOptions,
	detector detector.Scanner,
	runSelectors ...map[graph.EntityID]string,
) error {
	var selectors map[graph.EntityID]string
	if len(runSelectors) > 0 {
		selectors = runSelectors[0]
	}
	tree := treeprint.New()
	if err := addGraphChildren(tree, repo, g, func() graph.EntityID {
		if relativePath == "" {
			return attegit.EntityID(reference.Root)
		}
		tree, _ := reference.ParseTree(relativePath)
		return attegit.EntityID(tree)
	}(), options, detector, selectors); err != nil {
		return err
	}
	_, err := fmt.Fprint(w, tree.String())
	return err
}

func isHCLTargetKind(kind string) bool {
	return strings.HasPrefix(kind, attehcl.Namespace+":")
}

func isGraphChild(kind string) bool {
	if isHCLTargetKind(kind) {
		return true
	}
	switch kind {
	case attego.PackageKind, attego.PackageTestKind:
		return true
	default:
		return false
	}
}

func graphHCLTargetLabel(id graph.EntityID, detector detector.Scanner) (string, error) {
	kind, _, _, err := attehcl.DecodeEntityID(id, detector)
	if err != nil {
		return "", fmt.Errorf("decode HCL entity %q: %w", id, err)
	}
	return strings.TrimPrefix(kind, attehcl.Namespace+":") + " " + string(id), nil
}

// isRunTarget reports whether a discovered target belongs in run-target graph output.
func isRunTarget(includeRunTargets bool, targetSelector string) bool {
	return includeRunTargets && targetSelector != ""
}

func addGraphChildren(
	parent treeprint.Tree,
	repo *attegit.Repo,
	g *graph.Graph,
	parentID graph.EntityID,
	options PrintGraphOptions,
	detector detector.Scanner,
	runSelectors ...map[graph.EntityID]string,
) error {
	var selectors map[graph.EntityID]string
	if len(runSelectors) > 0 {
		selectors = runSelectors[0]
	}
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	if !g.Has(parentID) {
		return fmt.Errorf("parent entity %q not found", parentID)
	}
	children := make([]graph.EntityID, 0)
	seen := make(map[graph.EntityID]struct{})
	for _, id := range g.EntityKeys {
		for _, relation := range g.Out(id) {
			if relation.Kind != attegit.ContainsRelation {
				continue
			}
			var child graph.EntityID
			switch {
			case relation.To == parentID:
				child = id
			case id == parentID && (isGraphChild(string(g.Entities[relation.To].Kind)) ||
				isRunTarget(options.IncludeRunTargets, selectors[relation.To])):
				child = relation.To
			default:
				continue
			}
			if _, ok := seen[child]; !ok {
				children = append(children, child)
				seen[child] = struct{}{}
			}
		}
	}
	slices.Sort(children)
	for _, id := range children {
		entity := g.Entities[id]
		if options.IncludeRunTargets && !isHCLTargetKind(string(entity.Kind)) && entity.Kind != attego.PackageTestKind {
			if value, ok := selectors[id]; ok {
				parent.AddBranch(value)
				continue
			}
		}
		if isHCLTargetKind(string(entity.Kind)) {
			label, err := graphHCLTargetLabel(id, detector)
			if err != nil {
				return err
			}
			if options.IncludeRunTargets {
				if value, ok := selectors[id]; ok {
					label = value
				}
			}
			addTargetNode(parent, repo, g, id, label, options, detector)
			continue
		}
		switch entity.Kind {
		case attegit.TreeKind:
			entityPath, err := attegit.EntityPath(id)
			if err != nil {
				return fmt.Errorf("decode Git entity %q: %w", id, err)
			}
			branch := parent.AddBranch(plainStyle.Render(path.Base(entityPath.String()) + "/"))
			if err := addGraphChildren(branch, repo, g, id, options, detector, selectors); err != nil {
				return err
			}
		case attegit.BlobKind:
			if err := addBlobNode(parent, repo, id); err != nil {
				return err
			}
		case attego.PackageKind:
			if err := addPackageNode(parent, repo, g, id, options); err != nil {
				return err
			}
		case attego.PackageTestKind:
			if options.IncludeRunTargets {
				if selector, ok := selectors[id]; ok {
					parent.AddBranch(selector)
					break
				}
			}
			if err := addPackageNode(parent, repo, g, id, options); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported entity kind %q for %q", entity.Kind, id)
		}
	}
	return nil
}

func addTargetNode(
	parent treeprint.Tree,
	repo *attegit.Repo,
	g *graph.Graph,
	id graph.EntityID,
	label string,
	options PrintGraphOptions,
	detector detector.Scanner,
) {
	dependencies := make([]graph.EntityID, 0)
	for _, relation := range g.Out(id) {
		if relation.Kind == attehcl.DependsOnRelation {
			dependencies = append(dependencies, relation.To)
		}
	}
	label = atteHCLStyle.Render(label)
	if len(dependencies) == 0 {
		parent.AddNode(label)
		return
	}

	branch := parent.AddBranch(label)
	for _, dependency := range dependencies {
		if g.Entities[dependency].Kind == attego.PackageKind || g.Entities[dependency].Kind == attego.PackageTestKind {
			if err := addPackageNode(branch, repo, g, dependency, options); err != nil {
				branch.AddNode(graphDependencyLabel(g, dependency, detector))
			}
			continue
		}
		branch.AddNode(graphDependencyLabel(g, dependency, detector))
	}
}

func graphDependencyLabel(g *graph.Graph, id graph.EntityID, detector detector.Scanner) string {
	entity := g.Entities[id]
	if isHCLTargetKind(string(entity.Kind)) {
		kind, _, name, err := attehcl.DecodeEntityID(id, detector)
		if err == nil {
			return atteHCLStyle.Render(strings.TrimPrefix(kind, attehcl.Namespace+":") + " " + name)
		}
	}
	switch entity.Kind {
	case attego.PackageKind, attego.PackageTestKind:
		_, _, importPath, err := attego.DecodeEntityID(id)
		if err == nil {
			label := "go package"
			if entity.Kind == attego.PackageTestKind {
				label = "go package-test"
			}
			return goPackageStyle.Render(label + " " + importPath)
		}
	case attegit.BlobKind:
		if dependencyPath, err := entityPathString(id); err == nil {
			return plainStyle.Render(dependencyPath)
		}
	}
	return string(id)
}

func entityPath(id graph.EntityID) (reference.Path, error) {
	return attegit.EntityPath(id)
}

func entityPathString(id graph.EntityID) (string, error) {
	p, err := entityPath(id)
	if err != nil {
		return "", err
	}
	return p.String(), nil
}

func addBlobNode(parent treeprint.Tree, repo *attegit.Repo, id graph.EntityID) error {
	p, err := entityPath(id)
	if err != nil {
		return fmt.Errorf("decode Git blob %q: %w", id, err)
	}
	blob, err := reference.ParseBlob(p.String())
	if err != nil {
		return fmt.Errorf("decode Git blob path %q: %w", id, err)
	}
	label := path.Base(blob.String())
	if obj, ok := repo.Obj[blob]; ok && obj.Source == attegit.WorkingTreeSource {
		label = workingTreeStyle.Render(label)
	} else {
		label = plainStyle.Render(label)
	}
	parent.AddNode(label)
	return nil
}

func addPackageNode(parent treeprint.Tree, repo *attegit.Repo, g *graph.Graph, id graph.EntityID, options PrintGraphOptions) error {
	label := "go package"
	if g.Entities[id].Kind == attego.PackageTestKind {
		label = "go package-test"
	}
	_, _, importPath, err := attego.DecodeEntityID(id)
	if err != nil {
		return fmt.Errorf("decode Go package %q: %w", id, err)
	}
	localImports, stdlibImports, externalImports := packageImports(g, id)
	if !options.IncludeExternalImports {
		stdlibImports = nil
		externalImports = nil
	}
	files := packageFiles(g, id, options.IncludeGoFiles)
	if len(localImports) == 0 && len(stdlibImports) == 0 && len(externalImports) == 0 && len(files) == 0 {
		parent.AddNode(goPackageStyle.Render(fmt.Sprintf("%s %s", label, importPath)))
		return nil
	}
	branch := parent.AddBranch(goPackageStyle.Render(fmt.Sprintf("%s %s", label, importPath)))
	for _, file := range files {
		branch.AddNode("file " + file)
	}
	for _, imported := range localImports {
		branch.AddNode("import " + imported)
	}
	for _, imported := range stdlibImports {
		branch.AddNode(stdlibStyle.Render("std import " + imported))
	}
	for _, imported := range externalImports {
		branch.AddNode("external import " + imported)
	}
	return nil
}

func packageImports(g *graph.Graph, source graph.EntityID) ([]string, []string, []string) {
	local := make(map[string]struct{})
	stdlib := make(map[string]struct{})
	external := make(map[string]struct{})
	for _, relation := range g.Out(source) {
		if relation.Kind != attego.ImportsRelation || !g.Has(relation.To) {
			continue
		}
		entity := g.Entities[relation.To]
		_, _, importPath, err := attego.DecodeEntityID(relation.To)
		if err != nil {
			continue
		}
		switch entity.Kind {
		case attego.PackageKind:
			if hasContainment(g, relation.To) {
				local[importPath] = struct{}{}
			}
		case attego.PackageStdlibKind:
			stdlib[importPath] = struct{}{}
		case attego.PackageExternalKind:
			external[importPath] = struct{}{}
		}
	}
	return sortedStrings(local), sortedStrings(stdlib), sortedStrings(external)
}

func packageFiles(g *graph.Graph, source graph.EntityID, enabled bool) []string {
	if !enabled {
		return nil
	}
	files := make([]string, 0)
	for _, relation := range g.Out(source) {
		if relation.Kind != attego.SourceFileRelation || !g.Has(relation.To) || g.Entities[relation.To].Kind != attegit.BlobKind {
			continue
		}
		filePath, err := entityPathString(relation.To)
		if err != nil {
			continue
		}
		files = append(files, path.Base(filePath))
	}
	sort.Strings(files)
	return files
}

func sortedStrings(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func hasContainment(g *graph.Graph, id graph.EntityID) bool {
	for _, relation := range g.Out(id) {
		if relation.Kind == attegit.ContainsRelation || relation.Kind == attego.SourceFileRelation {
			return true
		}
	}
	return false
}
