package detector

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference/selector"
	"github.com/hashicorp/hcl/v2"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func testTargetSensorSpec(namespace string, targets []graphtarget.ID, presentation selector.Target) SensorSpec {
	return SensorSpec{
		Namespace: namespace,
		Targets: func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
			return targets, nil
		},
		TargetSelector: func(graphtarget.ID) selector.Target { return presentation },
		ExecuteTarget: func(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error) {
			return graphtarget.Execution{Args: []string{"true"}}, nil
		},
	}
}

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
	test.NotNil(t, functions["gopkg"])
	test.NotNil(t, functions["gopkg_test"])
}

func TestAttachValidation(t *testing.T) {
	var nilBuilder *Builder
	test.Error(t, nilBuilder.Attach(SensorSpec{}))

	builder := NewBuilder()
	test.Error(t, builder.Attach(SensorSpec{}))
	spec := testTargetSensorSpec("test", nil, selector.Target{Path: "pkg", Kind: "test"})
	test.NoError(t, builder.Attach(spec))
	test.Error(t, builder.Attach(spec))
}

func TestTargetCapabilityValidation(t *testing.T) {
	complete := testTargetSensorSpec("targets", nil, selector.Target{Path: "pkg", Kind: "test"})
	cases := []struct {
		name string
		spec SensorSpec
		want string
	}{
		{name: "targets only", spec: SensorSpec{Namespace: "targets", Targets: complete.Targets}, want: "TargetSelector"},
		{name: "selector only", spec: SensorSpec{Namespace: "selector", TargetSelector: complete.TargetSelector}, want: "Targets"},
		{
			name: "execution missing",
			spec: SensorSpec{Namespace: "execution", Targets: complete.Targets, TargetSelector: complete.TargetSelector},
			want: "ExecuteTarget",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			builder := NewBuilder()
			err := builder.Attach(tc.spec)
			test.ErrorContains(t, err, tc.want)
			test.ErrorContains(t, err, tc.spec.Namespace)
		})
	}

	builder := NewBuilder()
	must.NoError(t, builder.Attach(SensorSpec{
		Namespace: "graph-only",
		DecodeID:  func(id graph.EntityID) (graph.Entity, error) { return graph.Entity{ID: id}, nil },
	}))
	scanner := builder.Compile()
	targets, err := scanner.Targets(t.Context(), nil)
	test.NoError(t, err)
	test.EqOp(t, 0, len(targets))
	target := graphtarget.ID{Namespace: "graph-only"}
	_, ok := scanner.TargetSelector(target)
	test.False(t, ok, test.Sprintf("non-target Sensors must not gain selector capability"))
	_, ok = scanner.TargetString(target)
	test.False(t, ok, test.Sprintf("TargetString must fail closed without selector capability"))
	test.False(t, scanner.TargetMatches(target, "test", ""), test.Sprintf("TargetMatches must fail closed without selector capability"))
}

func TestScannerTargetDispatchAndSnapshot(t *testing.T) {
	id := graphtarget.ID{ID: "custom:one", Namespace: "custom"}
	firstSelector := selector.Target{Path: "first", Kind: "task"}
	spec := testTargetSensorSpec("custom", []graphtarget.ID{id}, firstSelector)
	builder := NewBuilder()
	must.NoError(t, builder.Attach(spec))
	first := builder.Compile()

	secondID := graphtarget.ID{ID: "custom:two", Namespace: "custom"}
	spec.Targets = func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
		return []graphtarget.ID{secondID}, nil
	}
	spec.TargetSelector = func(graphtarget.ID) selector.Target {
		return selector.Target{Path: "second", Kind: "task"}
	}
	must.NoError(t, builder.Attach(testTargetSensorSpec("later", nil, selector.Target{})))
	second := builder.Compile()

	value, ok := first.TargetSelector(id)
	test.True(t, ok, test.Sprintf("attached target Sensor should provide its selector"))
	test.EqOp(t, firstSelector.Path, value.Path)
	test.EqOp(t, firstSelector.Kind, value.Kind)
	test.SliceEqOp(t, firstSelector.Aliases, value.Aliases)
	canonical, ok := first.TargetString(id)
	test.True(t, ok, test.Sprintf("attached target Sensor should render its selector"))
	test.EqOp(t, "//first#task", canonical)
	test.True(t, first.TargetMatches(id, "task", "first"), test.Sprintf("Scanner should match using its attached selector"))
	test.False(t, first.TargetMatches(graphtarget.ID{Namespace: "missing"}, "task", ""), test.Sprintf("unknown namespaces must fail closed"))
	_, ok = first.TargetString(graphtarget.ID{Namespace: "later"})
	test.False(t, ok, test.Sprintf("a Scanner snapshot must not observe later Builder attachments"))

	firstTargets, err := first.Targets(t.Context(), nil)
	test.NoError(t, err)
	test.EqOp(t, 1, len(firstTargets))
	test.EqOp(t, id.ID, firstTargets[0].ID)
	secondTargets, err := second.Targets(t.Context(), nil)
	test.NoError(t, err)
	test.EqOp(t, 1, len(secondTargets))
	test.EqOp(t, id.ID, secondTargets[0].ID)
}

