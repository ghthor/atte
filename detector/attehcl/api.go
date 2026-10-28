package attehcl

import (
	"context"
	"fmt"
	"slices"

	"github.com/ghthor/atte/detector/attehcl/hclblock"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
)

type targetEvaluationCapabilities interface {
	hclblock.TargetKindCapabilities
	hclblock.SettingBlockCapabilities
	FunctionCapabilities
}

// Targets evaluates all atte.hcl files independently and groups declarations by kind.
func Targets(ctx context.Context, repo *attegit.Repo, scanner targetEvaluationCapabilities) (map[hclblock.TargetKind][]hclblock.Target, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	return targetsWithCapabilities(ctx, repo, scanner)
}

// DeclaredTargets returns target identities without evaluating target bodies.
// Results are ordered by repository-relative file path and source order.
func DeclaredTargets(ctx context.Context, repo *attegit.Repo, scanner targetEvaluationCapabilities) ([]graphtarget.ID, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	evaluator, err := newEvaluator(ctx, repo, scanner)
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
func SortedTargets(grouped map[hclblock.TargetKind][]hclblock.Target) []hclblock.Target {
	kinds := make([]hclblock.TargetKind, 0, len(grouped))
	for kind := range grouped {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	targets := make([]hclblock.Target, 0)
	for _, kind := range kinds {
		targets = append(targets, grouped[kind]...)
	}
	return targets
}

func targetsWithCapabilities(
	ctx context.Context,
	repo *attegit.Repo,
	scanner targetEvaluationCapabilities,
) (map[hclblock.TargetKind][]hclblock.Target, error) {
	evaluator, err := newEvaluator(ctx, repo, scanner)
	if err != nil {
		return nil, err
	}
	blocks, err := evaluator.evaluatedTargets()
	if err != nil {
		return nil, err
	}
	return targetsFromEvaluated(repo, blocks)
}

// Config is the evaluated configuration for a repository directory. Targets
// groups the file's target declarations by kind; Settings lists the file's
// setting blocks in source order.
type Config struct {
	Targets  map[hclblock.TargetKind][]hclblock.Target
	Settings []hclblock.SettingValue
}

// ConfigFor evaluates the file-local target configuration for a repository-relative directory.
func ConfigFor(ctx context.Context, repo *attegit.Repo, relativePath string, scanner targetEvaluationCapabilities) (Config, error) {
	if err := checkContext(ctx); err != nil {
		return Config{}, err
	}
	return configForWithCapabilities(ctx, repo, relativePath, scanner)
}

func configForWithCapabilities(
	ctx context.Context,
	repo *attegit.Repo,
	relativePath string,
	scanner targetEvaluationCapabilities,
) (Config, error) {
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
	evaluator, err := newEvaluatorForFile(ctx, repo, currentBlob, scanner)
	if err != nil {
		return Config{}, err
	}
	evaluated, err := evaluator.evaluatedFile(currentBlob)
	if err != nil {
		return Config{}, err
	}
	allTargets, err := targetsFromEvaluated(repo, evaluated.targets)
	if err != nil {
		return Config{}, err
	}
	return Config{Targets: allTargets, Settings: settingsFromEvaluated(evaluated.settings)}, nil
}
