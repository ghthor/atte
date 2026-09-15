package detector

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/reference"
	"github.com/zclconf/go-cty/cty/function"
)

// NewDefaultBuilder returns a Builder containing Atte's built-in Sensors,
// target kinds, and HCL functions.
func NewDefaultBuilder() (*Builder, error) {
	builder := NewBuilder()

	for kind, spec := range attehcl.BuiltInTargetKinds() {
		if err := RegisterHCLBlock(builder, kind, spec); err != nil {
			return nil, err
		}
	}
	if err := builder.RegisterSensor(attegit.Detector{}); err != nil {
		return nil, err
	}
	if err := builder.RegisterSensor(attego.Detector{}); err != nil {
		return nil, err
	}
	builder.includeHCL = true
	if err := builder.RegisterHCLFunction("path", attegit.PathHCLFunction); err != nil {
		return nil, err
	}
	for _, name := range []string{"gopkg", "gopkg_test"} {
		functionName := name
		if err := builder.RegisterHCLFunction(functionName, func(ctx context.Context, repo *attegit.Repo, file reference.Blob) (function.Function, error) {
			return attego.HCLFunction(ctx, repo, file, functionName)
		}); err != nil {
			return nil, err
		}
	}
	return builder, nil
}
