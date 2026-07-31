// Package propagation implements the Broad Phase of Atte.
package propagation

import (
	"fmt"
	"slices"

	"github.com/ghthor/atte/graph"
)

type Impulse struct {
	Entity graph.EntityID
	Reason string
}

type Cone struct {
	Candidates []graph.EntityID
	Depth      map[graph.EntityID]int
	Frontiers  [][]graph.EntityID
}

func (c *Cone) Contains(id graph.EntityID) bool { _, ok := c.Depth[id]; return ok }

func Broad(g *graph.Graph, impulses []Impulse) (*Cone, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	depth := make(map[graph.EntityID]int)
	frontier := make([]graph.EntityID, 0, len(impulses))
	for _, impulse := range impulses {
		if !g.Has(impulse.Entity) {
			return nil, fmt.Errorf("impulse references unknown entity %q", impulse.Entity)
		}
		if _, ok := depth[impulse.Entity]; !ok {
			depth[impulse.Entity] = 0
			frontier = append(frontier, impulse.Entity)
		}
	}
	slices.Sort(frontier)
	frontiers := make([][]graph.EntityID, 0, 1)
	if len(frontier) > 0 {
		frontiers = append(frontiers, append([]graph.EntityID(nil), frontier...))
	}
	for len(frontier) > 0 {
		next := make([]graph.EntityID, 0)
		for _, id := range frontier {
			for _, relation := range g.Out(id) {
				if _, ok := depth[relation.To]; ok {
					continue
				}
				depth[relation.To] = depth[id] + 1
				next = append(next, relation.To)
			}
		}
		slices.Sort(next)
		if len(next) > 0 {
			frontiers = append(frontiers, append([]graph.EntityID(nil), next...))
		}
		frontier = next
	}
	candidates := make([]graph.EntityID, 0, len(depth))
	for id := range depth {
		candidates = append(candidates, id)
	}
	slices.Sort(candidates)
	return &Cone{Candidates: candidates, Depth: depth, Frontiers: frontiers}, nil
}
