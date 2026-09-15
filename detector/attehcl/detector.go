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

// Detector adapts the HCL Sensor to the shared detector capabilities.
type Detector struct {
	scanner Capabilities
}

// NewDetector creates an HCL Sensor backed by the supplied Scanner.
func NewDetector(scanner Capabilities) Detector {
	return Detector{scanner: scanner}
}

func (d Detector) Namespace() string { return Namespace }

// DecodeID converts an attehcl entity ID into the shared graph representation.
func (d Detector) DecodeID(id graph.EntityID) (graph.Entity, error) {
	kind, _, _, err := DecodeEntityID(id, d.scanner)
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
	return Graph(ctx, repo, d.scanner, options...)
}

func (d Detector) Targets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	return DeclaredTargets(ctx, repo, d.scanner)
}
