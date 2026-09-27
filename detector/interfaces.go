package detector

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcltarget"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference/selector"
)

// Sensor identifies one attached detector implementation.
type Sensor interface {
	Namespace() string
}

// GraphSensor is capable of adding relationships between entities to the
// dependency graph.
type GraphSensor interface {
	Sensor
	Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
}

// TargetSensor discovers executable targets and owns their selector and
// execution capabilities. Discovery and presentation are one all-or-nothing
// capability so a Scanner never exposes targets it cannot render or execute.
type TargetSensor interface {
	Sensor
	Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
	TargetSelector(graphtarget.ID) selector.Target
	ExecuteTarget(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error)
}

// EntityDecodingSensor validates an entity ID and returns its detector-specific
// kind. IDs must begin with the detector namespace followed by a colon. A
// Sensor may return its namespace as the kind when the ID does not encode a
// more specific kind, as with attegit path IDs.
type EntityDecodingSensor interface {
	Sensor
	DecodeID(id graph.EntityID) (graph.Entity, error)
}

// SensorWithScanner opts a Sensor into receiving the compiled Scanner during
// Builder.Compile. Implementations may validate the Scanner against a local
// capability interface and return a new bound Sensor to keep compiled snapshots
// isolated.
type SensorWithScanner interface {
	AttachScanner(Scanner) (Sensor, error)
}

// SensorProvidingHCLFunctions supplies repository- and file-aware HCL
// function factories during attachment.
type SensorProvidingHCLFunctions interface {
	Sensor
	HCLFunctions() map[string]HCLFunctionFactory
}

// SensorProvidingHCLTargetBlocks supplies HCL target-kind specifications during
// attachment.
type SensorProvidingHCLTargetBlocks interface {
	Sensor
	HCLTargetBlocks() map[attehcltarget.Kind]attehcltarget.KindSpec
}
