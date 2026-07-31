package graph

import (
	"testing"

	"github.com/shoenig/test/must"
)

func TestAbsorb(t *testing.T) {
	left, err := New(
		[]Entity{{ID: "a", Kind: "source"}, {ID: "b", Kind: "target"}},
		[]Relationship{{From: "a", To: "b", Kind: "calls"}},
	)
	must.NoError(t, err)
	right, err := New(
		[]Entity{{ID: "a", Kind: "source"}, {ID: "b", Kind: "target"}, {ID: "c", Kind: "target"}},
		[]Relationship{
			{From: "a", To: "b", Kind: "calls"},
			{From: "b", To: "c", Kind: "contains"},
		},
	)
	must.NoError(t, err)

	must.NoError(t, left.Absorb(right))
	must.SliceEqOp(t, []EntityID{"a", "b", "c"}, left.EntityKeys)
	must.SliceEqOp(t, []Relationship{{From: "a", To: "b", Kind: "calls"}}, left.Out("a"))
	must.SliceEqOp(t, []Relationship{{From: "b", To: "c", Kind: "contains"}}, left.Out("b"))
}

func TestAbsorbPreservesRelationshipKinds(t *testing.T) {
	left, err := New([]Entity{{ID: "a"}, {ID: "b"}}, []Relationship{{From: "a", To: "b", Kind: "calls"}})
	must.NoError(t, err)
	right, err := New([]Entity{{ID: "a"}, {ID: "b"}}, []Relationship{{From: "a", To: "b", Kind: "imports"}})
	must.NoError(t, err)

	must.NoError(t, left.Absorb(right))
	must.SliceEqOp(t, []Relationship{
		{From: "a", To: "b", Kind: "calls"},
		{From: "a", To: "b", Kind: "imports"},
	}, left.Out("a"))
}

func TestAbsorbRejectsConflictingEntity(t *testing.T) {
	left, err := New([]Entity{{ID: "a", Kind: "source"}}, nil)
	must.NoError(t, err)
	right, err := New([]Entity{{ID: "a", Kind: "target"}}, nil)
	must.NoError(t, err)

	before := append([]EntityID(nil), left.EntityKeys...)
	must.ErrorContains(t, left.Absorb(right), "conflicting entity")
	must.SliceEqOp(t, before, left.EntityKeys)
}

func TestAbsorbRejectsNil(t *testing.T) {
	graph, err := New([]Entity{{ID: "a"}}, nil)
	must.NoError(t, err)
	must.ErrorContains(t, graph.Absorb(nil), "nil graph")

	var nilGraph *Graph
	must.ErrorContains(t, nilGraph.Absorb(graph), "nil graph")
}
