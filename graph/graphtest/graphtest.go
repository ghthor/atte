// Package graphtest provides assertions for graph tests.
package graphtest

import (
	"testing"

	"github.com/ghthor/atte/graph"
	"github.com/shoenig/test/must"
)

// HasRelation reports whether g contains a relationship with the given source,
// destination, and kind.
func HasRelation(g *graph.Graph, from, to graph.EntityID, kind graph.RelationKind) bool {
	for _, relation := range g.Out(from) {
		if relation.To == to && relation.Kind == kind {
			return true
		}
	}
	return false
}

// MustHaveRelation asserts that g contains the requested relationship.
func MustHaveRelation(t *testing.T, g *graph.Graph, from, to graph.EntityID, kind graph.RelationKind) {
	t.Helper()
	must.True(t, HasRelation(g, from, to, kind), must.Sprintf(
		"missing relation from %q to %q with kind %q; outgoing relations: %#v",
		from, to, kind, g.Out(from),
	))
}

// MustNotHaveRelation asserts that g does not contain the requested relationship.
func MustNotHaveRelation(t *testing.T, g *graph.Graph, from, to graph.EntityID, kind graph.RelationKind) {
	t.Helper()
	must.False(t, HasRelation(g, from, to, kind), must.Sprintf(
		"unexpected relation from %q to %q with kind %q; outgoing relations: %#v",
		from, to, kind, g.Out(from),
	))
}
