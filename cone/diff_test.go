package cone_test

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/ghthor/atte/cone"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/contenthash"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

const (
	contains graph.RelationKind = "contains"
	imports  graph.RelationKind = "imports"
	mentions graph.RelationKind = "mentions"
)

// world is a hand-built revision: entities are named by their id, an edge
// from -> to means from depends on to unless its kind is mentions, and tokens
// carry each entity's own content.
type world struct {
	entities []string
	edges    []graph.Relationship
	tokens   map[string]string
}

func (w world) snapshot(t *testing.T) *cone.Snapshot {
	t.Helper()
	entities := make([]graph.Entity, 0, len(w.entities))
	for _, id := range w.entities {
		entities = append(entities, graph.Entity{ID: graph.EntityID(id), Kind: "test"})
	}
	g, err := graph.New(entities, w.edges)
	must.NoError(t, err)
	tokens := make(map[graph.EntityID]string, len(w.tokens))
	for id, token := range w.tokens {
		tokens[graph.EntityID(id)] = token
	}
	return &cone.Snapshot{
		Graph:  g,
		Hashes: contenthash.New(g, []graph.RelationKind{contains, imports}, tokens),
	}
}

func edge(from string, kind graph.RelationKind, to string) graph.Relationship {
	return graph.Relationship{From: graph.EntityID(from), To: graph.EntityID(to), Kind: kind}
}

// base is a small repository:
//
//	root contains dir, b      dir contains a
//	pkg imports a             app imports pkg      app mentions b
func base() world {
	return world{
		entities: []string{"root", "dir", "a", "b", "pkg", "app"},
		edges: []graph.Relationship{
			edge("root", contains, "dir"),
			edge("root", contains, "b"),
			edge("dir", contains, "a"),
			edge("pkg", imports, "a"),
			edge("app", imports, "pkg"),
			edge("app", mentions, "b"),
		},
		tokens: map[string]string{"a": "1", "b": "1"},
	}
}

func (w world) withToken(id, token string) world {
	tokens := maps.Clone(w.tokens)
	tokens[id] = token
	w.tokens = tokens
	return w
}

// without removes id and every edge that touches it.
func (w world) without(id string) world {
	w.entities = slices.DeleteFunc(slices.Clone(w.entities), func(e string) bool { return e == id })
	w.edges = slices.DeleteFunc(slices.Clone(w.edges), func(r graph.Relationship) bool {
		return string(r.From) == id || string(r.To) == id
	})
	return w
}

func (w world) with(id string, edges ...graph.Relationship) world {
	w.entities = append(slices.Clone(w.entities), id)
	w.edges = append(slices.Clone(w.edges), edges...)
	return w
}

func ids(names ...string) []graph.EntityID {
	out := make([]graph.EntityID, 0, len(names))
	for _, name := range names {
		out = append(out, graph.EntityID(name))
	}
	return out
}

func diff(t *testing.T, from, to world) *cone.Delta {
	t.Helper()
	delta, err := cone.Diff(from.snapshot(t), to.snapshot(t))
	must.NoError(t, err)
	return delta
}

// relationships lists every relationship of g as text, sorted.
func relationships(g *graph.Graph) []string {
	var out []string
	for _, id := range g.EntityKeys {
		for _, r := range g.Out(id) {
			out = append(out, fmt.Sprintf("%s -%s-> %s", r.From, r.Kind, r.To))
		}
	}
	slices.Sort(out)
	return out
}

func TestDiffOfIdenticalSnapshotsIsEmpty(t *testing.T) {
	delta := diff(t, base(), base())

	test.Eq(t, ids(), delta.Changed)
	test.Eq(t, ids(), delta.Added)
	test.Eq(t, ids(), delta.Removed)
	test.Eq(t, 0, len(delta.Graph.Entities))
}

func TestDiffMarksTheEditedEntityAndEveryDependent(t *testing.T) {
	delta := diff(t, base(), base().withToken("a", "2"))

	// b is untouched. pkg and app reach a only through imports, and dir and
	// root reach it through contains.
	test.Eq(t, ids("a", "app", "dir", "pkg", "root"), delta.Changed)
	test.Eq(t, ids(), delta.Added)
	test.Eq(t, ids(), delta.Removed)
	test.True(t, delta.Contains("pkg"), test.Sprint("a dependent should be in the Cone"))
	test.False(t, delta.Contains("b"), test.Sprint("an unrelated entity should not be in the Cone"))
}

func TestDiffIgnoresRelationshipsThatAreNotDependencies(t *testing.T) {
	delta := diff(t, base(), base().withToken("b", "2"))

	// app mentions b, but a mention is not a dependency.
	test.Eq(t, ids("b", "root"), delta.Changed)
}

func TestDiffAddedEntityIsChangedAndSoIsItsContainer(t *testing.T) {
	head := base().with("c", edge("dir", contains, "c"))

	delta := diff(t, base(), head)

	test.Eq(t, ids("c", "dir", "root"), delta.Changed)
	test.Eq(t, ids("c"), delta.Added)
	test.Eq(t, ids(), delta.Removed)
}

