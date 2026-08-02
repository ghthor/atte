package registry

import (
	"testing"

	"github.com/ghthor/atte/reference/selector"
	"github.com/shoenig/test/must"
)

func TestNewBuiltIn(t *testing.T) {
	r, err := NewBuiltIn()
	must.NoError(t, err)
	must.NotNil(t, r)

	functions, err := r.HCLFunctions(nil, "")
	must.NoError(t, err)
	must.NotNil(t, functions)
	must.NotNil(t, functions["path"])
}

func TestRegisterValidation(t *testing.T) {
	var nilRegistry *Registry
	must.Error(t, nilRegistry.Register(Detector{}))

	r := New()
	must.Error(t, r.Register(Detector{}))
	detector := Detector{Namespace: "test", Selectorize: func(Target) (selector.Target, bool) { return selector.Target{}, true }}
	must.NoError(t, r.Register(detector))
	must.Error(t, r.Register(detector))
}
