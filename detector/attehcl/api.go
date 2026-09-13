package attehcl

import (
	"context"
	"fmt"
	"slices"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
)

// Targets evaluates all atte.hcl files independently and groups declarations by kind.
func Targets(ctx context.Context, repo *attegit.Repo, provider graphset.FunctionProvider) (map[Kind][]Target, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	return targetsWithProvider(ctx, repo, provider)
}

// DeclaredTargets returns target identities without evaluating target bodies.
// Results are ordered by repository-relative file path and source order.
func DeclaredTargets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	evaluator, err := newEvaluator(ctx, repo, nil)
	if err != nil {
		return nil, err
	}
	declarations, err := evaluator.declaredTargets()
	if err != nil {
		return nil, err
	}
	targets := make([]graphtarget.ID, 0, len(declarations))
	for i := range declarations {
		targets = append(targets, targetIDFromDeclaration(declarations[i]))
	}
	return targets, nil
}

// SortedTargets returns the targets grouped by kind in deterministic source order.
func SortedTargets(grouped map[Kind][]Target) []Target {
	kinds := make([]Kind, 0, len(grouped))
	for kind := range grouped {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	targets := make([]Target, 0)
	for _, kind := range kinds {
		targets = append(targets, grouped[kind]...)
	}
	return targets
}

func targetsWithProvider(ctx context.Context, repo *attegit.Repo, provider graphset.FunctionProvider) (map[Kind][]Target, error) {
	evaluator, err := newEvaluator(ctx, repo, provider)
	if err != nil {
		return nil, err
	}
	blocks, err := evaluator.evaluatedTargets()
	if err != nil {
		return nil, err
	}
	return targetsFromEvaluated(repo, blocks)
}

// Config is the evaluated target configuration for a repository directory.
type Config struct {
	Targets map[Kind][]Target
}

// ConfigFor evaluates the file-local target configuration for a repository-relative directory.
func ConfigFor(ctx context.Context, repo *attegit.Repo, relativePath string, provider graphset.FunctionProvider) (Config, error) {
	if err := checkContext(ctx); err != nil {
		return Config{}, err
	}
	return configForWithProvider(ctx, repo, relativePath, provider)
}

func configForWithProvider(ctx context.Context, repo *attegit.Repo, relativePath string, provider graphset.FunctionProvider) (Config, error) {
	if repo == nil {
		return Config{}, fmt.Errorf("repository is nil")
	}
	tree, err := reference.ParseTree(relativePath)
	if err != nil {
		return Config{}, fmt.Errorf("invalid repository directory %q: %w", relativePath, err)
	}
	currentBlob, err := tree.Blob(Filename)
	if err != nil {
		return Config{}, err
	}
	evaluator, err := newEvaluatorForFile(ctx, repo, currentBlob, provider)
	if err != nil {
		return Config{}, err
	}
	blocks, err := evaluator.evaluatedTargets(currentBlob)
	if err != nil {
		return Config{}, err
	}
	allTargets, err := targetsFromEvaluated(repo, blocks)
	if err != nil {
		return Config{}, err
	}
	targets := make(map[Kind][]Target, len(allTargets))
	for kind, kindTargets := range allTargets {
		for _, target := range kindTargets {
			if target.File.Tree() == tree {
				targets[kind] = append(targets[kind], target)
			}
		}
	}
	return Config{Targets: targets}, nil
}

// WithFunctions adds provider-supplied HCL functions.
func WithFunctions(provider graphset.FunctionProvider) graphset.Option {
	return func(options *graphset.Options) { options.Functions = provider }
}