func TestDiffRemovedEntityChangesItsContainerAndDependents(t *testing.T) {
	delta := diff(t, base(), base().without("a"))

	// a is gone from head, so it cannot be listed as changed or printed, but
	// everything whose closure held it lost an entry.
	test.Eq(t, ids("app", "dir", "pkg", "root"), delta.Changed)
	test.Eq(t, ids(), delta.Added)
	test.Eq(t, ids("a"), delta.Removed)
	test.False(t, delta.Contains("a"), test.Sprint("a removed entity is not in the head Cone"))
}

func TestDiffRenameIsOneRemovedAndOneAdded(t *testing.T) {
	head := base().without("a").with("a2", edge("dir", contains, "a2"), edge("pkg", imports, "a2")).withToken("a2", "1")

	delta := diff(t, base(), head)

	test.Eq(t, ids("a2", "app", "dir", "pkg", "root"), delta.Changed)
	test.Eq(t, ids("a2"), delta.Added)
	test.Eq(t, ids("a"), delta.Removed)
}

func TestDiffGraphIsTheSubgraphOfTheHeadGraphInducedByTheCone(t *testing.T) {
	delta := diff(t, base(), base().withToken("a", "2"))

	test.Eq(t, delta.Changed, delta.Graph.EntityKeys)
	// root -> b, and app mentions b, leave the Cone through b, which is
	// unchanged.
	test.Eq(t, []string{
		"app -imports-> pkg",
		"dir -contains-> a",
		"pkg -imports-> a",
		"root -contains-> dir",
	}, relationships(delta.Graph))
}

func TestDiffConeIsConnectedToTheRootThroughContains(t *testing.T) {
	for name, head := range map[string]world{
		"edited":  base().withToken("a", "2"),
		"added":   base().with("c", edge("dir", contains, "c")),
		"removed": base().without("a"),
	} {
		t.Run(name, func(t *testing.T) {
			delta := diff(t, base(), head)

			reached := map[graph.EntityID]bool{"root": true}
			for pending := []graph.EntityID{"root"}; len(pending) > 0; {
				id := pending[0]
				pending = pending[1:]
				for _, r := range delta.Graph.Out(id) {
					if r.Kind == contains && !reached[r.To] {
						reached[r.To] = true
						pending = append(pending, r.To)
					}
				}
			}
			// Everything a tree contains that changed is reached from the root
			// without leaving the Cone, so the pruned graph still renders as a
			// tree. Packages such as pkg are not contained by a tree here.
			for _, id := range delta.Changed {
				if inContainment(head, id) {
					test.True(t, reached[id], test.Sprintf("%s should be reachable from the root", id))
				}
			}
		})
	}
}

// inContainment reports whether some entity of w contains id.
func inContainment(w world, id graph.EntityID) bool {
	return slices.ContainsFunc(w.edges, func(r graph.Relationship) bool { return r.Kind == contains && r.To == id })
}

func TestDiffRequiresContentHashesOnBothSnapshots(t *testing.T) {
	hashed := base().snapshot(t)
	unhashed := &cone.Snapshot{Graph: hashed.Graph}

	_, err := cone.Diff(unhashed, hashed)
	test.ErrorContains(t, err, "base snapshot has no content hashes")

	_, err = cone.Diff(hashed, unhashed)
	test.ErrorContains(t, err, "head snapshot has no content hashes")

	_, err = cone.Diff(hashed, nil)
	test.ErrorContains(t, err, "head snapshot is empty")
}

func TestDiffOfCapturedRevisions(t *testing.T) {
	files := func(value string) map[string]string {
		return map[string]string{
			"go.mod":                  "module example.com/root\n\ngo 1.20\n",
			"main.go":                 "package main\n\nimport \"example.com/root/internal/value\"\n\nvar _ = value.Value\n",
			"internal/value/value.go": "package value\n\nconst Value = " + value + "\n",
		}
	}
	capture := func(value string) *cone.Snapshot {
		snapshot, err := cone.Capture(t.Context(), defaultScanner(t), openFiles(t, files(value)), cone.WithContentHashes())
		must.NoError(t, err)
		return snapshot
	}

	delta, err := cone.Diff(capture("1"), capture("2"))

	must.NoError(t, err)
	test.True(t, delta.Contains(fileID(t, "internal/value/value.go")), test.Sprint("the edited file should be in the Cone"))
	test.False(t, delta.Contains(fileID(t, "main.go")), test.Sprint("a file that only imports the edited package is not in the Cone"))
	test.False(t, delta.Contains(fileID(t, "go.mod")), test.Sprint("go.mod did not change"))
	test.True(t, delta.Contains(attegit.EntityID(reference.Root)), test.Sprint("the root tree contains the change"))
	test.Eq(t, ids(), delta.Added)
	test.Eq(t, ids(), delta.Removed)
	// The main package imports the edited package, so it is a Candidate even
	// though none of its own files changed.
	for _, importPath := range []string{"example.com/root", "example.com/root/internal/value"} {
		id := attego.EntityID(attego.PackageKind, reference.Root, importPath)
		test.True(t, delta.Contains(id), test.Sprintf("package %s should be in the Cone", importPath))
	}
}
