package graph

import (
	"testing"

	"github.com/shoenig/test"
)

func TestAbsorb(t *testing.T) {
	left, err := New(
		[]Entity{{ID: "a", Kind: "source"}, {ID: "b", Kind: "target"}},
		[]Relationship{{From: "a", To: "b", Kind: "calls"}},
	)
	test.NoError(t, err)
	right, err := New(
		[]Entity{{ID: "a", Kind: "source"}, {ID: "b", Kind: "target"}, {ID: "c", Kind: "target"}},
		[]Relationship{
			{From: "a", To: "b", Kind: "calls"},
			{From: "b", To: "c", Kind: "contains"},
		},
	)
	test.NoError(t, err)

	test.NoError(t, left.Absorb(right))
	test.SliceEqOp(t, []EntityID{"a", "b", "c"}, left.EntityKeys)
	test.SliceEqOp(t, []Relationship{{From: "a", To: "b", Kind: "calls"}}, left.Out("a"))
	test.SliceEqOp(t, []Relationship{{From: "b", To: "c", Kind: "contains"}}, left.Out("b"))
}

func TestAbsorbPreservesRelationshipKinds(t *testing.T) {
	left, err := New([]Entity{{ID: "a"}, {ID: "b"}}, []Relationship{{From: "a", To: "b", Kind: "calls"}})
	test.NoError(t, err)
	right, err := New([]Entity{{ID: "a"}, {ID: "b"}}, []Relationship{{From: "a", To: "b", Kind: "imports"}})
	test.NoError(t, err)

	test.NoError(t, left.Absorb(right))
	test.SliceEqOp(t, []Relationship{
		{From: "a", To: "b", Kind: "calls"},
		{From: "a", To: "b", Kind: "imports"},
	}, left.Out("a"))
}

func TestAbsorbRejectsConflictingEntity(t *testing.T) {
	left, err := New([]Entity{{ID: "a", Kind: "source"}}, nil)
	test.NoError(t, err)
	right, err := New([]Entity{{ID: "a", Kind: "target"}}, nil)
	test.NoError(t, err)

	before := append([]EntityID(nil), left.EntityKeys...)
	test.ErrorContains(t, left.Absorb(right), "conflicting entity")
	test.SliceEqOp(t, before, left.EntityKeys)
}

func TestAbsorbRejectsNil(t *testing.T) {
	graph, err := New([]Entity{{ID: "a"}}, nil)
	test.NoError(t, err)
	test.ErrorContains(t, graph.Absorb(nil), "nil graph")

	var nilGraph *Graph
	test.ErrorContains(t, nilGraph.Absorb(graph), "nil graph")
}
