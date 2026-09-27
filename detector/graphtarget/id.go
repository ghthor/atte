// Package graphtarget defines the detector-neutral identity of a graph node.
package graphtarget

import "github.com/ghthor/atte/detector/graph"

// Namespace identifies the detector namespace responsible for a graph target.
type Namespace string

// ID is the detector-neutral, long-form identity of a graph node.
//
// It preserves the graph entity ID and detector namespace alongside the
// detector-specific kind and repository path. This representation is intended
// for graph and Scanner operations; it is not the user-facing selector format.
// Use the owning Scanner's TargetSelector capability to obtain its presentation.
//
// TODO: Consider moving ID into the graph package because it directly
// translates to the string format used as a graph node.
type ID struct {
	ID        graph.EntityID
	Namespace Namespace
	Kind      string
	Path      string
	Name      string
	Label     string // Optional detector-owned label for command-layer fallback.
	Index     int
	Aliases   []string
}

// Execution is the executable command produced for a discovered target.
// Args contains the executable followed by its arguments; Dir is the process
// working directory and may be empty to inherit the caller's directory.
type Execution struct {
	Dir  string
	Args []string
}
