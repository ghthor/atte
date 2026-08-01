package attegit

import (
	"fmt"
	"strings"

	"github.com/ghthor/atte/graph"
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

// Graph converts the repository tree into the language-agnostic propagation
// graph. Containment relationships point from a child to its containing tree.
func (r *Repo) Graph() (*graph.Graph, error) {
	entities := make([]graph.Entity, 0, len(r.Obj)+len(r.Tree))
	seen := make(map[reference.Path]struct{}, len(r.Obj)+len(r.Tree))
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
		parent := p.Tree()
		if r.Obj[p].Kind == Tree {
			parent = p.(reference.Tree).Parent()
		}
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
