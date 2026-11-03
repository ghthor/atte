package cone_test

import (
	"slices"
	"testing"

	"github.com/ghthor/atte/cone"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// frontiers returns the layers of the Cone of the change from from to to.
func frontiers(t *testing.T, from, to world) [][]graph.EntityID {
	t.Helper()
	layers, err := diff(t, from, to).Frontiers()
	must.NoError(t, err)
	return layers
}

func layers(each ...[]graph.EntityID) [][]graph.EntityID { return each }

func TestFrontiersOfAnEmptyConeHaveNoLayers(t *testing.T) {
	test.SliceEmpty(t, frontiers(t, base(), base()))
}

func TestFrontiersLayerAnEditByDistanceFromIt(t *testing.T) {
	got := frontiers(t, base(), base().withToken("a", "2"))

	// a is the edit. dir contains it and pkg imports it. app imports pkg and
	// root contains dir.
	test.Eq(t, layers(ids("a"), ids("dir", "pkg"), ids("app", "root")), got)
}

func TestFrontiersUseTheNearestChangedDependency(t *testing.T) {
	got := frontiers(t, base(), base().withToken("a", "2").withToken("b", "2"))

	// root contains b, which changed, as well as dir, which is a layer later,
	// so root is only one step from an Impulse.
	test.Eq(t, layers(ids("a", "b"), ids("dir", "pkg", "root"), ids("app")), got)
}

func TestFrontiersTreatAnAddedEntityAsAnImpulse(t *testing.T) {
	head := base().withToken("a", "2").with("c", edge("c", imports, "pkg"))

	got := frontiers(t, base(), head)

	// c depends on pkg, which changed, but c is new, so it did not change
	// because of anything.
	test.Eq(t, layers(ids("a", "c"), ids("dir", "pkg"), ids("app", "root")), got)
}

func TestFrontiersTreatAnEntityThatLostADependencyAsAnImpulse(t *testing.T) {
	got := frontiers(t, base(), base().without("a"))

	// a is gone, so it cannot be listed. What depended on it changed with no
	// changed dependency, which is the closest the Cone has to the edit.
	test.Eq(t, layers(ids("dir", "pkg"), ids("app", "root")), got)
}

// cycle is base with x and y importing each other, and z importing x.
func cycle() world {
	w := base().with("x", edge("x", imports, "y"), edge("y", imports, "x"))
	return w.with("y").with("z", edge("z", imports, "x"))
}

func TestFrontiersPutAChangedCycleWithoutAnEntryInLayerZero(t *testing.T) {
	got := frontiers(t, cycle(), cycle().withToken("y", "2"))

	test.Eq(t, layers(ids("x", "y"), ids("z")), got)
}

func TestFrontiersLayerACycleByItsNearestChangedDependency(t *testing.T) {
	linked := func() world {
		w := cycle()
		w.edges = append(slices.Clone(w.edges), edge("y", imports, "a"))
		return w
	}

	got := frontiers(t, linked(), linked().withToken("a", "2"))

	test.Eq(t, layers(ids("a"), ids("dir", "pkg", "x", "y"), ids("app", "root", "z")), got)
}

func TestFrontiersListEveryChangedEntityOnce(t *testing.T) {
	for name, head := range map[string]world{
		"edited":  base().withToken("a", "2"),
		"both":    base().withToken("a", "2").withToken("b", "2"),
		"added":   base().with("c", edge("dir", contains, "c")),
		"removed": base().without("a"),
	} {
		t.Run(name, func(t *testing.T) {
			delta := diff(t, base(), head)

			got, err := delta.Frontiers()

			must.NoError(t, err)
			var flat []graph.EntityID
			for i, layer := range got {
				test.SliceNotEmpty(t, layer, test.Sprintf("layer %d", i))
				flat = append(flat, layer...)
			}
			test.SliceContainsAll(t, delta.Changed, flat)
			test.EqOp(t, len(delta.Changed), len(flat))
		})
	}
}

func TestFrontiersOfCapturedRevisions(t *testing.T) {
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

	got, err := delta.Frontiers()

	must.NoError(t, err)
	// The edited file is the only Impulse. Everything else changed because it
	// contains or imports something that did.
	test.Eq(t, ids(string(fileID(t, "internal/value/value.go"))), got[0])
	last := got[len(got)-1]
	test.SliceContains(t, last, attegit.EntityID(reference.Root))
}
