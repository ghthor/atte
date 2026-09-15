package detector

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
)

// Sensor identifies one registered detector implementation.
type Sensor interface {
	Namespace() string
}

// SensorGraph constructs a detector graph.
// Sensor implementations may expose additional methods that convert their
// neutral Targets into selector.Target values for user-facing matching.
type SensorGraph interface {
	Sensor
	Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
}

// SensorTarget discovers detector targets. Selector mappings are registered
// independently by the detector package with reference/selector.
type SensorTarget interface {
	Sensor
	Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
}

// SensorEntityDecoder validates an entity ID and returns its detector-specific
// kind. IDs must begin with the detector namespace followed by a colon. A
// Sensor may return its namespace as the kind when the ID does not encode a
// more specific kind, as with attegit path IDs.
type SensorEntityDecoder interface {
	Sensor
	DecodeID(id graph.EntityID) (graph.Entity, error)
}
