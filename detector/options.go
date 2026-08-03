// Package detector contains shared detector interfaces and configuration types.
package detector

import (
	"context"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/reference"
	"github.com/zclconf/go-cty/cty/function"
)

// FunctionProvider supplies additional HCL functions for a repository file.
type FunctionProvider func(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)

// GraphOptions contains the options shared by detector graph builders.
type GraphOptions struct {
	AttachToTree bool
	Functions    FunctionProvider
}

// GraphOption configures a detector graph builder.
type GraphOption func(*GraphOptions)

// WithAttachToTree attaches detector entities to their repository tree and source files.
func WithAttachToTree() GraphOption {
	return func(options *GraphOptions) { options.AttachToTree = true }
}
