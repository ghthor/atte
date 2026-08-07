package attehcl

import (
	"context"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
)

// Detector adapts the HCL detector to the shared detector capabilities.
type Detector struct {
	Provider graphset.FunctionProvider
}

func NewDetector(provider graphset.FunctionProvider) Detector {
	return Detector{Provider: provider}
}

func (d Detector) Namespace() string { return Namespace }

func init() {
	if err := selector.Register(Namespace, matchSelector, renderSelector); err != nil {
		panic(err)
	}
}

func matchSelector(target graphtarget.ID, identifier string) bool {
	kind := strings.TrimPrefix(target.Kind, Namespace+":")
	return selector.Target{Kind: kind, Name: target.Name, Index: target.Index, Aliases: target.Aliases}.Matches("#"+identifier, "")
}

func renderSelector(target graphtarget.ID) string {
	return Selector(Target{Kind: target.Kind, File: reference.Blob(target.Path), Name: target.Name, Index: target.Index, Aliases: target.Aliases}).String()
}

func (d Detector) Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
	graphOptions := make([]graphset.Option, 0, len(options)+2)
	graphOptions = append(graphOptions, graphset.WithAttachToTree(), WithFunctions(d.Provider))
	for _, option := range options {
		if option != nil {
			graphOptions = append(graphOptions, option)
		}
	}
	return Graph(ctx, repo, graphOptions...)
}

func (d Detector) Targets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	found, err := Targets(ctx, repo, d.Provider)
	if err != nil {
		return nil, err
	}
	result := make([]graphtarget.ID, 0, len(found))
	for _, target := range SortedTargets(found) {
		if target.Script == "" && target.Inline == "" {
			continue
		}
		name := target.DisplayName()
		result = append(result, graphtarget.ID{
			ID:        target.ID,
			Namespace: Namespace,
			Kind:      target.Kind,
			Path:      target.File.String(),
			Name:      name,
			Index:     target.Index,
			Aliases:   target.Aliases,
		})
	}
	return result, nil
}
