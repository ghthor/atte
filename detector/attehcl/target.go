package attehcl

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl/hclblock"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
)

func targetsFromEvaluated(repo *attegit.Repo, evaluated []evaluatedTarget) (map[hclblock.TargetKind][]hclblock.Target, error) {
	targets := make(map[hclblock.TargetKind][]hclblock.Target, len(evaluated))
	for _, item := range evaluated {
		target, err := targetFromEvaluated(item).ResolveScript(repo)
		if err != nil {
			return nil, err
		}
		targets[item.Kind] = append(targets[item.Kind], target)
	}
	return targets, nil
}

func targetFromEvaluated(item evaluatedTarget) hclblock.Target {
	identity := targetIdentityFor(item.File, item.Kind, item.Name, item.Index)
	target := item.Bound
	target.ID = identity.ID
	target.Kind = identity.Kind
	target.File = item.File
	target.Name = item.Name
	target.Label = item.Label
	target.Index = item.Index
	target.Aliases = identity.Aliases
	target.Source = item.Source
	return target
}

func builtinTargetScript(repo *attegit.Repo, file reference.Blob, target decodedTarget) (reference.Blob, string, error) {
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

func targetIdentityFor(file reference.Blob, kind hclblock.TargetKind, name string, index int) targetIdentity {
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

func DecodeEntityID(id graph.EntityID, scanner hclblock.TargetKindCapabilities) (string, reference.Blob, string, error) {
	parts := strings.SplitN(string(id), ":", 4)
	if len(parts) != 4 || parts[0] != Namespace {
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	kind := parts[0] + ":" + parts[1]

	kinds := targetKinds(scanner)
	if _, ok := kinds[hclblock.TargetKind(parts[1])]; !ok {
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	file, err := reference.ParseBlob(parts[2])
	if err != nil {
		return "", "", "", fmt.Errorf("invalid HCL file path %q: %w", parts[2], err)
	}
	return kind, file, parts[3], nil
}

func executeScriptTarget(target hclblock.Target, _ decodedTarget, root string) (hclblock.TargetCommand, error) {
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(target.File.String())))
	if target.Script != "" {
		return hclblock.TargetCommand{
			Dir:  dir,
			Args: []string{"/usr/bin/env", "bash", filepath.Join(root, filepath.FromSlash(target.Script.String()))},
		}, nil
	}
	if target.Inline != "" {
		return hclblock.TargetCommand{Dir: dir, Args: []string{"/usr/bin/env", "bash", "-c", target.Inline}}, nil
	}
	return hclblock.TargetCommand{}, fmt.Errorf("target %q has no script", target.ID)
}

func configScriptTarget(hclblock.Target, decodedTarget) (map[string]any, error) {
	return nil, nil
}

// Selector converts a target to its selector-facing identity.
func Selector(target hclblock.Target) selector.Target {
	kind := strings.TrimPrefix(target.Kind, Namespace+":")
	identity := targetIdentityFor(target.File, hclblock.TargetKind(kind), target.Name, target.Index)
	identity.Selector.Aliases = target.Aliases
	return identity.Selector
}
