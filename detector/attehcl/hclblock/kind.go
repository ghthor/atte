// Package hclblock defines the HCL block APIs shared by detector registration
// and HCL evaluation.
package hclblock

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
)

// TargetKind identifies an attached target kind and its target namespace.
type TargetKind string

// TargetDecoder decodes a schema-validated target body into a kind-owned value.
type TargetDecoder func(*hcl.BodyContent, *hcl.EvalContext) (any, error)

// TargetGraphProjection projects a target into graph entities and relationships.
// The attachToTree argument requests the common file/tree containment relations.
type TargetGraphProjection func(context.Context, *attegit.Repo, Target, TargetGraphContext, bool) (TargetGraph, error)

// TargetExecutionProjection constructs the command used to run a target. Args
// contains the executable and its arguments; Dir is the process working directory.
type TargetExecutionProjection func(Target, string) (TargetCommand, error)

// TargetConfigProjection adds decoded target data to config show output. The
// returned keys must not overlap the standard target identity fields.
type TargetConfigProjection func(Target) (map[string]any, error)

// TargetScriptProjection resolves a decoded target's script into a repository path
// or inline command.
type TargetScriptProjection func(*attegit.Repo, reference.Blob, any) (reference.Blob, string, error)

// TargetKindSpec describes the independent capabilities of an attached kind.
// Decoder is required; the other projections are optional.
type TargetKindSpec struct {
	Schema    *hcl.BodySchema
	Decoder   TargetDecoder
	Graph     TargetGraphProjection
	Execution TargetExecutionProjection
	Config    TargetConfigProjection
	Script    TargetScriptProjection
}

// TargetKindCapabilities exposes target-kind specifications to HCL evaluation.
type TargetKindCapabilities interface {
	TargetKinds() map[TargetKind]TargetKindSpec
}
