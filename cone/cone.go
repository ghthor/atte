package cone

import (
	"context"
	"fmt"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/contenthash"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
)

// Snapshot is a repository revision as the entity graph and content hashes
// see it.
type Snapshot struct {
	// Graph is the complete graph: the Git tree merged with every graph the
	// detectors contribute, attached to the tree.
	Graph *graph.Graph

	// Hashes is nil unless Capture was given WithContentHashes.
	Hashes *contenthash.Calculator
}

// CaptureOption configures Capture.
type CaptureOption func(*captureConfig)

type captureConfig struct {
	contentHashes bool
}

// WithContentHashes makes Capture compute the content hashes of the graph.
//
// Content tokens are strict: an external import that matches no require
// directive has a graph but no hashes, so Capture fails with this option where
// it succeeds without it.
func WithContentHashes() CaptureOption {
	return func(config *captureConfig) { config.contentHashes = true }
}

// Capture builds the complete graph of repo using scanner.
func Capture(ctx context.Context, scanner detector.Scanner, repo *attegit.Repo, options ...CaptureOption) (*Snapshot, error) {
	var config captureConfig
	for _, option := range options {
		option(&config)
	}
	gitGraph, err := repo.Graph()
	if err != nil {
		return nil, fmt.Errorf("build Git graph: %w", err)
	}
	detectorGraph, err := scanner.Graph(ctx, repo, graphset.WithAttachToTree())
	if err != nil {
		return nil, fmt.Errorf("build detector graph: %w", err)
	}
	if detectorGraph != nil {
		if err := gitGraph.Absorb(detectorGraph); err != nil {
			return nil, fmt.Errorf("merge detector graph: %w", err)
		}
	}
	snapshot := &Snapshot{Graph: gitGraph}
	if config.contentHashes {
		snapshot.Hashes, err = scanner.ContentHashes(ctx, repo, gitGraph)
		if err != nil {
			return nil, fmt.Errorf("compute content hashes: %w", err)
		}
	}
	return snapshot, nil
}