func TestScannersKeepSameNamespaceSelectorsIndependent(t *testing.T) {
	id := graphtarget.ID{ID: "custom:target", Namespace: "custom"}
	compile := func(path string) Scanner {
		builder := NewBuilder()
		must.NoError(t, builder.Attach(testTargetSensorSpec("custom", []graphtarget.ID{id}, selector.Target{Path: path, Kind: "task"})))
		return builder.Compile()
	}
	first := compile("one")
	second := compile("two")

	firstValue, firstOK := first.TargetString(id)
	secondValue, secondOK := second.TargetString(id)
	test.True(t, firstOK && secondOK, test.Sprintf("both Scanner-local selectors should resolve"))
	test.EqOp(t, "//one#task", firstValue)
	test.EqOp(t, "//two#task", secondValue)
	test.True(t, first.TargetMatches(id, "one#task", ""), test.Sprintf("first Scanner should use its own selector mapping"))
	test.False(t, first.TargetMatches(id, "two#task", ""), test.Sprintf("first Scanner must not observe the second mapping"))
}

func TestResolveTarget(t *testing.T) {
	first := graphtarget.ID{ID: "target:z", Namespace: "resolve"}
	second := graphtarget.ID{ID: "target:a", Namespace: "resolve"}
	presentations := map[graph.EntityID]selector.Target{
		first.ID:  selector.HCL("one/atte.hcl", "test", "unit", 0),
		second.ID: selector.HCL("two/atte.hcl", "test", "unit", 0),
	}
	builder := NewBuilder()
	must.NoError(t, builder.Attach(SensorSpec{
		Namespace: "resolve",
		Targets: func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
			return []graphtarget.ID{first, second}, nil
		},
		TargetSelector: func(target graphtarget.ID) selector.Target { return presentations[target.ID] },
		ExecuteTarget: func(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error) {
			return graphtarget.Execution{Args: []string{"true"}}, nil
		},
	}))
	scanner := builder.Compile()

	resolved, err := scanner.ResolveTarget(t.Context(), nil, "one#test.unit", "")
	test.NoError(t, err)
	test.EqOp(t, first.ID, resolved.ID)
	test.EqOp(t, first.Namespace, resolved.Namespace)

	resolved, err = scanner.ResolveTarget(t.Context(), nil, "//two/atte.hcl#test.unit", "unrelated")
	test.NoError(t, err)
	test.EqOp(t, second.ID, resolved.ID)
	resolved, err = scanner.ResolveTarget(t.Context(), nil, "atte.hcl#test.unit", "one")
	test.NoError(t, err)
	test.EqOp(t, first.ID, resolved.ID)

	_, err = scanner.ResolveTarget(t.Context(), nil, "missing", "")
	var noMatch *selector.NoMatchError
	test.True(t, errors.As(err, &noMatch), test.Sprintf("missing selectors should produce a typed no-match error"))
	test.EqOp(t, "missing", noMatch.Input)

	_, err = scanner.ResolveTarget(t.Context(), nil, "test", "")
	var ambiguous *selector.AmbiguousError
	test.True(t, errors.As(err, &ambiguous), test.Sprintf("multiple matching targets should produce a typed ambiguity error"))
	test.SliceEqOp(t, []selector.AmbiguousCandidate{
		{TargetID: "target:z", Selector: "//one/atte.hcl#test.unit"},
		{TargetID: "target:a", Selector: "//two/atte.hcl#test.unit"},
	}, ambiguous.Candidates)

	tiedFirst := graphtarget.ID{ID: "target:b", Namespace: "tie"}
	tiedSecond := graphtarget.ID{ID: "target:a", Namespace: "tie"}
	tiedBuilder := NewBuilder()
	must.NoError(t, tiedBuilder.Attach(SensorSpec{
		Namespace: "tie",
		Targets: func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
			return []graphtarget.ID{tiedFirst, tiedSecond}, nil
		},
		TargetSelector: func(graphtarget.ID) selector.Target {
			return selector.Target{Path: "same", Kind: "test"}
		},
		ExecuteTarget: func(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error) {
			return graphtarget.Execution{Args: []string{"true"}}, nil
		},
	}))
	_, err = tiedBuilder.Compile().ResolveTarget(t.Context(), nil, "test", "")
	var tiedAmbiguous *selector.AmbiguousError
	test.True(t, errors.As(err, &tiedAmbiguous), test.Sprintf("equal selectors should remain ambiguous"))
	test.SliceEqOp(t, []selector.AmbiguousCandidate{
		{TargetID: "target:a", Selector: "//same#test"},
		{TargetID: "target:b", Selector: "//same#test"},
	}, tiedAmbiguous.Candidates)
}

