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
	Plugin Plugin
}

// NewDetector creates an HCL detector backed by the supplied plugin registry.
func NewDetector(registry Plugin) Detector {
	return Detector{Plugin: registry}
}

func (d Detector) Namespace() string { return Namespace }

// DecodeID converts an attehcl entity ID into the shared graph representation.
func (d Detector) DecodeID(id graph.EntityID) (graph.Entity, error) {
	kind, _, _, err := DecodeEntityID(id, d.Plugin)
	if err != nil {
		return graph.Entity{}, err
	}
	return graph.Entity{ID: id, Kind: graph.EntityKind(kind)}, nil
}

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
	return Graph(ctx, repo, d.Plugin, options...)
}

func (d Detector) Targets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	return DeclaredTargets(ctx, repo, d.Plugin)
}
