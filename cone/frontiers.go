package cone

import (
	"fmt"

	"github.com/ghthor/atte/detector/graph"
)

// Frontiers layers the Cone by how far each Candidate is from the Impulses, so
// that a consumer can tell what changed first from what changed because of it.
// It is the Frontier of the glossary at each step of Propagation, derived from
// the finished Cone rather than found by walking outward.
//
// Layer 0 holds the Impulses: every added entity, and every changed entity with
// no changed dependency. A tree's own content hash changes whenever anything
// below it does, so looking for a changed dependency, not a changed hash, is
// what keeps the trees above an edit out of layer 0. A changed cycle that
// depends on nothing else that changed is in layer 0 as a whole, since it has
// no entry point.
//
// A later layer holds the changed entities whose nearest changed dependency is
// in the layer before it. Every changed entity is in exactly one layer, and
// each layer is sorted bytewise.
//
// Dependencies are read from the head Calculator. A dependency on an entity
// that was removed is not changed, so an entity that lost a dependency is an
// Impulse unless it also depends on something that changed.
func (d *Delta) Frontiers() ([][]graph.EntityID, error) {
	n := len(d.Changed)
	index := make(map[graph.EntityID]int, n)
	for i, id := range d.Changed {
		index[id] = i
	}
	added := make(map[graph.EntityID]struct{}, len(d.Added))
	for _, id := range d.Added {
		added[id] = struct{}{}
	}

	// dependencies holds the changed dependencies of each changed entity.
	dependencies := make([][]int, n)
	for i, id := range d.Changed {
		direct, err := d.Head.Hashes.Dependencies(id)
		if err != nil {
			return nil, fmt.Errorf("dependencies of %q: %w", id, err)
		}
		for _, dependency := range direct {
			if j, ok := index[dependency]; ok {
				dependencies[i] = append(dependencies[i], j)
			}
		}
	}

	// Components come dependencies first, so the layer of every component a
	// component depends on is known when its turn comes.
	components := stronglyConnected(dependencies)
	componentOf := make([]int, n)
	for c, members := range components {
		for _, m := range members {
			componentOf[m] = c
		}
	}
	layerOf := make([]int, len(components))
	depth := 0
	for c, members := range components {
		layer, impulse := -1, false
		for _, m := range members {
			if _, ok := added[d.Changed[m]]; ok {
				impulse = true
			}
			for _, dependency := range dependencies[m] {
				if other := componentOf[dependency]; other != c && (layer < 0 || layerOf[other] < layer) {
					layer = layerOf[other]
				}
			}
		}
		if impulse || layer < 0 {
			layerOf[c] = 0
		} else {
			layerOf[c] = layer + 1
		}
		depth = max(depth, layerOf[c]+1)
	}

	if n == 0 {
		return [][]graph.EntityID{}, nil
	}
	layers := make([][]graph.EntityID, depth)
	// Changed is sorted, so each layer is too.
	for i, id := range d.Changed {
		l := layerOf[componentOf[i]]
		layers[l] = append(layers[l], id)
	}
	return layers, nil
}

// stronglyConnected returns the strongly connected components of the graph
// whose edges are adjacency, listed so that a component comes after every
// component it has an edge to (Tarjan's algorithm).
func stronglyConnected(adjacency [][]int) [][]int {
	n := len(adjacency)
	var (
		counter    int
		number     = make([]int, n) // discovery number, 0 when unvisited
		low        = make([]int, n)
		onStack    = make([]bool, n)
		stack      []int
		components [][]int
	)
	var visit func(v int)
	visit = func(v int) {
		counter++
		number[v], low[v] = counter, counter
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adjacency[v] {
			switch {
			case number[w] == 0:
				visit(w)
				low[v] = min(low[v], low[w])
			case onStack[w]:
				low[v] = min(low[v], number[w])
			}
		}
		if low[v] != number[v] {
			return
		}
		var members []int
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			members = append(members, w)
			if w == v {
				break
			}
		}
		components = append(components, members)
	}
	for v := range n {
		if number[v] == 0 {
			visit(v)
		}
	}
	return components
}
