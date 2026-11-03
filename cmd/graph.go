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
	"github.com/ghthor/atte/cone"
	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/contenthash"
	"github.com/ghthor/atte/detector/graph"
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
	contentHash     bool
	goVersions      bool
}

// PrintGraphOptions controls the details included in graph output.
type PrintGraphOptions struct {
	// IncludeExternalImports includes imports that cannot be resolved inside the repository.
	IncludeExternalImports bool
	// IncludeGoFiles includes Go source files linked to packages and package tests.
	IncludeGoFiles bool
	// IncludeRunTargets renders runnable entities using atte run selectors.
	IncludeRunTargets bool
	// IncludeContentHash shows the abbreviated content hash after each tree,
	// file, package and target, and the module version of external imports.
	IncludeContentHash bool
	// IncludeGoVersions shows the Go version each package's module declares.
	IncludeGoVersions bool
}

// renderOptions is what printing a graph works from: the caller's options and
// the state computed from them once the graph is built.
type renderOptions struct {
	PrintGraphOptions

	// hashes is set when content hashes or Go versions are shown.
	hashes *contenthash.Calculator

	// hashLabel, when set, replaces the abbreviated hash that follows an entity
	// when content hashes are shown. It lets a caller print something that is
	// not the hash of one revision, such as a range of two.
	hashLabel func(graph.EntityID) (string, error)

	// complete is set when the graph being printed is a subset of a larger
	// graph, as for the entities that changed between two revisions. It is the
	// graph that decides whether an imported package belongs to the repository,
	// because a subset can lack the source files that say so. Leaving it nil
	// means the graph being printed is complete.
	complete *graph.Graph
}

// contentHashLength is the number of leading hex characters of a content hash
// that graph output shows.
const contentHashLength = 12

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
				PrintGraphOptions{
					IncludeExternalImports: options.externalImports,
					IncludeGoFiles:         options.goFiles,
					IncludeRunTargets:      options.runTargets,
					IncludeContentHash:     options.contentHash,
					IncludeGoVersions:      options.goVersions,
				},
			)
		},
	}
	graphCmd.Flags().StringVarP(&options.ref, "ref", "r", "HEAD", "Git revision to print")
	graphCmd.Flags().BoolVar(&options.workingTree, "working-tree", false, "Include modified and non-ignored untracked files")
	graphCmd.Flags().BoolVar(&options.externalImports, "external-imports", false, "Include external Go imports")
	graphCmd.Flags().BoolVar(&options.goFiles, "go-files", false, "Include Go source files")
	graphCmd.Flags().BoolVar(&options.runTargets, "run-targets", false, "Render runnable entities as atte run selectors")
	graphCmd.Flags().BoolVar(&options.contentHash, "content-hash", false, "Show the content hash of each entity")
	graphCmd.Flags().BoolVar(&options.goVersions, "go-versions", false, "Show the Go version each package's module declares")
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
	// Git reports the canonical repository root; canonicalize cwd as well so
	// symlinked paths (for example /var -> /private/var on macOS) stay relative.
	realCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", "", fmt.Errorf("resolve working directory: %w", err)
	}
	relative, err := filepath.Rel(root, realCwd)
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
	detector, err := scannerForContext(ctx)
	if err != nil {
		return fmt.Errorf("register detectors: %w", err)
	}
	// Hashes cover the whole graph, so an entity prints the same hash from any
	// working directory. They are computed only when shown: content tokens are
	// strict, and plain graph output must work for a repository whose external
	// imports cannot all be resolved.
	var captureOptions []cone.CaptureOption
	if printOptions.IncludeContentHash || printOptions.IncludeGoVersions {
		captureOptions = append(captureOptions, cone.WithContentHashes())
	}
	snapshot, err := cone.Capture(ctx, detector, repo, captureOptions...)
	if err != nil {
		return err
	}
	render := renderOptions{PrintGraphOptions: printOptions, hashes: snapshot.Hashes}
	var runSelectors map[graph.EntityID]string
	if printOptions.IncludeRunTargets {
		runSelectors, err = graphRunTargetSelectors(ctx, repo)
		if err != nil {
			return fmt.Errorf("discover run targets: %w", err)
		}
	}
	return printGitGraph(w, repo, snapshot.Graph, relativePath, render, detector, runSelectors)
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
	options renderOptions,
	detector detector.Scanner,
	runSelectors ...map[graph.EntityID]string,
) error {
	var selectors map[graph.EntityID]string
	if len(runSelectors) > 0 {
		selectors = runSelectors[0]
	}
	start := attegit.EntityID(reference.Root)
	if relativePath != "" {
		tree, _ := reference.ParseTree(relativePath)
		start = attegit.EntityID(tree)
	}
	// A subset has no tree where nothing below it changed, which is an empty
	// result. A complete graph has every tree, so a missing one is an error.
	if options.complete != nil && !g.Has(start) {
		return nil
	}
	tree := treeprint.New()
	if err := addGraphChildren(tree, repo, g, start, options, detector, selectors); err != nil {
		return err
	}
	_, err := fmt.Fprint(w, tree.String())
	return err
}

