package attegit

import (
	"fmt"
	"strings"

	"github.com/ghthor/atte/graph"
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
func EntityID(p Path) graph.EntityID {
	return graph.EntityID(entityPrefix + string(p))
}

// EntityPath returns the repository-relative path represented by an entity ID.
func EntityPath(id graph.EntityID) Path {
	return Path(strings.TrimPrefix(string(id), entityPrefix))
}

// Graph converts the repository tree into the language-agnostic propagation
// graph. Containment relationships point from a child to its containing tree.
func (r *Repo) Graph() (*graph.Graph, error) {
	entities := make([]graph.Entity, 0, len(r.Obj)+len(r.Tree))
	seen := make(map[Path]struct{}, len(r.Obj)+len(r.Tree))
	for _, p := range r.TreeKeys {
		entities = append(entities, graph.Entity{ID: EntityID(p), Kind: TreeKind})
		seen[p] = struct{}{}
	}
	for _, p := range r.ObjKeys {
		if _, ok := seen[p]; ok {
			continue
		}
		obj := r.Obj[p]
		kind := BlobKind
		if obj.Kind == Tree {
			kind = TreeKind
		}
		entities = append(entities, graph.Entity{ID: EntityID(p), Kind: kind})
		seen[p] = struct{}{}
	}

	relations := make([]graph.Relationship, 0, len(r.Obj)+len(r.Tree))
	for _, p := range r.ObjKeys {
		parent := parentPath(p)
		if _, ok := r.Tree[parent]; !ok {
			return nil, fmt.Errorf("parent tree %q not found for %q", parent, p)
		}
		relations = append(relations, graph.Relationship{
			From: EntityID(p),
			To:   EntityID(parent),
			Kind: ContainsRelation,
		})
	}
	return graph.New(entities, relations)
}
