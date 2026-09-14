// Package graphset contains shared detector graph configuration types.
package graphset

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/zclconf/go-cty/cty/function"
)

// FunctionProvider supplies additional HCL functions for a repository file.
type FunctionProvider func(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)

// EntityDecoder resolves a graph entity ID to its detector-specific entity kind.
type EntityDecoder func(graph.EntityID) (graph.Entity, error)

// Options contains the options shared by detector graph builders.
type Options struct {
	AttachToTree  bool
	Functions     FunctionProvider
	EntityDecoder EntityDecoder
}

// Option configures a detector graph builder.
type Option func(*Options)

// WithAttachToTree attaches detector entities to their repository tree and source files.
func WithAttachToTree() Option {
	return func(options *Options) { options.AttachToTree = true }
}

// WithEntityDecoder supplies the resolver used for detector entity dependencies.
func WithEntityDecoder(decoder EntityDecoder) Option {
	return func(options *Options) { options.EntityDecoder = decoder }
}
