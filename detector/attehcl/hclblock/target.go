package hclblock

import (
	"fmt"
	"os/exec"
	"strconv"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	referencetarget "github.com/ghthor/atte/reference/target"
	"github.com/hashicorp/hcl/v2"
)

// TargetSourceFileRelation connects a target to the atte.hcl file that declares it.
const TargetSourceFileRelation graph.RelationKind = "source-file"

// TargetGraphContext provides common dependency resolution to a graph projector.
type TargetGraphContext struct {
	ResolveTarget   func(hcl.Traversal) (graph.Entity, error)
	ResolveTargetAt func(reference.Blob, hcl.Traversal) (graph.Entity, error)
	EntityKind      func(graph.EntityID) (graph.EntityKind, error)
}

// TargetGraph is the graph projection of one target.
type TargetGraph struct {
	Entities      []graph.Entity
	Relationships []graph.Relationship
}

// TargetCommand is the executable representation of one target.
type TargetCommand struct {
	Dir  string
	Args []string
}

// Target describes one target declared by an attached kind in an atte.hcl file.
// Its fields carry the graph identity, source location, and optional script
// data; the kind-owned decoded value is read with TargetDecodedAs. The optional
// projections are bound from TargetKindSpec by TargetKindSpec.Decode.
type Target struct {
	ID      graph.EntityID
	Kind    string
	File    reference.Blob
	Name    string
	Label   string
	Index   int
	Aliases []string
	Script  reference.Blob
	Inline  string
	Source  hcl.Range

	decoded   any
	graph     TargetGraphProjection
	execution func(Target, string) (TargetCommand, error)
	config    func(Target) (map[string]any, error)
	script    func(*attegit.Repo, reference.Blob, any) (reference.Blob, string, error)
}

// TargetDecodedAs returns the kind-owned decoded value of target as a T. The
// error names the expected and actual types.
func TargetDecodedAs[T any](target Target) (T, error) {
	return decodedAs[T](fmt.Sprintf("target %q", target.ID), target.decoded)
}

// ResolveScript returns target with Script and Inline set from the kind's
// script projection. Targets whose kind has no script projection are returned
// unchanged.
func (target Target) ResolveScript(repo *attegit.Repo) (Target, error) {
	if target.script == nil {
		return target, nil
	}
	script, inline, err := target.script(repo, target.File, target.decoded)
	if err != nil {
		return Target{}, err
	}
	target.Script = script
	target.Inline = inline
	return target, nil
}

// DisplayName returns the stable display identifier component for the target.
// Anonymous targets use their kind-local source index.
func (target Target) DisplayName() string {
	if target.Name != "" {
		return target.Name
	}
	return strconv.Itoa(target.Index)
}

// Runnable reports whether the attached kind supplied an execution projection.
func (target Target) Runnable() bool {
	return target.execution != nil
}

// Execution constructs the executable representation of the target from a repository root.
func (target Target) Execution(root string) (TargetCommand, error) {
	if target.execution == nil {
		return TargetCommand{}, fmt.Errorf("target %q has no execution projection", target.ID)
	}
	command, err := target.execution(target, root)
	if err != nil {
		return TargetCommand{}, fmt.Errorf("construct command for target %q: %w", target.ID, err)
	}
	if len(command.Args) == 0 {
		return TargetCommand{}, fmt.Errorf("construct command for target %q: command has no arguments", target.ID)
	}
	return command, nil
}

// Command constructs the command used to execute the target from a repository root.
func (target Target) Command(root string) (*exec.Cmd, error) {
	command, err := target.Execution(root)
	if err != nil {
		return nil, err
	}
	process := exec.Command(command.Args[0], command.Args[1:]...)
	process.Dir = command.Dir
	return process, nil
}

// GraphProjection returns the optional graph projection supplied by the kind.
func (target Target) GraphProjection() TargetGraphProjection {
	return target.graph
}

// GraphProjectionBase returns the target entity and, when requested, the common
// declaring-file and tree entities and relationships for a target.
func (target Target) GraphProjectionBase(attachToTree bool) TargetGraph {
	result := TargetGraph{Entities: []graph.Entity{{ID: target.ID, Kind: graph.EntityKind(target.Kind)}}}
	if !attachToTree {
		return result
	}
	tree := attegit.EntityID(target.File.Tree())
	file := attegit.EntityID(target.File)
	result.Entities = append(result.Entities,
		graph.Entity{ID: tree, Kind: attegit.TreeKind},
		graph.Entity{ID: file, Kind: attegit.BlobKind},
	)
	result.Relationships = append(result.Relationships,
		graph.Relationship{From: target.ID, To: file, Kind: TargetSourceFileRelation},
		graph.Relationship{From: tree, To: target.ID, Kind: attegit.ContainsRelation},
	)
	return result
}

// Configuration returns the standard target identity and the attached kind's
// optional configuration projection.
func (value Target) Configuration() (referencetarget.Computed, error) {
	computed := referencetarget.Computed{
		ID:     string(value.ID),
		Kind:   value.Kind,
		File:   value.File.String(),
		Name:   value.DisplayName(),
		Label:  value.Label,
		Index:  value.Index,
		Script: value.Script.String(),
		Inline: value.Inline,
	}
	if value.config == nil {
		return computed, nil
	}
	extra, err := value.config(value)
	if err != nil {
		return referencetarget.Computed{}, fmt.Errorf("project configuration for target %q: %w", value.ID, err)
	}
	for key := range extra {
		switch key {
		case "id", "kind", "file", "name", "label", "index", "script", "inline":
			return referencetarget.Computed{}, fmt.Errorf("project configuration for target %q: key %q conflicts with target identity", value.ID, key)
		}
	}
	if len(extra) > 0 {
		computed.Meta = extra
	}
	return computed, nil
}
