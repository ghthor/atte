// Package graphtarget defines the detector-neutral identity of a graph node.
package graphtarget

import "github.com/ghthor/atte/detector/graph"

// ID is the detector-neutral, long-form identity of a graph node.
//
// It preserves the graph entity ID and detector namespace alongside the
// detector-specific kind and repository path. This representation is intended
// for graph and registry operations; it is not the user-facing selector format.
// Convert it to a selector.Target when constructing CLI identifiers.
//
// TODO: Consider moving ID into the graph package because it directly
// translates to the string format used as a graph node.
type ID struct {
	ID        graph.EntityID
	Namespace string
	Kind      string
	Path      string
	Name      string
	Index     int
	Aliases   []string
}
