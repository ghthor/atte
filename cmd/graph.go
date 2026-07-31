/*
Copyright © 2026 Will Owens

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
package cmd

import (
	"fmt"
	"io"
	"os"
	"path"
	"sort"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/graph"
	"github.com/spf13/cobra"
	"github.com/xlab/treeprint"
)

var (
	graphRef         string
	graphWorkingTree bool
)

// graphCmd represents the graph command.
var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Print the Git tree for the working directory",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		repositoryPath, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		var options []attegit.OpenOption
		if graphWorkingTree {
			options = append(options, attegit.WithWorkingTree())
		}
		repo, err := attegit.Open(repositoryPath, graphRef, options...)
		if err != nil {
			return err
		}
		return printGraph(cmd.OutOrStdout(), repo)
	},
}

func init() {
	rootCmd.AddCommand(graphCmd)
	graphCmd.Flags().StringVarP(&graphRef, "ref", "r", "HEAD", "Git revision to print")
	graphCmd.Flags().BoolVar(&graphWorkingTree, "working-tree", false, "Include modified and non-ignored untracked files")
}

func printGraph(w io.Writer, repo *attegit.Repo) error {
	tree := treeprint.New()
	g, err := repo.Graph()
	if err != nil {
		return fmt.Errorf("build graph: %w", err)
	}
	if err := addGraphChildren(tree, g, attegit.EntityID("")); err != nil {
		return err
	}
	_, err = fmt.Fprint(w, tree.String())
	return err
}

func addGraphChildren(parent treeprint.Tree, g *graph.Graph, parentID graph.EntityID) error {
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
		entity, ok := g.Entity(id)
		if !ok {
			return fmt.Errorf("child entity %q not found", id)
		}
		name := path.Base(string(attegit.EntityPath(id)))
		if entity.Kind == attegit.TreeKind {
			branch := parent.AddBranch(name + "/")
			if err := addGraphChildren(branch, g, id); err != nil {
				return err
			}
			continue
		}
		if entity.Kind != attegit.BlobKind {
			return fmt.Errorf("unsupported entity kind %q for %q", entity.Kind, id)
		}
		parent.AddNode(name)
	}
	return nil
}
