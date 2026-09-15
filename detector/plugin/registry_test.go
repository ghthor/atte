package plugin

import (
	"context"
	"fmt"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/hashicorp/hcl/v2"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestNewDefaultBuilder(t *testing.T) {
	builder, err := NewDefaultBuilder()
	test.NoError(t, err)
	r := builder.Compile()
	test.NotNil(t, r)
	test.EqOp(t, 3, len(r.TargetKinds()))

	functions, err := r.HCLFunctions(t.Context(), nil, "")
	test.NoError(t, err)
	test.NotNil(t, functions)
	test.NotNil(t, functions["path"])
}

func TestRegisterValidation(t *testing.T) {
	var nilBuilder *Builder
	test.Error(t, nilBuilder.Register(Detector{}))

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

func TestRegisterTarget(t *testing.T) {
	r := New()
	decoder := func(*hcl.BodyContent, *hcl.EvalContext) (any, error) { return struct{}{}, nil }
	schema := hcl.BodySchema{}
	spec := attehcl.TargetKindSpec{Schema: &schema, Decoder: decoder}
	test.NoError(t, RegisterHCLBlock(r, "custom", spec))
	test.Error(t, RegisterHCLBlock(r, "custom", spec))

	registered := r.Compile().TargetKinds()
	test.EqOp(t, 1, len(registered))
	registeredSpec, ok := registered["custom"]
	test.True(t, ok, test.Sprintf("custom target kind should be registered: %#v", registered))
	test.NotNil(t, registeredSpec.Schema)
}

func TestCompileSnapshotsRegistrations(t *testing.T) {
	builder := New()
	decoder := func(*hcl.BodyContent, *hcl.EvalContext) (any, error) { return struct{}{}, nil }
	must.NoError(t, RegisterHCLBlock(builder, "first", attehcl.TargetKindSpec{Decoder: decoder}))
	compiled := builder.Compile()
	must.NoError(t, RegisterHCLBlock(builder, "second", attehcl.TargetKindSpec{Decoder: decoder}))

	test.EqOp(t, 1, len(compiled.TargetKinds()))
	test.EqOp(t, 2, len(builder.Compile().TargetKinds()))
}

func TestRegisterTargetValidation(t *testing.T) {
	decoder := func(*hcl.BodyContent, *hcl.EvalContext) (any, error) { return struct{}{}, nil }
	test.Error(t, RegisterHCLBlock(nil, "custom", attehcl.TargetKindSpec{Decoder: decoder}))
	r := New()
	test.Error(t, RegisterHCLBlock(r, "bad name", attehcl.TargetKindSpec{Decoder: decoder}))
	test.Error(t, RegisterHCLBlock(r, "missing_decoder", attehcl.TargetKindSpec{}))
}

func TestDecodeID(t *testing.T) {
	builder, err := NewDefaultBuilder()
	test.NoError(t, err)
	r := builder.Compile()

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
	builder := New()
	detector := Detector{
		Namespace: "targets",
		Targets:   func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) { return nil, nil },
	}
	test.NoError(t, builder.Register(detector))
	test.NoError(t, builder.Register(Detector{
		Namespace: "broken",
		DecodeID: func(graph.EntityID) (graph.Entity, error) {
			return graph.Entity{}, fmt.Errorf("bad entity")
		},
	}))
	r := builder.Compile()
	decode := func(id graph.EntityID) error {
		_, err := r.DecodeID(id)
		return err
	}
	test.ErrorContains(t, decode(""), "no namespace")
	test.ErrorContains(t, decode("unknown:value"), "no detector registered")
	test.ErrorContains(t, decode("targets:value"), "does not decode")
	test.ErrorContains(t, decode("broken:value"), "bad entity")
}

type methodDetector struct{}

func (methodDetector) Namespace() string { return "method" }

func (methodDetector) Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
	return nil, nil
}
