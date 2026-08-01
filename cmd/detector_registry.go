package cmd

import (
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/ghthor/atte/registry"
)

func builtInDetectorRegistry() (*registry.Registry, error) {
	r := registry.New()
	if err := r.Register(registry.Detector{
		Namespace: attego.Namespace,
		Graph:     attego.GraphWithContainment,
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
	}); err != nil {
		return nil, err
	}
	if err := r.Register(registry.Detector{
		Namespace: attehcl.Namespace,
		Graph:     attehcl.GraphWithContainment,
		Targets: func(repo *attegit.Repo) ([]registry.Target, error) {
			found, err := attehcl.Targets(repo)
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
	}); err != nil {
		return nil, err
	}
	return r, nil
}
