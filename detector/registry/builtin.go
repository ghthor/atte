package registry

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/reference"
	"github.com/zclconf/go-cty/cty/function"
)

// NewBuiltIn returns a registry containing atte's built-in detectors and HCL functions.
func NewBuiltIn() (*Registry, error) {
	r := New()

	if err := r.RegisterDetector(attegit.Detector{}); err != nil {
		return nil, err
	}
	if err := r.RegisterDetector(attego.Detector{}); err != nil {
		return nil, err
	}
	if err := r.RegisterDetector(attehcl.NewDetector(r.FunctionProvider(), r.DecodeID)); err != nil {
		return nil, err
	}
	if err := r.RegisterHCLFunction("path", attegit.PathHCLFunction); err != nil {
		return nil, err
	}
	for _, name := range []string{"gopkg", "gopkg_test"} {
		functionName := name
		if err := r.RegisterHCLFunction(functionName, func(ctx context.Context, repo *attegit.Repo, file reference.Blob) (function.Function, error) {
			return attego.HCLFunction(ctx, repo, file, functionName)
		}); err != nil {
			return nil, err
		}
	}
	return r, nil
}
