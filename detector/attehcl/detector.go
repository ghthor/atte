package attehcl

import (
	"context"
	"strings"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
)

// Detector adapts the HCL detector to the shared detector capabilities.
type Detector struct {
	Provider detector.FunctionProvider
}

func NewDetector(provider detector.FunctionProvider) Detector {
	return Detector{Provider: provider}
}

func (d Detector) Namespace() string { return Namespace }

func (d Detector) Graph(ctx context.Context, repo *attegit.Repo, options ...detector.GraphOption) (*graph.Graph, error) {
	graphOptions := make([]detector.GraphOption, 0, len(options)+2)
	graphOptions = append(graphOptions, detector.WithAttachToTree(), WithFunctions(d.Provider))
	for _, option := range options {
		if option != nil {
			graphOptions = append(graphOptions, option)
		}
	}
	return Graph(ctx, repo, graphOptions...)
}

func (d Detector) Targets(ctx context.Context, repo *attegit.Repo) ([]detector.Target, error) {
	found, err := Targets(ctx, repo, d.Provider)
	if err != nil {
		return nil, err
	}
	result := make([]detector.Target, 0, len(found))
	for _, target := range found {
		if target.Script == "" {
			continue
		}
		result = append(result, detector.Target{
			ID:        target.ID,
			Namespace: Namespace,
			Kind:      target.Kind,
			Path:      target.File.String(),
			Name:      target.Name,
			Index:     target.Index,
			Aliases:   target.Aliases,
		})
	}
	return result, nil
}

func (Detector) Selector(target detector.Target) (selector.Target, bool) {
	return Selector(Target{Kind: target.Kind, File: reference.Blob(target.Path), Name: target.Name, Index: target.Index, Aliases: target.Aliases}), true
}

func (Detector) MatchIdentifier(target detector.Target, identifier string) bool {
	kind := strings.TrimPrefix(target.Kind, Namespace+":")
	return selector.Target{Kind: kind, Name: target.Name, Index: target.Index, Aliases: target.Aliases}.Matches("#"+identifier, "")
}