// hashSuffix returns the text appended to the label of id: a space and the
// abbreviated content hash, or nothing when hashes are not shown.
func (options renderOptions) hashSuffix(id graph.EntityID) (string, error) {
	if !options.IncludeContentHash {
		return "", nil
	}
	if options.hashLabel != nil {
		label, err := options.hashLabel(id)
		if err != nil {
			return "", err
		}
		return " " + plainStyle.Render(label), nil
	}
	sum, err := options.hashes.Sum(id)
	if err != nil {
		return "", fmt.Errorf("content hash of %q: %w", id, err)
	}
	return " " + plainStyle.Render(sum.String()[:contentHashLength]), nil
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
	options renderOptions,
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
	for _, relation := range g.Out(parentID) {
		if relation.Kind != attegit.ContainsRelation {
			continue
		}
		kind := g.Entities[relation.To].Kind
		if kind == attegit.TreeKind || kind == attegit.BlobKind || isGraphChild(string(kind)) ||
			isRunTarget(options.IncludeRunTargets, selectors[relation.To]) {
			children = append(children, relation.To)
		}
	}
	slices.Sort(children)
	for _, id := range children {
		entity := g.Entities[id]
		if options.IncludeRunTargets && !isHCLTargetKind(string(entity.Kind)) && entity.Kind != attego.PackageTestKind {
			if value, ok := selectors[id]; ok {
				suffix, err := options.hashSuffix(id)
				if err != nil {
					return err
				}
				parent.AddBranch(value + suffix)
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
			if err := addTargetNode(parent, repo, g, id, label, options, detector); err != nil {
				return err
			}
			continue
		}
		switch entity.Kind {
		case attegit.TreeKind:
			entityPath, err := attegit.EntityPath(id)
			if err != nil {
				return fmt.Errorf("decode Git entity %q: %w", id, err)
			}
			suffix, err := options.hashSuffix(id)
			if err != nil {
				return err
			}
			branch := parent.AddBranch(plainStyle.Render(path.Base(entityPath.String())+"/") + suffix)
			if err := addGraphChildren(branch, repo, g, id, options, detector, selectors); err != nil {
				return err
			}
		case attegit.BlobKind:
			if err := addBlobNode(parent, repo, id, options); err != nil {
				return err
			}
		case attego.PackageKind:
			if err := addPackageNode(parent, repo, g, id, options); err != nil {
				return err
			}
		case attego.PackageTestKind:
			if options.IncludeRunTargets {
				if selector, ok := selectors[id]; ok {
					suffix, err := options.hashSuffix(id)
					if err != nil {
						return err
					}
					parent.AddBranch(selector + suffix)
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
	options renderOptions,
	detector detector.Scanner,
) error {
	dependencies := make([]graph.EntityID, 0)
	for _, relation := range g.Out(id) {
		if relation.Kind == attehcl.DependsOnRelation {
			dependencies = append(dependencies, relation.To)
		}
	}
	suffix, err := options.hashSuffix(id)
	if err != nil {
		return err
	}
	label = atteHCLStyle.Render(label) + suffix
	if len(dependencies) == 0 {
		parent.AddNode(label)
		return nil
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
	return nil
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

func addBlobNode(parent treeprint.Tree, repo *attegit.Repo, id graph.EntityID, options renderOptions) error {
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
	suffix, err := options.hashSuffix(id)
	if err != nil {
		return err
	}
	parent.AddNode(label + suffix)
	return nil
}

func addPackageNode(parent treeprint.Tree, repo *attegit.Repo, g *graph.Graph, id graph.EntityID, options renderOptions) error {
	label := "go package"
	if g.Entities[id].Kind == attego.PackageTestKind {
		label = "go package-test"
	}
	_, _, importPath, err := attego.DecodeEntityID(id)
	if err != nil {
		return fmt.Errorf("decode Go package %q: %w", id, err)
	}
	localImports, stdlibImports, externalImports, externalIDs := packageImports(g, options.complete, id)
	if !options.IncludeExternalImports {
		stdlibImports = nil
		externalImports = nil
	}
	files := packageFiles(g, id, options.IncludeGoFiles)
	var goVersion string
	if options.IncludeGoVersions {
		goVersion, err = packageGoVersion(g, id, options)
		if err != nil {
			return err
		}
	}
	suffix, err := options.hashSuffix(id)
	if err != nil {
		return err
	}
	packageLabel := goPackageStyle.Render(fmt.Sprintf("%s %s", label, importPath)) + suffix
	if len(localImports) == 0 && len(stdlibImports) == 0 && len(externalImports) == 0 && len(files) == 0 && goVersion == "" {
		parent.AddNode(packageLabel)
		return nil
	}
	branch := parent.AddBranch(packageLabel)
	if goVersion != "" {
		branch.AddNode(goVersion)
	}
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
		line := "external import " + imported
		if options.IncludeContentHash {
			// The module and version stand in for a hash: external packages are
			// leaves, and their module version is their whole content.
			if module, ok := strings.CutPrefix(options.hashes.Token(externalIDs[imported]), "go-module:"); ok {
				line += " " + plainStyle.Render("("+module+")")
			}
		}
		branch.AddNode(line)
	}
	return nil
}

// packageGoVersion returns the output line for the Go version of the module
// that owns the package, or "" when the package has none.
func packageGoVersion(g *graph.Graph, source graph.EntityID, options renderOptions) (string, error) {
	for _, relation := range g.Out(source) {
		if relation.Kind != attego.GoVersionRelation {
			continue
		}
		version, ok := strings.CutPrefix(options.hashes.Token(relation.To), "go-version:")
		if !ok {
			continue
		}
		suffix, err := options.hashSuffix(relation.To)
		if err != nil {
			return "", err
		}
		return "go version " + version + suffix, nil
	}
	return "", nil
}

// packageImports returns the sorted import paths of source, grouped into
// repository, standard library and external packages. It also returns the
// entity of each external import path. A package is a repository package when
// complete, or g if complete is nil, has source files or contents for it.
func packageImports(g, complete *graph.Graph, source graph.EntityID) ([]string, []string, []string, map[string]graph.EntityID) {
	if complete == nil {
		complete = g
	}
	local := make(map[string]struct{})
	stdlib := make(map[string]struct{})
	external := make(map[string]struct{})
	externalIDs := make(map[string]graph.EntityID)
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
			if hasContainment(complete, relation.To) {
				local[importPath] = struct{}{}
			}
		case attego.PackageStdlibKind:
			stdlib[importPath] = struct{}{}
		case attego.PackageExternalKind:
			external[importPath] = struct{}{}
			externalIDs[importPath] = relation.To
		}
	}
	return sortedStrings(local), sortedStrings(stdlib), sortedStrings(external), externalIDs
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
