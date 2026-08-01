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

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/graph"
	"github.com/spf13/cobra"
	"github.com/xlab/treeprint"
)

var (
	graphRef             string
	graphWorkingTree     bool
	graphExternalImports bool
)

type PrintGraphOptions struct {
	IncludeExternalImports bool
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
		return printGraph(cmd.OutOrStdout(), repo, relative, PrintGraphOptions{IncludeExternalImports: graphExternalImports})
	},
}

func init() {
	rootCmd.AddCommand(graphCmd)
	graphCmd.Flags().StringVarP(&graphRef, "ref", "r", "HEAD", "Git revision to print")
	graphCmd.Flags().BoolVar(&graphWorkingTree, "working-tree", false, "Include modified and non-ignored untracked files")
	graphCmd.Flags().BoolVar(&graphExternalImports, "external-imports", false, "Include external Go imports")
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
	return printGitGraph(w, gitGraph, relativePath, printOptions)
}

func printGitGraph(w io.Writer, g *graph.Graph, relativePath string, options PrintGraphOptions) error {
	tree := treeprint.New()
	if err := addGraphChildren(tree, g, attegit.EntityID(attegit.Path(relativePath)), options); err != nil {
		return err
	}
	_, err := fmt.Fprint(w, tree.String())
	return err
}

func addGraphChildren(parent treeprint.Tree, g *graph.Graph, parentID graph.EntityID, options PrintGraphOptions) error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	if !g.Has(parentID) {
		return fmt.Errorf("parent entity %q not found", parentID)
	}
	children := make([]graph.EntityID, 0)
	for _, id := range g.EntityKeys {
		for _, relation := range g.Out(id) {
			if relation.Kind == attegit.ContainsRelation && relation.To == parentID {
				children = append(children, id)
				break
			}
		}
	}
	sort.Slice(children, func(i, j int) bool { return children[i] < children[j] })
	for _, id := range children {
		entity := g.Entities[id]
		switch entity.Kind {
		case attegit.TreeKind:
			branch := parent.AddBranch(path.Base(string(attegit.EntityPath(id))) + "/")
			if err := addGraphChildren(branch, g, id, options); err != nil {
				return err
			}
		case attegit.BlobKind:
			parent.AddNode(path.Base(string(attegit.EntityPath(id))))
		case attego.PackageKind, attego.PackageTestKind:
			label := "go package"
			if entity.Kind == attego.PackageTestKind {
				label = "go package-test"
			}
			_, _, importPath, err := attego.DecodeEntityID(id)
			if err != nil {
				return fmt.Errorf("decode Go package %q: %w", id, err)
			}
			localImports, external := packageImports(g, id)
			if !options.IncludeExternalImports {
				external = nil
			}
			if len(localImports) == 0 && len(external) == 0 {
				parent.AddNode(fmt.Sprintf("%s %s", label, importPath))
				continue
			}
			branch := parent.AddBranch(fmt.Sprintf("%s %s", label, importPath))
			for _, imported := range localImports {
				branch.AddNode("import " + imported)
			}
			for _, imported := range external {
				branch.AddNode("external import " + imported)
			}
		default:
			return fmt.Errorf("unsupported entity kind %q for %q", entity.Kind, id)
		}
	}
	return nil
}

func packageImports(g *graph.Graph, source graph.EntityID) ([]string, []string) {
	local := make(map[string]struct{})
	external := make(map[string]struct{})
	for _, relation := range g.Out(source) {
		if relation.Kind != attego.ImportsRelation || !g.Has(relation.To) {
			continue
		}
		entity := g.Entities[relation.To]
		if entity.Kind != attego.PackageKind {
			continue
		}
		_, _, importPath, err := attego.DecodeEntityID(relation.To)
		if err != nil {
			continue
		}
		if hasContainment(g, relation.To) {
			local[importPath] = struct{}{}
		} else {
			external[importPath] = struct{}{}
		}
	}
	return sortedStrings(local), sortedStrings(external)
}

func sortedStrings(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func externalImports(g *graph.Graph, source graph.EntityID) []string {
	seen := map[string]struct{}{}
	for _, relation := range g.Out(source) {
		if relation.Kind != attego.ImportsRelation || !g.Has(relation.To) {
			continue
		}
		entity := g.Entities[relation.To]
		if entity.Kind != attego.PackageKind || hasContainment(g, relation.To) {
			continue
		}
		_, _, importPath, err := attego.DecodeEntityID(relation.To)
		if err == nil {
			seen[importPath] = struct{}{}
		}
	}
	imports := make([]string, 0, len(seen))
	for importPath := range seen {
		imports = append(imports, importPath)
	}
	sort.Strings(imports)
	return imports
}

func hasContainment(g *graph.Graph, id graph.EntityID) bool {
	for _, relation := range g.Out(id) {
		if relation.Kind == attegit.ContainsRelation {
			return true
		}
	}
	return false
}
