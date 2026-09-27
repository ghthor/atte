// Package attehcltarget contains the target-kind API shared by detector
// registration and HCL evaluation.
package attehcltarget

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
)

// Kind identifies an attached target kind and its target namespace.
type Kind string

// Decoder decodes a schema-validated target body into a kind-owned value.
type Decoder func(*hcl.BodyContent, *hcl.EvalContext) (any, error)

// GraphProjection projects a target into graph entities and relationships. The
// attachToTree argument requests the common file/tree containment relations.
type GraphProjection func(context.Context, *attegit.Repo, Target, GraphContext, bool) (Graph, error)

// ExecutionProjection constructs the command used to run a target. Args
// contains the executable and its arguments; Dir is the process working directory.
type ExecutionProjection func(Target, string) (Command, error)

// ConfigProjection adds decoded target data to config show output. The returned
// keys must not overlap the standard target identity fields.
type ConfigProjection func(Target) (map[string]any, error)

// ScriptProjection resolves a decoded target's script into a repository path
// or inline command.
type ScriptProjection func(*attegit.Repo, reference.Blob, any) (reference.Blob, string, error)

// KindSpec describes the independent capabilities of an attached kind. Decoder
// is required; the other projections are optional.
type KindSpec struct {
	Schema    *hcl.BodySchema
	Decoder   Decoder
	Graph     GraphProjection
	Execution ExecutionProjection
	Config    ConfigProjection
	Script    ScriptProjection
}

// KindCapabilities exposes target-kind specifications to HCL evaluation.
type KindCapabilities interface {
	TargetKinds() map[Kind]KindSpec
}
