package attehcl

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/ghthor/atte/reference/target"
	"github.com/hashicorp/hcl/v2"
)

func targetsFromEvaluated(repo *attegit.Repo, evaluated []evaluatedTarget) (map[Kind][]Target, error) {
	targets := make(map[Kind][]Target, len(evaluated))
	for _, item := range evaluated {
		target := targetFromEvaluated(item)
		if item.Spec.Script != nil {
			script, inline, err := item.Spec.Script(repo, item.File, item.Decoded)
			if err != nil {
				return nil, err
			}
			target.Script = script
			target.Inline = inline
		}
		targets[item.Kind] = append(targets[item.Kind], target)
	}
	return targets, nil
}

func targetFromEvaluated(item evaluatedTarget) Target {
	identity := targetIdentityFor(item.File, item.Kind, item.Name, item.Index)
	return Target{
		ID:        identity.ID,
		Kind:      identity.Kind,
		File:      item.File,
		Name:      item.Name,
		Label:     item.Label,
		Index:     item.Index,
		Aliases:   identity.Aliases,
		Decoded:   item.Decoded,
		graph:     item.Spec.Graph,
		execution: item.Spec.Execution,
		config:    item.Spec.Config,
		Source:    item.Source,
	}
}

func builtinTargetScript(repo *attegit.Repo, file reference.Blob, decoded any) (reference.Blob, string, error) {
	target, ok := decoded.(decodedTarget)
	if !ok {
		return "", "", nil
	}
	if script, ok := strings.CutPrefix(target.Script, DecodingPathPrefix); ok {
		return reference.Blob(script), "", nil
	}
	if target.Script == "" {
		return "", "", nil
	}
	trimmed := strings.TrimSpace(target.Script)
	if object, ok := repo.Obj[reference.Blob(trimmed)]; ok {
		return "", "", fmt.Errorf(
			"decode HCL %q: script string resolves to repository %q %q; use path(%q) for an external script",
			file,
			object.Kind,
			trimmed,
			trimmed,
		)
	}
	return "", target.Script, nil
}

func EntityID(kind string, file reference.Blob, name string) graph.EntityID {
	return graph.EntityID(fmt.Sprintf("%s:%s:%s", kind, file, name))
}

type targetIdentity struct {
	ID          graph.EntityID
	Kind        string
	DisplayName string
	Selector    selector.Target
	Aliases     []string
}

func targetIdentityFor(file reference.Blob, kind Kind, name string, index int) targetIdentity {
	display := displayName(name, index)
	selectorIdentity := selector.Target{
		Path:  file.String(),
		Kind:  string(kind),
		Name:  display,
		Index: index,
	}
	aliases := selector.Aliases(selectorIdentity)
	selectorIdentity.Aliases = aliases
	return targetIdentity{
		ID:          EntityID(Namespace+":"+string(kind), file, display),
		Kind:        Namespace + ":" + string(kind),
		DisplayName: display,
		Selector:    selectorIdentity,
		Aliases:     aliases,
	}
}

func DecodeEntityID(id graph.EntityID, scanner TargetCapabilities) (string, reference.Blob, string, error) {
	parts := strings.SplitN(string(id), ":", 4)
	if len(parts) != 4 || parts[0] != Namespace {
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	kind := parts[0] + ":" + parts[1]

	kinds := targetKinds(scanner)
	if _, ok := kinds[Kind(parts[1])]; !ok {
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	file, err := reference.ParseBlob(parts[2])
	if err != nil {
		return "", "", "", fmt.Errorf("invalid HCL file path %q: %w", parts[2], err)
	}
	return kind, file, parts[3], nil
}

// Target describes one target declared by a attached kind in an atte.hcl
// file. Targets are returned in repository order. Named targets retain their
// source label in Name; anonymous targets retain an empty Name and use Index
// for their kind-local source position. Script is a repository-relative path
// resolved from an explicit path expression.
//
// Script is the repository-relative path resolved from an explicit path
// expression; Inline contains ordinary string script content. Both are empty
// only when the corresponding value is not present. ID is the corresponding graph entity
// identifier, and Selector converts the target to the canonical CLI selector.
//
// Kind identifies the attached target kind. File identifies the declaring
// atte.hcl file. Label preserves the HCL block label, when present, while
// Index records the block's zero-based position within its kind. Decoded contains
// the value returned by the kind's attached decoder.
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
	Decoded any
	Source  hcl.Range

	graph     TargetGraphProjection
	execution TargetExecutionProjection
	config    TargetConfigProjection
}

// DisplayName returns the stable display identifier component for the target.
// Anonymous targets use their kind-local source index.
func (target Target) DisplayName() string {
	return displayName(target.Name, target.Index)
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
		graph.Relationship{From: target.ID, To: file, Kind: SourceFileRelation},
		graph.Relationship{From: tree, To: target.ID, Kind: attegit.ContainsRelation},
	)
	return result
}

// Configuration returns the standard target identity and the attached kind's
// optional configuration projection.
func (value Target) Configuration() (target.Computed, error) {
	computed := target.Computed{
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
		return target.Computed{}, fmt.Errorf("project configuration for target %q: %w", value.ID, err)
	}
	for key := range extra {
		switch key {
		case "kind", "file", "name", "label", "index", "script", "inline":
			return target.Computed{}, fmt.Errorf("project configuration for target %q: key %q conflicts with target identity", value.ID, key)
		}
	}
	if len(extra) > 0 {
		computed.Meta = extra
	}
	return computed, nil
}

func executeScriptTarget(target Target, root string) (TargetCommand, error) {
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(target.File.String())))
	if target.Script != "" {
		return TargetCommand{Dir: dir, Args: []string{"/usr/bin/env", "bash", filepath.Join(root, filepath.FromSlash(target.Script.String()))}}, nil
	}
	if target.Inline != "" {
		return TargetCommand{Dir: dir, Args: []string{"/usr/bin/env", "bash", "-c", target.Inline}}, nil
	}
	return TargetCommand{}, fmt.Errorf("target %q has no script", target.ID)
}

func configScriptTarget(Target) (map[string]any, error) {
	return nil, nil
}

// Selector converts a target to its selector-facing identity.
func Selector(target Target) selector.Target {
	kind := strings.TrimPrefix(target.Kind, Namespace+":")
	identity := targetIdentityFor(target.File, Kind(kind), target.Name, target.Index)
	identity.Selector.Aliases = target.Aliases
	return identity.Selector
}
