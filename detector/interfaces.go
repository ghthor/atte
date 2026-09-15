package detector

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
)

// Sensor identifies one attached detector implementation.
type Sensor interface {
	Namespace() string
}

// GraphSensor is capable of adding relationships between entities to the
// dependency graph. Sensor implementations may expose additional methods that
// convert their neutral Targets into selector.Target values for user-facing
// matching.
type GraphSensor interface {
	Sensor
	Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
}

// TargetSensor is capable of finding targets. Selector mappings are configured
// independently by the detector package with reference/selector.
type TargetSensor interface {
	Sensor
	Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
}

// EntityDecodingSensor validates an entity ID and returns its detector-specific
// kind. IDs must begin with the detector namespace followed by a colon. A
// Sensor may return its namespace as the kind when the ID does not encode a
// more specific kind, as with attegit path IDs.
type EntityDecodingSensor interface {
	Sensor
	DecodeID(id graph.EntityID) (graph.Entity, error)
}

// SensorProvidingHCLFunctions supplies repository- and file-aware HCL
// function factories during attachment.
type SensorProvidingHCLFunctions interface {
	Sensor
	HCLFunctions() map[string]HCLFunctionFactory
}

// SensorProvidingHCLBlocks supplies HCL target-kind specifications during
// attachment.
type SensorProvidingHCLBlocks interface {
	Sensor
	HCLBlocks() map[attehcl.Kind]attehcl.TargetKindSpec
}
