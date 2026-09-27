package attehcl

import (
	"context"
	"fmt"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcltarget"
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

// NewDetector creates an HCL Sensor that can receive a compiled Scanner.
func NewDetector() *Detector {
	return &Detector{}
}

// AttachScanner validates the compiled Scanner capabilities and returns a
// Sensor bound to them.
func (Detector) AttachScanner(scanner any) (any, error) {
	capabilities, ok := scanner.(Capabilities)
	if !ok {
		return nil, fmt.Errorf("scanner %T does not provide HCL capabilities", scanner)
	}
	return &Detector{scanner: capabilities}, nil
}

// HCLTargetBlocks provides the HCL target-kind specifications contributed by
// the Sensor during attachment.
func (Detector) HCLTargetBlocks() map[attehcltarget.Kind]attehcltarget.KindSpec {
	return BuiltInTargetKinds()
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

func (Detector) TargetSelector(target graphtarget.ID) selector.Target {
	return Selector(attehcltarget.Target{
		Kind:    target.Kind,
		File:    reference.Blob(target.Path),
		Name:    target.Name,
		Index:   target.Index,
		Aliases: target.Aliases,
	})
}

func (d Detector) Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
	return Graph(ctx, repo, d.scanner, options...)
}

func (d Detector) Targets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	grouped, err := Targets(ctx, repo, d.scanner)
	if err != nil {
		return nil, err
	}
	nativeTargets := SortedTargets(grouped)
	result := make([]graphtarget.ID, 0, len(nativeTargets))
	for _, target := range nativeTargets {
		if !target.Runnable() {
			continue
		}
		result = append(result, graphtarget.ID{
			ID:        target.ID,
			Namespace: graphtarget.Namespace(Namespace),
			Kind:      target.Kind,
			Path:      target.File.String(),
			Name:      target.DisplayName(),
			Label:     target.Label,
			Index:     target.Index,
			Aliases:   target.Aliases,
		})
	}
	return result, nil
}

func (d Detector) ExecuteTarget(ctx context.Context, repo *attegit.Repo, root string, target graphtarget.ID) (graphtarget.Execution, error) {
	grouped, err := Targets(ctx, repo, d.scanner)
	if err != nil {
		return graphtarget.Execution{}, err
	}
	for _, native := range SortedTargets(grouped) {
		if native.ID != target.ID {
			continue
		}
		command, err := native.Execution(root)
		if err != nil {
			return graphtarget.Execution{}, err
		}
		return graphtarget.Execution{Dir: command.Dir, Args: command.Args}, nil
	}
	return graphtarget.Execution{}, fmt.Errorf("HCL target %q was not discovered", target.ID)
}
