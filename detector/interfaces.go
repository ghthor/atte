package detector

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
)

// Detector describes the capabilities used to register a detector.
type Detector interface {
	Namespace() string
}

// GraphDetector constructs a detector graph.
// Detector implementations may expose additional methods that convert their
// neutral Targets into selector.Target values for user-facing matching.
type GraphDetector interface {
	Detector
	Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
}

// TargetDetector discovers detector targets. Selector mappings are registered
// independently by the detector package with reference/selector.
type TargetDetector interface {
	Detector
	Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
}

// EntityDecoder validates an entity ID and returns its detector-specific kind.
// IDs must begin with the detector namespace followed by a colon. A detector
// may return its namespace as the kind when the ID does not encode a more
// specific kind, as with attegit path IDs.
type EntityDecoder interface {
	Detector
	DecodeID(id graph.EntityID) (graph.Entity, error)
}
