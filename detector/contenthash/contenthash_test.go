package contenthash

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/ghthor/atte/detector/graph"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

const (
	dep   graph.RelationKind = "dep"
	other graph.RelationKind = "other"
)

type edge struct {
	from, to graph.EntityID
	kind     graph.RelationKind
}

func build(t *testing.T, ids []graph.EntityID, edges []edge) *graph.Graph {
	t.Helper()
	entities := make([]graph.Entity, 0, len(ids))
	for _, id := range ids {
		entities = append(entities, graph.Entity{ID: id, Kind: "test"})
	}
	relations := make([]graph.Relationship, 0, len(edges))
	for _, e := range edges {
		relations = append(relations, graph.Relationship{From: e.from, To: e.to, Kind: e.kind})
	}
	g, err := graph.New(entities, relations)
	must.NoError(t, err)
	return g
}

func sum(t *testing.T, c *Calculator, id graph.EntityID) Sum {
	t.Helper()
	s, err := c.Sum(id)
	must.NoError(t, err)
	return s
}

func closure(t *testing.T, c *Calculator, id graph.EntityID) []graph.EntityID {
	t.Helper()
	ids, err := c.Closure(id)
	must.NoError(t, err)
	return ids
}

// reference computes a sha the slow way, from the specification: breadth-first
// reachability from the entity, sorted bytewise, hashed with the documented
// encoding.
func reference(g *graph.Graph, id graph.EntityID, tokens map[graph.EntityID]string) Sum {
	seen := map[graph.EntityID]bool{id: true}
	queue := []graph.EntityID{id}
	var reached []graph.EntityID
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, relation := range g.Out(next) {
			if relation.Kind != dep || seen[relation.To] {
				continue
			}
			seen[relation.To] = true
			reached = append(reached, relation.To)
			queue = append(queue, relation.To)
		}
	}
	slices.Sort(reached)

	var buf []byte
	buf = append(buf, "atte-content-sha\x00v1"...)
	entry := func(x graph.EntityID) {
		buf = binary.AppendUvarint(buf, uint64(len(x)))
		buf = append(buf, x...)
		buf = binary.AppendUvarint(buf, uint64(len(tokens[x])))
		buf = append(buf, tokens[x]...)
	}
	entry(id)
	buf = binary.AppendUvarint(buf, uint64(len(reached)))
	for _, x := range reached {
		entry(x)
	}
	return sha256.Sum256(buf)
}

func TestSumGoldenVector(t *testing.T) {
	// a depends on b and c; b depends on c. The expected values were computed
	// by an independent implementation of the encoding in the IDR.
	g := build(t,
		[]graph.EntityID{"a", "b", "c"},
		[]edge{{"a", "b", dep}, {"a", "c", dep}, {"b", "c", dep}},
	)
	tokens := map[graph.EntityID]string{"b": "git-blob:bb", "c": "git-blob:cc"}
	c := New(g, []graph.RelationKind{dep}, tokens)

	test.EqOp(t, "50351f9c5e26c47f102ed7aefe437c75dcf1d2fd23747e9ca1af14053d42bef4", sum(t, c, "a").String())
	test.EqOp(t, "c3238e493fe4179c7fd1f2229d6b171a38e7ca84925581e2b59a1f9c09d0c4fa", sum(t, c, "b").String())
	test.EqOp(t, "c30be22cfbd463950364d981a34253de6a197f3971188d1c9bf3facd83a3a243", sum(t, c, "c").String())
}

func TestClosureIsSortedAndExcludesEntity(t *testing.T) {
	g := build(t,
		[]graph.EntityID{"a", "b", "c", "d"},
		[]edge{{"a", "d", dep}, {"a", "b", dep}, {"b", "c", dep}},
	)
	c := New(g, []graph.RelationKind{dep}, nil)

	test.Eq(t, []graph.EntityID{"b", "c", "d"}, closure(t, c, "a"))
	test.Eq(t, []graph.EntityID{"c"}, closure(t, c, "b"))
	test.SliceEmpty(t, closure(t, c, "c"))
}

func TestClosureIgnoresUnfollowedRelations(t *testing.T) {
	g := build(t,
		[]graph.EntityID{"a", "b", "c"},
		[]edge{{"a", "b", dep}, {"a", "c", other}},
	)
	c := New(g, []graph.RelationKind{dep}, nil)

	test.Eq(t, []graph.EntityID{"b"}, closure(t, c, "a"))
	test.NotEq(t, sum(t, New(g, []graph.RelationKind{dep, other}, nil), "a"), sum(t, c, "a"))
}

func TestRedundantDirectEdgeKeepsSum(t *testing.T) {
	ids := []graph.EntityID{"a", "b", "c"}
	chain := []edge{{"a", "b", dep}, {"b", "c", dep}}
	shortcut := append(slices.Clone(chain), edge{"a", "c", dep})
	tokens := map[graph.EntityID]string{"c": "x"}

	before := New(build(t, ids, chain), []graph.RelationKind{dep}, tokens)
	after := New(build(t, ids, shortcut), []graph.RelationKind{dep}, tokens)

	for _, id := range ids {
		test.EqOp(t, sum(t, before, id), sum(t, after, id), test.Sprintf("sum of %q", id))
	}
}

