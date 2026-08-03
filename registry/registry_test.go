package registry

import (
	"testing"

	"github.com/ghthor/atte/reference/selector"
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
	detector := Detector{Namespace: "test", Selectorize: func(Target) (selector.Target, bool) { return selector.Target{}, true }}
	test.NoError(t, r.Register(detector))
	test.Error(t, r.Register(detector))
}
