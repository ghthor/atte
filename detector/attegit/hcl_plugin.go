package attegit

import (
	"context"
	"fmt"
	"reflect"

	"github.com/ghthor/atte/reference"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// RepositoryPathType is the cty capsule type used for repository-relative blob references
var RepositoryPathType = cty.CapsuleWithOps(
	"atte.repository_path",
	reflect.TypeFor[reference.Blob](),
	&cty.CapsuleOps{
		GoString:  func(value any) string { return fmt.Sprintf("path(%q)", value.(reference.Blob)) },
		RawEquals: func(a, b any) bool { return a.(reference.Blob) == b.(reference.Blob) },
	},
)

// PathHCLFunctions returns the Git-backed HCL functions for one file.
func PathHCLFunctions(ctx context.Context, repo *Repo, file reference.Blob) (map[string]function.Function, error) {
	fn, err := PathHCLFunction(ctx, repo, file)
	if err != nil {
		return nil, err
	}
	return map[string]function.Function{"path": fn}, nil
}

// HCLFunctions provides the HCL function factories contributed by the Git
// Sensor during sensor attachment.
func (Detector) HCLFunctions() map[string]func(context.Context, *Repo, reference.Blob) (function.Function, error) {
	return map[string]func(context.Context, *Repo, reference.Blob) (function.Function, error){
		"path": PathHCLFunction,
	}
}

// PathHCLFunction constructs the Git-backed path function for an HCL file.
func PathHCLFunction(_ context.Context, _ *Repo, file reference.Blob) (function.Function, error) {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(RepositoryPathType),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			resolved, err := reference.ResolveBlobFromBlob(file, reference.SomePath(args[0].AsString()))
			if err != nil {
				return cty.NilVal, fmt.Errorf("resolve path %q from %q: %w", args[0].AsString(), file, err)
			}
			return cty.CapsuleVal(RepositoryPathType, &resolved), nil
		},
	}), nil
}
