package detector

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
	scanner := builder.Compile()
	test.NotNil(t, scanner)
	test.EqOp(t, 3, len(scanner.TargetKinds()))

	functions, err := scanner.HCLFunctions(t.Context(), nil, "")
	test.NoError(t, err)
	test.NotNil(t, functions)
	test.NotNil(t, functions["path"])
}

func TestRegisterValidation(t *testing.T) {
	var nilBuilder *Builder
	test.Error(t, nilBuilder.Register(SensorSpec{}))

	builder := NewBuilder()
	test.Error(t, builder.Register(SensorSpec{}))
	spec := SensorSpec{Namespace: "test", Targets: func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) { return nil, nil }}
	test.NoError(t, builder.Register(spec))
	test.Error(t, builder.Register(spec))
}

func TestRegisterSensor(t *testing.T) {
	builder := NewBuilder()
	test.NoError(t, builder.RegisterSensor(methodSensor{}))
	test.Error(t, builder.RegisterSensor(methodSensor{}))
}

func TestRegisterTarget(t *testing.T) {
	builder := NewBuilder()
	decoder := func(*hcl.BodyContent, *hcl.EvalContext) (any, error) { return struct{}{}, nil }
	schema := hcl.BodySchema{}
	spec := attehcl.TargetKindSpec{Schema: &schema, Decoder: decoder}
	test.NoError(t, RegisterHCLBlock(builder, "custom", spec))
	test.Error(t, RegisterHCLBlock(builder, "custom", spec))

	registered := builder.Compile().TargetKinds()
	test.EqOp(t, 1, len(registered))
	registeredSpec, ok := registered["custom"]
	test.True(t, ok, test.Sprintf("custom target kind should be registered: %#v", registered))
	test.NotNil(t, registeredSpec.Schema)
}

func TestCompileSnapshotsRegistrations(t *testing.T) {
	builder := NewBuilder()
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
	builder := NewBuilder()
	test.Error(t, RegisterHCLBlock(builder, "bad name", attehcl.TargetKindSpec{Decoder: decoder}))
	test.Error(t, RegisterHCLBlock(builder, "missing_decoder", attehcl.TargetKindSpec{}))
}

func TestDecodeID(t *testing.T) {
	builder, err := NewDefaultBuilder()
	test.NoError(t, err)
	scanner := builder.Compile()

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
			entity, err := scanner.DecodeID(tc.id)
			test.NoError(t, err)
			test.EqOp(t, tc.id, entity.ID)
			test.EqOp(t, tc.kind, entity.Kind)
		})
	}
}

func TestDecodeIDErrors(t *testing.T) {
	builder := NewBuilder()
	spec := SensorSpec{
		Namespace: "targets",
		Targets:   func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) { return nil, nil },
	}
	test.NoError(t, builder.Register(spec))
	test.NoError(t, builder.Register(SensorSpec{
		Namespace: "broken",
		DecodeID: func(graph.EntityID) (graph.Entity, error) {
			return graph.Entity{}, fmt.Errorf("bad entity")
		},
	}))
	scanner := builder.Compile()
	decode := func(id graph.EntityID) error {
		_, err := scanner.DecodeID(id)
		return err
	}
	test.ErrorContains(t, decode(""), "no namespace")
	test.ErrorContains(t, decode("unknown:value"), "no Sensor registered")
	test.ErrorContains(t, decode("targets:value"), "does not decode")
	test.ErrorContains(t, decode("broken:value"), "bad entity")
}

type methodSensor struct{}

func (methodSensor) Namespace() string { return "method" }

func (methodSensor) Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
	return nil, nil
}
