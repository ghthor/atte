package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/graph"
	"github.com/spf13/cobra"
	"github.com/xlab/treeprint"
)

var (
	graphRef             string
	graphWorkingTree     bool
	graphExternalImports bool
	graphGoFiles         bool
)

// PrintGraphOptions controls the details included in graph output.
type PrintGraphOptions struct {
	// IncludeExternalImports includes imports that cannot be resolved inside the repository.
	IncludeExternalImports bool
	// IncludeGoFiles includes Go source files linked to packages and package tests.
	IncludeGoFiles bool
}

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Print the graph for the current directory",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		repoRoot, relative, err := repositoryContext(cwd)
		if err != nil {
			return err
		}
		var options []attegit.OpenOption
		if graphWorkingTree {
			options = append(options, attegit.WithWorkingTree())
		}
		repo, err := attegit.Open(repoRoot, graphRef, options...)
		if err != nil {
			return err
		}
		return printGraph(cmd.OutOrStdout(), repo, relative, PrintGraphOptions{IncludeExternalImports: graphExternalImports, IncludeGoFiles: graphGoFiles})
	},
}

func init() {
	rootCmd.AddCommand(graphCmd)
	graphCmd.Flags().StringVarP(&graphRef, "ref", "r", "HEAD", "Git revision to print")
	graphCmd.Flags().BoolVar(&graphWorkingTree, "working-tree", false, "Include modified and non-ignored untracked files")
	graphCmd.Flags().BoolVar(&graphExternalImports, "external-imports", false, "Include external Go imports")
	graphCmd.Flags().BoolVar(&graphGoFiles, "go-files", false, "Include Go source files")
}

func repositoryContext(cwd string) (string, string, error) {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
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

func printGraph(w io.Writer, repo *attegit.Repo, relativePath string, options ...PrintGraphOptions) error {
	var printOptions PrintGraphOptions
	if len(options) > 0 {
		printOptions = options[0]
	}
	gitGraph, err := repo.Graph()
	if err != nil {
		return fmt.Errorf("build Git graph: %w", err)
	}
	goGraph, err := attego.GraphWithContainment(repo)
	if err != nil {
		return fmt.Errorf("build Go graph: %w", err)
	}
	if err := gitGraph.Absorb(goGraph); err != nil {
		return fmt.Errorf("merge Go graph: %w", err)
	}
	hclGraph, err := attehcl.GraphWithContainment(repo)
	if err != nil {
		return fmt.Errorf("build HCL graph: %w", err)
	}
	if err := gitGraph.Absorb(hclGraph); err != nil {
		return fmt.Errorf("merge HCL graph: %w", err)
	}
	return printGitGraph(w, repo, gitGraph, relativePath, printOptions)
}

var (
	workingTreeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	goPackageStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ADD8"))
	stdlibStyle      = lipgloss.NewStyle().Faint(true)
)

func printGitGraph(w io.Writer, repo *attegit.Repo, g *graph.Graph, relativePath string, options PrintGraphOptions) error {
	tree := treeprint.New()
	if err := addGraphChildren(tree, repo, g, attegit.EntityID(attegit.Path(relativePath)), options); err != nil {
		return err
	}
	_, err := fmt.Fprint(w, tree.String())
	return err
}

func addGraphChildren(parent treeprint.Tree, repo *attegit.Repo, g *graph.Graph, parentID graph.EntityID, options PrintGraphOptions) error {
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
			case id == parentID && (g.Entities[relation.To].Kind == attego.PackageKind || g.Entities[relation.To].Kind == attego.PackageTestKind || g.Entities[relation.To].Kind == attehcl.TestKind):
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
	sort.Slice(children, func(i, j int) bool { return children[i] < children[j] })
	for _, id := range children {
		entity := g.Entities[id]
		switch entity.Kind {
		case attegit.TreeKind:
			branch := parent.AddBranch(path.Base(string(attegit.EntityPath(id))) + "/")
			if err := addGraphChildren(branch, repo, g, id, options); err != nil {
				return err
			}
		case attegit.BlobKind:
			addBlobNode(parent, repo, id)
		case attego.PackageKind, attego.PackageTestKind:
			if err := addPackageNode(parent, repo, g, id, options); err != nil {
				return err
			}
		case attehcl.TestKind:
			parent.AddBranch("test " + string(id))
		default:
			return fmt.Errorf("unsupported entity kind %q for %q", entity.Kind, id)
		}
	}
	return nil
}

func addBlobNode(parent treeprint.Tree, repo *attegit.Repo, id graph.EntityID) {
	label := path.Base(string(attegit.EntityPath(id)))
	if obj, ok := repo.Obj[attegit.EntityPath(id)]; ok && obj.Source == attegit.WorkingTreeSource {
		label = workingTreeStyle.Render(label)
	}
	parent.AddNode(label)
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
		files = append(files, path.Base(string(attegit.EntityPath(relation.To))))
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
