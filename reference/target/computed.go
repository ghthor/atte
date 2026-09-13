// Package target contains target configuration value types shared by consumers.
package target

// Computed is the common configuration representation of an evaluated target.
// Meta contains kind-specific configuration fields.
type Computed struct {
	Kind   string         `json:"kind"`
	File   string         `json:"file"`
	Name   string         `json:"name"`
	Label  string         `json:"label,omitempty"`
	Index  int            `json:"index"`
	Script string         `json:"script,omitempty"`
	Inline string         `json:"inline,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}
