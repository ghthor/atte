package detector

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference/selector"
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

// TargetDetector discovers and canonicalizes detector targets.
type TargetDetector interface {
	Detector
	Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
	Selector(graphtarget.ID) (selector.Target, bool)
	MatchIdentifier(graphtarget.ID, string) bool
}
