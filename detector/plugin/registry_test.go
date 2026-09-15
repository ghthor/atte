package plugin

import (
	"context"
	"fmt"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/shoenig/test"
)

func TestNewBuiltIn(t *testing.T) {
	r, err := NewBuiltIn()
	test.NoError(t, err)
	test.NotNil(t, r)

	functions, err := r.HCLFunctions(t.Context(), nil, "")
	test.NoError(t, err)
	test.NotNil(t, functions)
	test.NotNil(t, functions["path"])
}

func TestRegisterValidation(t *testing.T) {
	var nilRegistry *Registry
	test.Error(t, nilRegistry.Register(Detector{}))

	r := New()
	test.Error(t, r.Register(Detector{}))
	detector := Detector{Namespace: "test", Targets: func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) { return nil, nil }}
	test.NoError(t, r.Register(detector))
	test.Error(t, r.Register(detector))
}

func TestRegisterDetector(t *testing.T) {
	r := New()
	test.NoError(t, r.RegisterDetector(methodDetector{}))
	test.Error(t, r.RegisterDetector(methodDetector{}))
}

func TestDecodeID(t *testing.T) {
	r, err := NewBuiltIn()
	test.NoError(t, err)

	cases := []struct {
		name string
		id   graph.EntityID
		kind graph.EntityKind
	}{
		{name: "git", id: "attegit:some/path", kind: "attegit"},
		{name: "go", id: "attego:package:go.mod:cA", kind: "attego:package"},
		{name: "hcl", id: "attehcl:test:atte.hcl:0", kind: "attehcl:test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entity, err := r.DecodeID(tc.id)
			test.NoError(t, err)
			test.EqOp(t, tc.id, entity.ID)
			test.EqOp(t, tc.kind, entity.Kind)
		})
	}
}

func TestDecodeIDErrors(t *testing.T) {
	var nilRegistry *Registry
	_, err := nilRegistry.DecodeID("attegit:path")
	test.ErrorContains(t, err, "registry is nil")

	r := New()
	decode := func(id graph.EntityID) error {
		_, err := r.DecodeID(id)
		return err
	}
	test.ErrorContains(t, decode(""), "no namespace")
	test.ErrorContains(t, decode("unknown:value"), "no detector registered")

	detector := Detector{
		Namespace: "targets",
		Targets:   func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) { return nil, nil },
	}
	test.NoError(t, r.Register(detector))
	test.ErrorContains(t, decode("targets:value"), "does not decode")

	test.NoError(t, r.Register(Detector{
		Namespace: "broken",
		DecodeID: func(graph.EntityID) (graph.Entity, error) {
			return graph.Entity{}, fmt.Errorf("bad entity")
		},
	}))
	test.ErrorContains(t, decode("broken:value"), "bad entity")
}

type methodDetector struct{}

func (methodDetector) Namespace() string { return "method" }

func (methodDetector) Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
	return nil, nil
}
