package registry

import (
	"context"
	"testing"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
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

func TestRegisterDetector(t *testing.T) {
	r := New()
	test.NoError(t, r.RegisterDetector(methodDetector{}))
	test.Error(t, r.RegisterDetector(methodDetector{}))
}

type methodDetector struct{}

func (methodDetector) Namespace() string { return "method" }

func (methodDetector) Selector(target Target) (selector.Target, bool) {
	return selector.Target{Kind: target.Kind}, true
}

func (methodDetector) Targets(context.Context, *attegit.Repo) ([]detector.Target, error) {
	return nil, nil
}

func (methodDetector) MatchIdentifier(Target, string) bool { return true }
