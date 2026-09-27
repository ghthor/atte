package attehcl

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcltarget"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
)

func targetsFromEvaluated(repo *attegit.Repo, evaluated []evaluatedTarget) (map[attehcltarget.Kind][]attehcltarget.Target, error) {
	targets := make(map[attehcltarget.Kind][]attehcltarget.Target, len(evaluated))
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

func targetFromEvaluated(item evaluatedTarget) attehcltarget.Target {
	identity := targetIdentityFor(item.File, item.Kind, item.Name, item.Index)
	target := attehcltarget.Target{
		ID:      identity.ID,
		Kind:    identity.Kind,
		File:    item.File,
		Name:    item.Name,
		Label:   item.Label,
		Index:   item.Index,
		Aliases: identity.Aliases,
		Decoded: item.Decoded,
		Source:  item.Source,
	}
	return attehcltarget.Bind(target, item.Spec)
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

func targetIdentityFor(file reference.Blob, kind attehcltarget.Kind, name string, index int) targetIdentity {
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

func DecodeEntityID(id graph.EntityID, scanner attehcltarget.KindCapabilities) (string, reference.Blob, string, error) {
	parts := strings.SplitN(string(id), ":", 4)
	if len(parts) != 4 || parts[0] != Namespace {
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	kind := parts[0] + ":" + parts[1]

	kinds := targetKinds(scanner)
	if _, ok := kinds[attehcltarget.Kind(parts[1])]; !ok {
		return "", "", "", fmt.Errorf("invalid attehcl entity ID %q", id)
	}
	file, err := reference.ParseBlob(parts[2])
	if err != nil {
		return "", "", "", fmt.Errorf("invalid HCL file path %q: %w", parts[2], err)
	}
	return kind, file, parts[3], nil
}

func executeScriptTarget(target attehcltarget.Target, root string) (attehcltarget.Command, error) {
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(target.File.String())))
	if target.Script != "" {
		return attehcltarget.Command{Dir: dir, Args: []string{"/usr/bin/env", "bash", filepath.Join(root, filepath.FromSlash(target.Script.String()))}}, nil
	}
	if target.Inline != "" {
		return attehcltarget.Command{Dir: dir, Args: []string{"/usr/bin/env", "bash", "-c", target.Inline}}, nil
	}
	return attehcltarget.Command{}, fmt.Errorf("target %q has no script", target.ID)
}

func configScriptTarget(attehcltarget.Target) (map[string]any, error) {
	return nil, nil
}

// Selector converts a target to its selector-facing identity.
func Selector(target attehcltarget.Target) selector.Target {
	kind := strings.TrimPrefix(target.Kind, Namespace+":")
	identity := targetIdentityFor(target.File, attehcltarget.Kind(kind), target.Name, target.Index)
	identity.Selector.Aliases = target.Aliases
	return identity.Selector
}
