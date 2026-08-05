// Package graphset contains shared detector graph configuration types.
package graphset

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/reference"
	"github.com/zclconf/go-cty/cty/function"
)

// FunctionProvider supplies additional HCL functions for a repository file.
type FunctionProvider func(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)

// Options contains the options shared by detector graph builders.
type Options struct {
	AttachToTree bool
	Functions    FunctionProvider
}

// Option configures a detector graph builder.
type Option func(*Options)

// WithAttachToTree attaches detector entities to their repository tree and source files.
func WithAttachToTree() Option {
	return func(options *Options) { options.AttachToTree = true }
}
