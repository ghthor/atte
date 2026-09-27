package scanner_test

import (
	"testing"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/scanner"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestNewDefault(t *testing.T) {
	builder, err := scanner.NewDefault()
	must.NoError(t, err)
	test.ErrorContains(t, builder.AttachSensor(attego.NewDetector()), "already attached")

	compiled, err := builder.Compile()
	must.NoError(t, err)
	test.NotNil(t, compiled)
	test.EqOp(t, 3, len(compiled.TargetKinds()))

	functions, err := compiled.HCLFunctions(t.Context(), nil, "")
	test.NoError(t, err)
	test.NotNil(t, functions)
	test.NotNil(t, functions["path"])
	test.NotNil(t, functions["gopkg"])
	test.NotNil(t, functions["gopkg_test"])
}

func TestDecodeID(t *testing.T) {
	compiled := newDefaultScanner(t)

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
			entity, err := compiled.DecodeID(tc.id)
			test.NoError(t, err)
			test.EqOp(t, tc.id, entity.ID)
			test.EqOp(t, tc.kind, entity.Kind)
		})
	}
}

func newDefaultScanner(t *testing.T) detector.Scanner {
	t.Helper()
	builder, err := scanner.NewDefault()
	must.NoError(t, err)
	compiled, err := builder.Compile()
	must.NoError(t, err)
	return compiled
}