func TestResolveTargetErrorsAndCancellation(t *testing.T) {
	discoveryErr := &selector.NoMatchError{Input: "discovery"}
	discoveryCalls := 0
	builder := NewBuilder()
	must.NoError(t, builder.Attach(SensorSpec{
		Namespace: "broken",
		Targets: func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
			discoveryCalls++
			return nil, discoveryErr
		},
		TargetSelector: func(graphtarget.ID) selector.Target { return selector.Target{} },
		ExecuteTarget: func(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error) {
			return graphtarget.Execution{Args: []string{"true"}}, nil
		},
	}))
	scanner := builder.Compile()
	_, err := scanner.ResolveTarget(t.Context(), nil, "test", "")
	test.ErrorIs(t, err, discoveryErr)
	var wrappedNoMatch *selector.NoMatchError
	test.True(t, errors.As(err, &wrappedNoMatch), test.Sprintf("discovery wrapping should preserve shared selector error types"))
	test.ErrorContains(t, err, "resolve target")
	test.EqOp(t, 1, discoveryCalls)

	preCanceled, stop := context.WithCancel(t.Context())
	stop()
	discoveryCalls = 0
	_, err = scanner.ResolveTarget(preCanceled, nil, "test", "")
	test.ErrorIs(t, err, context.Canceled)
	test.EqOp(t, 0, discoveryCalls)

	ctx, cancel := context.WithCancel(t.Context())
	id := graphtarget.ID{ID: "cancel:target", Namespace: "cancel"}
	builder = NewBuilder()
	must.NoError(t, builder.Attach(SensorSpec{
		Namespace: "cancel",
		Targets: func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
			return []graphtarget.ID{id}, nil
		},
		TargetSelector: func(graphtarget.ID) selector.Target {
			cancel()
			return selector.Target{Path: "pkg", Kind: "test"}
		},
		ExecuteTarget: func(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error) {
			return graphtarget.Execution{Args: []string{"true"}}, nil
		},
	}))
	_, err = builder.Compile().ResolveTarget(ctx, nil, "test", "")
	test.ErrorIs(t, err, context.Canceled)
}

func TestResolveTargetRejectsDiscoveredTargetWithoutSelector(t *testing.T) {
	builder := NewBuilder()
	id := graphtarget.ID{ID: "unattached:target", Namespace: "unattached"}
	must.NoError(t, builder.Attach(SensorSpec{
		Namespace: "target-source",
		Targets: func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
			return []graphtarget.ID{id}, nil
		},
		TargetSelector: func(graphtarget.ID) selector.Target { return selector.Target{Path: "pkg", Kind: "test"} },
		ExecuteTarget: func(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error) {
			return graphtarget.Execution{Args: []string{"true"}}, nil
		},
	}))
	_, err := builder.Compile().ResolveTarget(t.Context(), nil, "test", "")
	test.ErrorContains(t, err, "has no selector capability")
	test.ErrorContains(t, err, string(id.ID))
}

func TestAttachSensor(t *testing.T) {
	builder := NewBuilder()
	test.NoError(t, builder.AttachSensor(methodSensor{}))
	test.Error(t, builder.AttachSensor(methodSensor{}))

	scanner := builder.Compile()
	functions, err := scanner.HCLFunctions(t.Context(), nil, "")
	test.NoError(t, err)
	test.NotNil(t, functions["method"])
	test.NotNil(t, scanner.TargetKinds()["method"])
	target := graphtarget.ID{ID: "method:target", Namespace: "method"}
	canonical, ok := scanner.TargetString(target)
	test.True(t, ok, test.Sprintf("method-based TargetSensor should adapt its selector capability"))
	test.EqOp(t, "//method#test", canonical)
	execution, err := scanner.ExecuteTarget(t.Context(), nil, "", target)
	test.NoError(t, err)
	test.SliceEqOp(t, []string{"true"}, execution.Args)
}

