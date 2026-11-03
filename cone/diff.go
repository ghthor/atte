package cone

import (
	"errors"
	"fmt"

	"github.com/ghthor/atte/detector/graph"
)

// Delta is the Broad Phase output: the Cone of the change from Base to Head,
// found by subtracting the base content hashes from the head's.
type Delta struct {
	Base *Snapshot
	Head *Snapshot

	// Changed is the Cone: every head entity that base does not have or whose
	// content hash differs from base, sorted bytewise.
	Changed []graph.EntityID

	// Added is the subset of Changed that base does not have.
	Added []graph.EntityID

	// Removed is every base entity that head does not have, sorted bytewise.
	// These entities are not in Head.Graph, so they are not in Graph either; a
	// consumer sees them only through the Changed entities that contained or
	// depended on them.
	Removed []graph.EntityID

	// Graph is the subgraph of Head.Graph induced by Changed: the changed
	// entities and every head relationship whose two ends are both changed.
	Graph *graph.Graph

	changed map[graph.EntityID]struct{}
}

// Contains reports whether id is in the Cone.
func (d *Delta) Contains(id graph.EntityID) bool {
	_, ok := d.changed[id]
	return ok
}

// Diff subtracts the content hashes of base from those of head. Both Snapshots
// must have been captured WithContentHashes.
//
// Diff computes the hash of every head entity and of every base entity that
// head also has, so afterwards both calculators are fully memoized.
func Diff(base, head *Snapshot) (*Delta, error) {
	if err := requireHashes("base", base); err != nil {
		return nil, err
	}
	if err := requireHashes("head", head); err != nil {
		return nil, err
	}

	delta := &Delta{
		Base:    base,
		Head:    head,
		Changed: make([]graph.EntityID, 0),
		Added:   make([]graph.EntityID, 0),
		Removed: make([]graph.EntityID, 0),
		changed: make(map[graph.EntityID]struct{}),
	}
	// EntityKeys is sorted bytewise, so the results are too.
	for _, id := range head.Graph.EntityKeys {
		headSum, err := head.Hashes.Sum(id)
		if err != nil {
			return nil, fmt.Errorf("head content hash of %q: %w", id, err)
		}
		if !base.Graph.Has(id) {
			delta.Added = append(delta.Added, id)
			delta.markChanged(id)
			continue
		}
		baseSum, err := base.Hashes.Sum(id)
		if err != nil {
			return nil, fmt.Errorf("base content hash of %q: %w", id, err)
		}
		if baseSum != headSum {
			delta.markChanged(id)
		}
	}
	for _, id := range base.Graph.EntityKeys {
		if !head.Graph.Has(id) {
			delta.Removed = append(delta.Removed, id)
		}
	}

	induced, err := induce(head.Graph, delta.changed, delta.Changed)
	if err != nil {
		return nil, fmt.Errorf("build the graph of changed entities: %w", err)
	}
	delta.Graph = induced
	return delta, nil
}

func (d *Delta) markChanged(id graph.EntityID) {
	d.Changed = append(d.Changed, id)
	d.changed[id] = struct{}{}
}

func requireHashes(name string, snapshot *Snapshot) error {
	if snapshot == nil || snapshot.Graph == nil {
		return fmt.Errorf("%s snapshot is empty", name)
	}
	if snapshot.Hashes == nil {
		return errors.New(name + " snapshot has no content hashes; capture it WithContentHashes")
	}
	return nil
}

// induce returns the subgraph of g induced by the entities in keep, listed in
// order.
func induce(g *graph.Graph, keep map[graph.EntityID]struct{}, order []graph.EntityID) (*graph.Graph, error) {
	entities := make([]graph.Entity, 0, len(order))
	relationships := make([]graph.Relationship, 0, len(order))
	for _, id := range order {
		entities = append(entities, g.Entities[id])
		for _, relationship := range g.Out(id) {
			if _, ok := keep[relationship.To]; ok {
				relationships = append(relationships, relationship)
			}
		}
	}
	return graph.New(entities, relationships)
}
