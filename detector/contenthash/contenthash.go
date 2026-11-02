// Package contenthash computes deterministic content hashes for graph
// entities.
//
// The hash of an entity covers the entity itself and the flattened set of every
// entity it transitively depends on. It is not a Merkle hash: a dependency's
// own hash is never an input, so restructuring edges without changing the
// reachable set leaves every hash unchanged.
package contenthash

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"slices"
	"sync"

	"github.com/ghthor/atte/detector/graph"
)

// encodingPrefix is hashed first. Its version lets the encoding change without
// silently reusing old hashes.
const encodingPrefix = "atte-content-sha\x00v1"

// Sum is a content hash.
type Sum [sha256.Size]byte

// String returns the lowercase hex encoding of s.
func (s Sum) String() string { return hex.EncodeToString(s[:]) }

// Calculator computes content hashes over one graph. The graph must be complete:
// the hash of an entity does not depend on which part of the graph is printed.
//
// Dependency sets are computed lazily and memoized per strongly connected
// component, so every dependent of an entity reuses its set. A Calculator is
// safe for concurrent use.
type Calculator struct {
	graph  *graph.Graph
	follow map[graph.RelationKind]struct{}
	tokens map[graph.EntityID]string
	index  map[graph.EntityID]int32

	mu       sync.Mutex
	adj      [][]int32 // dependency indexes per entity, valid when adjDone
	adjDone  []bool
	num      []int32 // Tarjan discovery number, 0 when unvisited
	low      []int32
	onStack  []bool
	stack    []int32
	counter  int32
	comp     []int32   // component of each finished entity, -1 until finished
	closures [][]int32 // sorted entity indexes reachable from a component, members included
}

// New returns a Calculator over g. Only relationships whose kind is in
// dependencies are followed; an edge From -> To means From depends on To.
// tokens maps an entity to its content token, and entities without an entry
// have an empty token.
func New(g *graph.Graph, dependencies []graph.RelationKind, tokens map[graph.EntityID]string) *Calculator {
	c := &Calculator{
		graph:   g,
		follow:  make(map[graph.RelationKind]struct{}, len(dependencies)),
		tokens:  tokens,
		index:   make(map[graph.EntityID]int32, len(g.EntityKeys)),
		adj:     make([][]int32, len(g.EntityKeys)),
		adjDone: make([]bool, len(g.EntityKeys)),
		num:     make([]int32, len(g.EntityKeys)),
		low:     make([]int32, len(g.EntityKeys)),
		onStack: make([]bool, len(g.EntityKeys)),
		comp:    make([]int32, len(g.EntityKeys)),
	}
	for _, kind := range dependencies {
		c.follow[kind] = struct{}{}
	}
	for i, id := range g.EntityKeys {
		c.index[id] = int32(i)
		c.comp[i] = -1
	}
	return c
}

// Token returns the content token of id, which is empty for entities that have
// no content of their own.
func (c *Calculator) Token(id graph.EntityID) string { return c.tokens[id] }

// Closure returns every entity id reachable from id over dependency edges,
// excluding id itself, sorted bytewise.
func (c *Calculator) Closure(id graph.EntityID) ([]graph.EntityID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.resolve(id)
	if err != nil {
		return nil, err
	}
	return c.closureOf(v), nil
}

// Sum returns the content hash of id.
func (c *Calculator) Sum(id graph.EntityID) (Sum, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.resolve(id)
	if err != nil {
		return Sum{}, err
	}
	closure := c.closureOf(v)

	h := sha256.New()
	h.Write([]byte(encodingPrefix))
	c.writeEntry(h, id)
	var length [binary.MaxVarintLen64]byte
	h.Write(length[:binary.PutUvarint(length[:], uint64(len(closure)))])
	for _, dependency := range closure {
		c.writeEntry(h, dependency)
	}
	var sum Sum
	h.Sum(sum[:0])
	return sum, nil
}

// writeEntry writes the length-prefixed id and token of one entity.
func (c *Calculator) writeEntry(h hash.Hash, id graph.EntityID) {
	var length [binary.MaxVarintLen64]byte
	token := c.tokens[id]
	h.Write(length[:binary.PutUvarint(length[:], uint64(len(id)))])
	h.Write([]byte(id))
	h.Write(length[:binary.PutUvarint(length[:], uint64(len(token)))])
	h.Write([]byte(token))
}

func (c *Calculator) resolve(id graph.EntityID) (int32, error) {
	v, ok := c.index[id]
	if !ok {
		return 0, fmt.Errorf("unknown entity %q", id)
	}
	if c.comp[v] < 0 {
		c.visit(v)
	}
	return v, nil
}

// closureOf returns the sorted ids reachable from v, excluding v.
func (c *Calculator) closureOf(v int32) []graph.EntityID {
	members := c.closures[c.comp[v]]
	ids := make([]graph.EntityID, 0, len(members))
	for _, w := range members {
		if w != v {
			ids = append(ids, c.graph.EntityKeys[w])
		}
	}
	return ids
}

// dependencies returns the sorted, de-duplicated dependency indexes of v.
func (c *Calculator) dependencies(v int32) []int32 {
	if c.adjDone[v] {
		return c.adj[v]
	}
	var deps []int32
	for _, relation := range c.graph.Out(c.graph.EntityKeys[v]) {
		if _, ok := c.follow[relation.Kind]; ok {
			deps = append(deps, c.index[relation.To])
		}
	}
	slices.Sort(deps)
	c.adj[v] = slices.Compact(deps)
	c.adjDone[v] = true
	return c.adj[v]
}

// visit is Tarjan's strongly connected components algorithm. Components finish
// in reverse topological order, so every component a finishing component
// depends on already has its closure. Memoizing per component rather than per
// entity is what keeps results independent of where a walk starts: an entity
// inside a cycle never receives a set that is missing the rest of its cycle.
func (c *Calculator) visit(v int32) {
	c.counter++
	c.num[v], c.low[v] = c.counter, c.counter
	c.stack = append(c.stack, v)
	c.onStack[v] = true

	for _, w := range c.dependencies(v) {
		switch {
		case c.num[w] == 0:
			c.visit(w)
			c.low[v] = min(c.low[v], c.low[w])
		case c.onStack[w]:
			c.low[v] = min(c.low[v], c.num[w])
		}
	}
	if c.low[v] != c.num[v] {
		return
	}

	id := int32(len(c.closures))
	var members []int32
	for {
		w := c.stack[len(c.stack)-1]
		c.stack = c.stack[:len(c.stack)-1]
		c.onStack[w] = false
		c.comp[w] = id
		members = append(members, w)
		if w == v {
			break
		}
	}
	reachable := slices.Clone(members)
	for _, m := range members {
		for _, w := range c.dependencies(m) {
			if c.comp[w] != id {
				reachable = append(reachable, c.closures[c.comp[w]]...)
			}
		}
	}
	slices.Sort(reachable)
	c.closures = append(c.closures, slices.Compact(reachable))
}