func TestEdgeOrderKeepsSum(t *testing.T) {
	ids := []graph.EntityID{"a", "b", "c", "d"}
	edges := []edge{{"a", "b", dep}, {"a", "c", dep}, {"b", "d", dep}, {"c", "d", dep}, {"d", "a", dep}}
	want := New(build(t, ids, edges), []graph.RelationKind{dep}, nil)

	rng := rand.New(rand.NewSource(1))
	for range 20 {
		shuffled := slices.Clone(edges)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got := New(build(t, ids, shuffled), []graph.RelationKind{dep}, nil)
		for _, id := range ids {
			test.EqOp(t, sum(t, want, id), sum(t, got, id), test.Sprintf("sum of %q", id))
		}
	}
}

func TestTokenChangeReachesOnlyDependents(t *testing.T) {
	g := build(t,
		[]graph.EntityID{"app", "lib", "leaf", "unrelated"},
		[]edge{{"app", "lib", dep}, {"lib", "leaf", dep}},
	)
	relations := []graph.RelationKind{dep}
	before := New(g, relations, map[graph.EntityID]string{"leaf": "v1"})
	after := New(g, relations, map[graph.EntityID]string{"leaf": "v2"})

	for _, id := range []graph.EntityID{"app", "lib", "leaf"} {
		test.NotEq(t, sum(t, before, id), sum(t, after, id), test.Sprintf("sum of %q", id))
	}
	test.EqOp(t, sum(t, before, "unrelated"), sum(t, after, "unrelated"))
}

func TestTokenIsPartOfEntry(t *testing.T) {
	g := build(t, []graph.EntityID{"a"}, nil)
	relations := []graph.RelationKind{dep}

	test.NotEq(t,
		sum(t, New(g, relations, map[graph.EntityID]string{"a": "x"}), "a"),
		sum(t, New(g, relations, nil), "a"),
	)
}

func TestCycleMembersDependOnEachOther(t *testing.T) {
	g := build(t,
		[]graph.EntityID{"a", "b", "c"},
		[]edge{{"a", "b", dep}, {"b", "a", dep}, {"b", "c", dep}},
	)
	c := New(g, []graph.RelationKind{dep}, nil)

	test.Eq(t, []graph.EntityID{"b", "c"}, closure(t, c, "a"))
	test.Eq(t, []graph.EntityID{"a", "c"}, closure(t, c, "b"))
	test.SliceEmpty(t, closure(t, c, "c"))
}

func TestSelfEdgeIsIgnored(t *testing.T) {
	ids := []graph.EntityID{"a"}
	relations := []graph.RelationKind{dep}

	loop := New(build(t, ids, []edge{{"a", "a", dep}}), relations, nil)
	plain := New(build(t, ids, nil), relations, nil)

	test.SliceEmpty(t, closure(t, loop, "a"))
	test.EqOp(t, sum(t, plain, "a"), sum(t, loop, "a"))
}

func TestUnknownEntity(t *testing.T) {
	c := New(build(t, []graph.EntityID{"a"}, nil), []graph.RelationKind{dep}, nil)

	_, err := c.Sum("missing")
	test.Error(t, err)
	_, err = c.Closure("missing")
	test.Error(t, err)
}

func TestDependenciesAreDirectSortedAndFollowed(t *testing.T) {
	g := build(t,
		[]graph.EntityID{"a", "b", "c", "d", "e"},
		[]edge{{"a", "d", dep}, {"a", "b", dep}, {"a", "c", other}, {"b", "e", dep}, {"a", "a", dep}},
	)
	c := New(g, []graph.RelationKind{dep}, nil)

	got, err := c.Dependencies("a")

	must.NoError(t, err)
	// Not c, which is not followed, e, which is not direct, or a itself.
	test.Eq(t, []graph.EntityID{"b", "d"}, got)
	got, err = c.Dependencies("e")
	must.NoError(t, err)
	test.SliceEmpty(t, got)
}

func TestDependenciesOfUnknownEntity(t *testing.T) {
	c := New(build(t, []graph.EntityID{"a"}, nil), []graph.RelationKind{dep}, nil)

	_, err := c.Dependencies("missing")

	test.Error(t, err)
}

// TestRandomGraphsMatchReference compares the memoized per-component
// calculation with the slow specification, evaluating entities in a random
// order so that results prove independent of where a walk starts.
func TestRandomGraphsMatchReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := range 200 {
		n := 2 + rng.Intn(14)
		ids := make([]graph.EntityID, 0, n)
		tokens := make(map[graph.EntityID]string, n)
		for i := range n {
			id := graph.EntityID(fmt.Sprintf("n%02d", i))
			ids = append(ids, id)
			if rng.Intn(2) == 0 {
				tokens[id] = fmt.Sprintf("t%d", rng.Intn(3))
			}
		}
		edges := make([]edge, 0, 3*n)
		for range rng.Intn(3 * n) {
			kind := dep
			if rng.Intn(4) == 0 {
				kind = other
			}
			edges = append(edges, edge{ids[rng.Intn(n)], ids[rng.Intn(n)], kind})
		}
		g := build(t, ids, edges)
		c := New(g, []graph.RelationKind{dep}, tokens)

		for _, i := range rng.Perm(n) {
			id := ids[i]
			test.EqOp(t, reference(g, id, tokens), sum(t, c, id), test.Sprintf("round %d: sum of %q", round, id))
		}
	}
}
