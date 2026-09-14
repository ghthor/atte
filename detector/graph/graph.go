// Package graph models the Universe as directed relationships between entities.
package graph

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// EntityID uniquely identifies an entity in a Graph.
type EntityID string

// EntityKind identifies the namespace-qualified kind of a graph entity.
// Entity kinds use the form "namespace:kind" when a namespace has multiple
// entity types; a detector may use its namespace alone when an ID does not
// encode a more specific kind.
type EntityKind string

// Entity describes a graph entity and its kind.
type Entity struct {
	ID EntityID

	// Kind identifies the detector-specific type of the entity.
	Kind EntityKind
}

// RelationKind identifies the meaning and direction of a relationship.
type RelationKind string

// Relationship is a directed edge from From to To.
type Relationship struct {
	From EntityID
	To   EntityID
	Kind RelationKind
}

// Namespace returns the portion of the entity ID before its first colon.
func (id EntityID) Namespace() string {
	n, _, _ := strings.Cut(string(id), ":")
	return n
}

// Graph stores entities and their outgoing relationships. EntityKeys and outgoing
// relationships are kept in deterministic order by New and graph mutations.
type Graph struct {
	Entities   map[EntityID]Entity
	EntityKeys []EntityID
	out        map[EntityID][]Relationship
}

// New validates and constructs a graph. Entity IDs and relationship kinds must
// be non-empty, and relationships must reference known entities.
func New(entities []Entity, relationships []Relationship) (*Graph, error) {
	byID := make(map[EntityID]Entity, len(entities))
	for _, entity := range entities {
		if strings.TrimSpace(string(entity.ID)) == "" {
			return nil, fmt.Errorf("entity has empty id")
		}
		if _, ok := byID[entity.ID]; ok {
			return nil, fmt.Errorf("duplicate entity id %q", entity.ID)
		}
		byID[entity.ID] = entity
	}
	out := make(map[EntityID][]Relationship, len(byID))
	for i, relation := range relationships {
		if strings.TrimSpace(string(relation.Kind)) == "" {
			return nil, fmt.Errorf("relationship %d has empty kind", i)
		}
		if _, ok := byID[relation.From]; !ok {
			return nil, fmt.Errorf("relationship references unknown entity %q", relation.From)
		}
		if _, ok := byID[relation.To]; !ok {
			return nil, fmt.Errorf("relationship references unknown entity %q", relation.To)
		}
		out[relation.From] = append(out[relation.From], relation)
	}
	for id := range out {
		slices.SortFunc(out[id], func(a, b Relationship) int {
			if c := strings.Compare(string(a.To), string(b.To)); c != 0 {
				return c
			}
			return strings.Compare(string(a.Kind), string(b.Kind))
		})
	}
	return &Graph{Entities: byID, EntityKeys: slices.Sorted(maps.Keys(byID)), out: out}, nil
}

// Absorb adds the entities and relationships from other to g. Duplicate
// entities and relationships are ignored when they are equivalent.
func (g *Graph) Absorb(other *Graph) error {
	if g == nil {
		return fmt.Errorf("cannot absorb into a nil graph")
	}
	if other == nil {
		return fmt.Errorf("cannot absorb a nil graph")
	}

	entities := make([]Entity, 0, len(g.Entities)+len(other.Entities))
	byID := make(map[EntityID]Entity, len(g.Entities)+len(other.Entities))
	for _, source := range []*Graph{g, other} {
		for _, id := range source.EntityKeys {
			entity := source.Entities[id]
			if existing, ok := byID[id]; ok {
				if existing != entity {
					return fmt.Errorf("conflicting entity %q", id)
				}
				continue
			}
			byID[id] = entity
			entities = append(entities, entity)
		}
	}

	relations := make([]Relationship, 0)
	seen := make(map[Relationship]struct{})
	for _, source := range []*Graph{g, other} {
		for _, id := range source.EntityKeys {
			for _, relation := range source.out[id] {
				if _, ok := seen[relation]; ok {
					continue
				}
				seen[relation] = struct{}{}
				relations = append(relations, relation)
			}
		}
	}

	merged, err := New(entities, relations)
	if err != nil {
		return fmt.Errorf("absorb graph: %w", err)
	}
	g.Entities = merged.Entities
	g.EntityKeys = merged.EntityKeys
	g.out = merged.out
	return nil
}

// Has reports whether id identifies an entity in g.
func (g *Graph) Has(id EntityID) bool { _, ok := g.Entities[id]; return ok }

// Entity returns the entity identified by id, if present.
func (g *Graph) Entity(id EntityID) (Entity, bool) { entity, ok := g.Entities[id]; return entity, ok }

// Out returns a copy of the relationships leaving id. Modifying the returned
// slice does not change the graph.
func (g *Graph) Out(id EntityID) []Relationship { return append([]Relationship(nil), g.out[id]...) }
