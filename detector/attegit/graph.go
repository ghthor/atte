package attegit

import (
	"fmt"
	"strings"

	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
)

const (
	// Namespace is the graph ID namespace used by the Git detector.
	Namespace = "attegit"
	// TreeKind identifies Git tree entities in the graph.
	TreeKind = Namespace + ":tree"
	// BlobKind identifies Git blob entities in the graph.
	BlobKind = Namespace + ":blob"
	// ContainsRelation identifies Git containment relationships.
	ContainsRelation graph.RelationKind = "contains"
)

const entityPrefix = Namespace + ":"

// EntityID returns the graph ID for a repository-relative path.
func EntityID(p reference.Path) graph.EntityID {
	return graph.EntityID(entityPrefix + p.String())
}

// EntityPath returns the repository-relative path represented by an entity ID.
func EntityPath(id graph.EntityID) (reference.Path, error) {
	if !strings.HasPrefix(string(id), entityPrefix) {
		return nil, fmt.Errorf("invalid %s entity ID %q", Namespace, id)
	}
	value := strings.TrimPrefix(string(id), entityPrefix)
	if value == "" {
		return reference.Root, nil
	}
	return reference.ParsePath(value)
}

type Graphs struct {
	Full *graph.Graph
	Tree *graph.Graph
}

// Graph converts the repository tree into the language-agnostic propagation
// graph. Containment relationships point from a child to its containing tree.
func (r *Repo) Graph() (*graph.Graph, error) {
	if r.fullGraph != nil {
		return r.fullGraph, nil
	}

	type g struct {
		nodes []graph.Entity
		edges []graph.Relationship
	}
	var (
		l1   = len(r.Obj)
		l2   = l1 + len(r.Tree)
		full = g{
			nodes: make([]graph.Entity, 0, l1),
			edges: make([]graph.Relationship, 0, l2),
		}
		tree = g{
			nodes: make([]graph.Entity, 0, l1),
			edges: make([]graph.Relationship, 0, l2),
		}
	)

	root := graph.Entity{ID: EntityID(reference.Root), Kind: TreeKind}
	tree.nodes = append(tree.nodes, root)
	full.nodes = append(full.nodes, root)

	for _, p := range r.ObjKeys {
		o := r.Obj[p]
		parent := parentTree(p, o.Kind)
		if _, ok := r.Tree[parent]; !ok {
			return nil, fmt.Errorf("parent tree %q not found for %q", parent, p)
		}

		rel := graph.Relationship{
			From: EntityID(p),
			To:   EntityID(parent),
			Kind: ContainsRelation,
		}
		e := graph.Entity{ID: EntityID(p), Kind: BlobKind}
		if o.Kind == Tree {
			e.Kind = TreeKind
			tree.nodes = append(tree.nodes, e)
			tree.edges = append(tree.edges, rel)
		}
		full.nodes = append(full.nodes, e)
		full.edges = append(full.edges, rel)
	}

	var err error
	r.treeGraph, err = graph.New(tree.nodes, tree.edges)
	if err != nil {
		return nil, err
	}
	r.fullGraph, err = graph.New(full.nodes, full.edges)
	if err != nil {
		return nil, err
	}
	return r.fullGraph, nil
}

func (r *Repo) TreeGraph() (*graph.Graph, error) {
	if r.treeGraph != nil {
		return r.treeGraph, nil
	}

	_, err := r.Graph()
	if err != nil {
		return nil, err
	}

	return r.treeGraph, nil
}
