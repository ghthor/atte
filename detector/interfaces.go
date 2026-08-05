package detector

import (
	"context"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/graph"
	"github.com/ghthor/atte/reference/selector"
)

// Target is the detector-neutral, long-form identity of a graph node.
//
// It preserves the graph entity ID and detector namespace alongside the
// detector-specific kind and repository path. This representation is intended
// for graph and registry operations; it is not the user-facing selector format.
// Convert it to a selector.Target when constructing CLI identifiers.
type Target struct {
	ID        graph.EntityID
	Namespace string
	Kind      string
	Path      string
	Name      string
	Index     int
	Aliases   []string
}

// Detector describes the capabilities used to register a detector.
type Detector interface {
	Namespace() string
}

// GraphDetector constructs a detector graph.
// Detector implementations may expose additional methods that convert their
// neutral Targets into selector.Target values for user-facing matching.
type GraphDetector interface {
	Detector
	Graph(context.Context, *attegit.Repo, ...GraphOption) (*graph.Graph, error)
}

// TargetDetector discovers and canonicalizes detector targets.
type TargetDetector interface {
	Detector
	Targets(context.Context, *attegit.Repo) ([]Target, error)
	Selector(Target) (selector.Target, bool)
	MatchIdentifier(Target, string) bool
}
