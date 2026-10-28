// Package hclblock defines the HCL block APIs shared by detector registration
// and HCL evaluation.
package hclblock

import (
	"context"
	"fmt"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
)

// TargetKind identifies an attached target kind and its target namespace.
type TargetKind string

// TargetGraphProjection projects a target into graph entities and relationships.
// The attachToTree argument requests the common file/tree containment relations.
type TargetGraphProjection func(context.Context, *attegit.Repo, Target, TargetGraphContext, bool) (TargetGraph, error)

// TargetKindOptions holds the optional projections of a target kind whose
// decoder produces a T. Each projection receives the target and its decoded
// value. Config results must not overlap the standard target identity fields.
// Execution receives the repository root. Script resolves a decoded target's
// script into a repository path or inline command.
type TargetKindOptions[T any] struct {
	Graph     func(context.Context, *attegit.Repo, Target, T, TargetGraphContext, bool) (TargetGraph, error)
	Execution func(Target, T, string) (TargetCommand, error)
	Config    func(Target, T) (map[string]any, error)
	Script    func(*attegit.Repo, reference.Blob, T) (reference.Blob, string, error)
}

// TargetKindSpec describes the independent capabilities of an attached kind.
// Build one with NewTargetKindSpec; the zero value has no decoder and is
// rejected on attachment.
type TargetKindSpec struct {
	schema    *hcl.BodySchema
	decode    func(*hcl.BodyContent, *hcl.EvalContext) (any, error)
	graph     TargetGraphProjection
	execution func(Target, string) (TargetCommand, error)
	config    func(Target) (map[string]any, error)
	script    func(*attegit.Repo, reference.Blob, any) (reference.Blob, string, error)
}

// NewTargetKindSpec returns a TargetKindSpec whose decoder produces a T. The
// schema is copied. A nil decode leaves the spec without a decoder, which
// attachment rejects. Every option is optional.
func NewTargetKindSpec[T any](
	schema *hcl.BodySchema,
	decode func(*hcl.BodyContent, *hcl.EvalContext) (T, error),
	options TargetKindOptions[T],
) TargetKindSpec {
	spec := TargetKindSpec{schema: copyBodySchema(schema)}
	if decode != nil {
		spec.decode = func(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
			return decode(content, ctx)
		}
	}
	if graph := options.Graph; graph != nil {
		spec.graph = func(ctx context.Context, repo *attegit.Repo, target Target, graphContext TargetGraphContext, attachToTree bool) (TargetGraph, error) {
			decoded, err := TargetDecodedAs[T](target)
			if err != nil {
				return TargetGraph{}, err
			}
			return graph(ctx, repo, target, decoded, graphContext, attachToTree)
		}
	}
	if execution := options.Execution; execution != nil {
		spec.execution = func(target Target, root string) (TargetCommand, error) {
			decoded, err := TargetDecodedAs[T](target)
			if err != nil {
				return TargetCommand{}, err
			}
			return execution(target, decoded, root)
		}
	}
	if config := options.Config; config != nil {
		spec.config = func(target Target) (map[string]any, error) {
			decoded, err := TargetDecodedAs[T](target)
			if err != nil {
				return nil, err
			}
			return config(target, decoded)
		}
	}
	if script := options.Script; script != nil {
		spec.script = func(repo *attegit.Repo, file reference.Blob, decoded any) (reference.Blob, string, error) {
			value, err := decodedAs[T](fmt.Sprintf("script of %q", file), decoded)
			if err != nil {
				return "", "", err
			}
			return script(repo, file, value)
		}
	}
	return spec
}

// Schema returns a copy of the target body schema, or nil when none was set.
func (spec TargetKindSpec) Schema() *hcl.BodySchema {
	return copyBodySchema(spec.schema)
}

// WithSchema returns spec with a copy of schema as its body schema.
func (spec TargetKindSpec) WithSchema(schema *hcl.BodySchema) TargetKindSpec {
	spec.schema = copyBodySchema(schema)
	return spec
}

// HasDecoder reports whether spec can decode a body.
func (spec TargetKindSpec) HasDecoder() bool {
	return spec.decode != nil
}

// Decode decodes content for a target of the given kind. The returned Target
// carries the decoded value and the optional projections; the caller supplies
// its identity and source fields.
func (spec TargetKindSpec) Decode(kind TargetKind, content *hcl.BodyContent, ctx *hcl.EvalContext) (Target, error) {
	if spec.decode == nil {
		return Target{}, fmt.Errorf("target kind %q has no decoder", kind)
	}
	decoded, err := spec.decode(content, ctx)
	if err != nil {
		return Target{}, err
	}
	if err := checkDecoded(fmt.Sprintf("target kind %q", kind), decoded); err != nil {
		return Target{}, err
	}
	return Target{
		decoded:   decoded,
		graph:     spec.graph,
		execution: spec.execution,
		config:    spec.config,
		script:    spec.script,
	}, nil
}

// TargetKindCapabilities exposes target-kind specifications to HCL evaluation.
type TargetKindCapabilities interface {
	TargetKinds() map[TargetKind]TargetKindSpec
}
