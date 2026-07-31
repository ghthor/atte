// Package graph models the Universe as directed relationships between entities.
package graph

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type EntityID string

type Entity struct {
	ID   EntityID
	Kind string
}
type RelationKind string
type Relationship struct {
	From EntityID
	To   EntityID
	Kind RelationKind
}

type Graph struct {
	Entities   map[EntityID]Entity
	EntityKeys []EntityID
	out        map[EntityID][]Relationship
}

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

func (g *Graph) Has(id EntityID) bool              { _, ok := g.Entities[id]; return ok }
func (g *Graph) Entity(id EntityID) (Entity, bool) { entity, ok := g.Entities[id]; return entity, ok }
func (g *Graph) Out(id EntityID) []Relationship    { return append([]Relationship(nil), g.out[id]...) }
