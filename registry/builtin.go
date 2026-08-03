package registry

import (
	"strings"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/graph"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
)

// NewBuiltIn returns a registry containing atte's built-in detectors and HCL functions.
func NewBuiltIn() (*Registry, error) {
	r := New()
	if err := r.Register(Detector{
		Namespace: attego.Namespace,
		Graph:     attego.Graph,
		Targets: func(repo *attegit.Repo) ([]Target, error) {
			found, err := attego.Targets(repo)
			if err != nil {
				return nil, err
			}
			targets := make([]Target, 0, len(found))
			for _, target := range found {
				aliases := selector.Aliases(attego.Selector(target))
				targets = append(targets, Target{
					ID:        target.ID,
					Namespace: attego.Namespace,
					Kind:      target.Kind,
					Path:      target.PackageDir.String(),
					Name:      "go_test",
					Aliases:   aliases,
				})
			}
			return targets, nil
		},
		Selectorize: func(target Target) (selector.Target, bool) {
			result := attego.Selector(attego.Target{PackageDir: reference.Tree(target.Path)})
			result.Aliases = target.Aliases
			return result, true
		},
		MatchIdentifier: func(target Target, identifier string) bool {
			return selector.Target{
				Kind:    "go_test",
				Aliases: target.Aliases,
			}.Matches("#"+identifier, "")
		},
	}); err != nil {
		return nil, err
	}
	if err := r.Register(Detector{
		Namespace: attehcl.Namespace,
		Graph: func(repo *attegit.Repo, options ...detector.GraphOption) (*graph.Graph, error) {
			graphOptions := make([]detector.GraphOption, 0, len(options)+2)
			graphOptions = append(graphOptions, detector.WithAttachToTree(), attehcl.WithFunctions(r.FunctionProvider()))
			for _, option := range options {
				if option != nil {
					graphOptions = append(graphOptions, option)
				}
			}
			return attehcl.Graph(repo, graphOptions...)
		},
		Targets: func(repo *attegit.Repo) ([]Target, error) {
			found, err := attehcl.Targets(repo, r.FunctionProvider())
			if err != nil {
				return nil, err
			}
			targets := make([]Target, 0, len(found))
			for _, target := range found {
				if target.Script == "" {
					continue
				}
				targets = append(targets, Target{
					ID:        target.ID,
					Namespace: attehcl.Namespace,
					Kind:      target.Kind,
					Path:      target.File.String(),
					Name:      target.Name,
					Index:     target.Index,
					Aliases:   target.Aliases,
				})
			}
			return targets, nil
		},
		Selectorize: func(target Target) (selector.Target, bool) {
			return attehcl.Selector(attehcl.Target{Kind: target.Kind, File: reference.Blob(target.Path), Name: target.Name, Index: target.Index, Aliases: target.Aliases}), true
		},
		MatchIdentifier: func(target Target, identifier string) bool {
			kind := strings.TrimPrefix(target.Kind, attehcl.Namespace+":")
			return selector.Target{
				Kind:    kind,
				Name:    target.Name,
				Index:   target.Index,
				Aliases: target.Aliases,
			}.Matches("#"+identifier, "")
		},
	}); err != nil {
		return nil, err
	}
	if err := r.RegisterHCLFunction("path", attegit.PathHCLFunction); err != nil {
		return nil, err
	}
	return r, nil
}