func TestAttachSensorRequiresCompleteTargetCapability(t *testing.T) {
	builder := NewBuilder()
	must.NoError(t, builder.AttachSensor(partialMethodSensor{}))
	scanner := builder.Compile()
	targets, err := scanner.Targets(t.Context(), nil)
	test.NoError(t, err)
	test.EqOp(t, 0, len(targets))
	_, ok := scanner.TargetSelector(graphtarget.ID{Namespace: "partial"})
	test.False(t, ok, test.Sprintf("partial method capabilities must not be adapted as target discovery"))
}

func TestAttachTarget(t *testing.T) {
	builder := NewBuilder()
	decoder := func(*hcl.BodyContent, *hcl.EvalContext) (any, error) { return struct{}{}, nil }
	schema := hcl.BodySchema{}
	spec := attehcl.TargetKindSpec{Schema: &schema, Decoder: decoder}
	test.NoError(t, AttachHCLTargetBlock(builder, "custom", spec))
	test.Error(t, AttachHCLTargetBlock(builder, "custom", spec))

	attached := builder.Compile().TargetKinds()
	test.EqOp(t, 1, len(attached))
	attachedSpec, ok := attached["custom"]
	test.True(t, ok, test.Sprintf("custom target kind should be attached: %#v", attached))
	test.NotNil(t, attachedSpec.Schema)
}

func TestAttachHCLFunction(t *testing.T) {
	builder := NewBuilder()
	test.NoError(t, builder.AttachHCLFunction("path", attegit.PathHCLFunction))
	test.Error(t, builder.AttachHCLFunction("path", attegit.PathHCLFunction))
}

func TestCompileSnapshotsAttachments(t *testing.T) {
	builder := NewBuilder()
	decoder := func(*hcl.BodyContent, *hcl.EvalContext) (any, error) { return struct{}{}, nil }
	must.NoError(t, AttachHCLTargetBlock(builder, "first", attehcl.TargetKindSpec{Decoder: decoder}))
	compiled := builder.Compile()
	must.NoError(t, AttachHCLTargetBlock(builder, "second", attehcl.TargetKindSpec{Decoder: decoder}))

	test.EqOp(t, 1, len(compiled.TargetKinds()))
	test.EqOp(t, 2, len(builder.Compile().TargetKinds()))
}

func TestAttachTargetValidation(t *testing.T) {
	decoder := func(*hcl.BodyContent, *hcl.EvalContext) (any, error) { return struct{}{}, nil }
	test.Error(t, AttachHCLTargetBlock(nil, "custom", attehcl.TargetKindSpec{Decoder: decoder}))
	builder := NewBuilder()
	test.Error(t, AttachHCLTargetBlock(builder, "bad name", attehcl.TargetKindSpec{Decoder: decoder}))
	test.Error(t, AttachHCLTargetBlock(builder, "missing_decoder", attehcl.TargetKindSpec{}))
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
	spec := testTargetSensorSpec("targets", nil, selector.Target{Path: "pkg", Kind: "test"})
	test.NoError(t, builder.Attach(spec))
	test.NoError(t, builder.Attach(SensorSpec{
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
	test.ErrorContains(t, decode("unknown:value"), "no Sensor attached")
	test.ErrorContains(t, decode("targets:value"), "does not decode")
	test.ErrorContains(t, decode("broken:value"), "bad entity")
}

type methodSensor struct{}

type partialMethodSensor struct{}

func (partialMethodSensor) Namespace() string { return "partial" }

func (partialMethodSensor) Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
	return []graphtarget.ID{{ID: "partial:target", Namespace: "partial"}}, nil
}

func (partialMethodSensor) TargetSelector(graphtarget.ID) selector.Target {
	return selector.Target{Path: "partial", Kind: "test"}
}

func (partialMethodSensor) DecodeID(id graph.EntityID) (graph.Entity, error) {
	return graph.Entity{ID: id}, nil
}

func (methodSensor) Namespace() string { return "method" }

func (methodSensor) Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
	return nil, nil
}

func (methodSensor) TargetSelector(graphtarget.ID) selector.Target {
	return selector.Target{Path: "method", Kind: "test"}
}

func (methodSensor) ExecuteTarget(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error) {
	return graphtarget.Execution{Args: []string{"true"}}, nil
}

var _ TargetSensor = methodSensor{}

func (methodSensor) HCLFunctions() map[string]HCLFunctionFactory {
	return map[string]HCLFunctionFactory{"method": attegit.PathHCLFunction}
}

func (methodSensor) HCLTargetBlocks() map[attehcl.Kind]attehcl.TargetKindSpec {
	return map[attehcl.Kind]attehcl.TargetKindSpec{
		"method": {Decoder: func(*hcl.BodyContent, *hcl.EvalContext) (any, error) { return struct{}{}, nil }},
	}
}
