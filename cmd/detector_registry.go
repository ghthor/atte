package cmd

import (
	"strconv"
	"strings"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/graph"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/ghthor/atte/registry"
	"github.com/zclconf/go-cty/cty/function"
)

func builtInDetectorRegistry() (*registry.Registry, error) {
	r := registry.New()
	if err := r.Register(registry.Detector{
		Namespace: attego.Namespace,
		Graph: func(repo *attegit.Repo, options ...detector.GraphOption) (*graph.Graph, error) {
			return attego.Graph(repo, options...)
		},
		Targets: func(repo *attegit.Repo) ([]registry.Target, error) {
			found, err := attego.Targets(repo)
			if err != nil {
				return nil, err
			}
			targets := make([]registry.Target, 0, len(found))
			for _, target := range found {
				targets = append(targets, registry.Target{ID: target.ID, Namespace: attego.Namespace, Kind: target.Kind, Path: target.PackageDir.String(), Name: "go_test"})
			}
			return targets, nil
		},
		Selectorize: func(target registry.Target) (selector.Target, bool) {
			return attego.Selector(attego.Target{PackageDir: reference.Tree(target.Path)}), true
		},
		MatchIdentifier: func(target registry.Target, identifier string) bool {
			return identifier == "go_test"
		},
	}); err != nil {
		return nil, err
	}
	if err := r.Register(registry.Detector{
		Namespace: attehcl.Namespace,
		Graph: func(repo *attegit.Repo, options ...detector.GraphOption) (*graph.Graph, error) {
			graphOptions := make([]detector.GraphOption, 0, len(options)+2)
			graphOptions = append(graphOptions, detector.WithAttachToTree(), attehcl.WithFunctions(func(repo *attegit.Repo, file reference.Blob) (map[string]function.Function, error) {
				return r.HCLFunctions(repo, file)
			}))
			for _, option := range options {
				if option != nil {
					graphOptions = append(graphOptions, option)
				}
			}
			return attehcl.Graph(repo, graphOptions...)
		},
		Targets: func(repo *attegit.Repo) ([]registry.Target, error) {
			found, err := attehcl.TargetsWithFunctions(repo, attegit.PathHCLFunctions)
			if err != nil {
				return nil, err
			}
			targets := make([]registry.Target, 0, len(found))
			for _, target := range found {
				if target.Script == "" {
					continue
				}
				targets = append(targets, registry.Target{ID: target.ID, Namespace: attehcl.Namespace, Kind: target.Kind, Path: target.File.String(), Name: target.Name, Index: target.Index})
			}
			return targets, nil
		},
		Selectorize: func(target registry.Target) (selector.Target, bool) {
			return attehcl.Selector(attehcl.Target{Kind: target.Kind, File: reference.Blob(target.Path), Name: target.Name, Index: target.Index}), true
		},
		MatchIdentifier: func(target registry.Target, identifier string) bool {
			kind := strings.TrimPrefix(target.Kind, attehcl.Namespace+":")
			return identifier == kind || identifier == kind+"."+target.Name || identifier == kind+"."+strconv.Itoa(target.Index)
		},
	}); err != nil {
		return nil, err
	}

	if err := r.RegisterHCLFunction("path", attegit.PathHCLFunction); err != nil {
		return nil, err
	}

	return r, nil
}
