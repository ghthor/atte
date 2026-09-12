package attehcl

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
	"github.com/ghthor/atte/reference/selector"
	"github.com/hashicorp/hcl/v2"
)

func targetFromEvaluated(repo *attegit.Repo, item evaluatedTarget) (Target, error) {
	script, inline, err := targetScript(repo, item.File, item.Decoded)
	if err != nil {
		return Target{}, err
	}
	kind := string(item.Kind)
	name := displayName(item.Name, item.Index)
	return Target{
		ID:      item.ID,
		Kind:    Namespace + ":" + kind,
		File:    item.File,
		Name:    item.Name,
		Label:   item.Name,
		Index:   item.Index,
		Aliases: selector.Aliases(selector.Target{Path: item.File.String(), Kind: kind, Name: name, Index: item.Index}),
		Script:  script,
		Inline:  inline,
		Decoded: item.Decoded,
		Source:  item.Source,
	}, nil
}

func targetsFromEvaluated(repo *attegit.Repo, evaluated []evaluatedTarget) (map[Kind][]Target, error) {
	targets := make(map[Kind][]Target, len(evaluated))
	for _, item := range evaluated {
		target, err := targetFromEvaluated(repo, item)
		if err != nil {
			return nil, err
		}
		targets[item.Kind] = append(targets[item.Kind], target)
	}
	return targets, nil
}

func targetScript(repo *attegit.Repo, file reference.Blob, decoded any) (reference.Blob, string, error) {
	target, ok := decoded.(scriptTarget)
	if !ok {
		return "", "", nil
	}
	if strings.HasPrefix(target.Script, DecodingPathPrefix) {
		return reference.Blob(strings.TrimPrefix(target.Script, DecodingPathPrefix)), "", nil
	}
	if target.Script == "" {
		return "", "", nil
	}
	trimmed := strings.TrimSpace(target.Script)
	if object, ok := repo.Obj[reference.Blob(trimmed)]; ok {
		return "", "", fmt.Errorf("decode HCL %q: script string resolves to repository %q %q; use path(%q) for an external script", file, object.Kind, trimmed, trimmed)
	}
	return "", target.Script, nil
}

// Targets evaluates all HCL files independently and groups targets by kind.
func Targets(ctx context.Context, repo *attegit.Repo, provider graphset.FunctionProvider) (map[Kind][]Target, error) {
	evaluator, err := newEvaluator(ctx, repo, provider)
	if err != nil {
		return nil, err
	}
	evaluated, err := evaluator.evaluatedTargets()
	if err != nil {
		return nil, err
	}
	return targetsFromEvaluated(repo, evaluated)
}

// SortedTargets flattens grouped targets in deterministic kind order.
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

// ConfigFor evaluates only the atte.hcl file at relativePath.
func ConfigFor(ctx context.Context, repo *attegit.Repo, relativePath string, provider graphset.FunctionProvider) (Config, error) {
	if repo == nil {
		return Config{}, fmt.Errorf("repository is nil")
	}
	tree, err := reference.ParseTree(relativePath)
	if err != nil {
		return Config{}, fmt.Errorf("invalid repository directory %q: %w", relativePath, err)
	}
	file, err := tree.Blob(Filename)
	if err != nil {
		return Config{}, err
	}
	evaluator, err := newEvaluatorForFile(ctx, repo, file, provider)
	if err != nil {
		return Config{}, err
	}
	evaluated, err := evaluator.evaluatedTargets(file)
	if err != nil {
		return Config{}, err
	}
	targets, err := targetsFromEvaluated(repo, evaluated)
	if err != nil {
		return Config{}, err
	}
	return Config{Targets: targets}, nil
}

// WithFunctions supplies provider functions to Graph and Detector.
func WithFunctions(provider graphset.FunctionProvider) graphset.Option {
	return func(options *graphset.Options) { options.Functions = provider }
}

// Graph builds the detector graph from decoded targets and the declaration index.
func Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config := graphset.Options{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return graphFor(ctx, repo, config)
}

func graphFor(ctx context.Context, repo *attegit.Repo, options graphset.Options) (*graph.Graph, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	evaluator, err := newEvaluator(ctx, repo, options.Functions)
	if err != nil {
		return nil, err
	}
	evaluated, err := evaluator.evaluatedTargets()
	if err != nil {
		return nil, err
	}
	declarations := make(map[graph.EntityID]declaration, len(evaluated))
	named := make(map[targetReference]graph.EntityID, len(evaluated))
	for _, item := range evaluated {
		declarations[item.ID] = item.declaration
		if item.Name != "" {
			named[targetReference{file: item.File, kind: item.Kind, name: item.Name}] = item.ID
		}
	}

	entities := make([]graph.Entity, 0, len(evaluated))
	seenEntities := make(map[graph.EntityID]struct{})
	addEntity := func(entity graph.Entity) {
		if _, exists := seenEntities[entity.ID]; exists {
			return
		}
		seenEntities[entity.ID] = struct{}{}
		entities = append(entities, entity)
	}
	relations := make([]graph.Relationship, 0)
	for _, item := range evaluated {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		added, err := addEvaluatedTargetGraph(ctx, repo, item, named, declarations, addEntity, options.AttachToTree)
		if err != nil {
			return nil, err
		}
		relations = append(relations, added...)
	}
	return graph.New(entities, relations)
}

type targetReference struct {
	file reference.Blob
	kind Kind
	name string
}

func addEvaluatedTargetGraph(ctx context.Context, repo *attegit.Repo, target evaluatedTarget, named map[targetReference]graph.EntityID, declarations map[graph.EntityID]declaration, addEntity func(graph.Entity), containment bool) ([]graph.Relationship, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := target.ID
	addEntity(graph.Entity{ID: id, Kind: Namespace + ":" + string(target.Kind)})
	relations := make([]graph.Relationship, 0)
	if containment {
		fileID := attegit.EntityID(target.File)
		treeID := attegit.EntityID(target.File.Tree())
		addEntity(graph.Entity{ID: fileID, Kind: attegit.BlobKind})
		addEntity(graph.Entity{ID: treeID, Kind: attegit.TreeKind})
		relations = append(relations,
			graph.Relationship{From: id, To: fileID, Kind: SourceFileRelation},
			graph.Relationship{From: treeID, To: id, Kind: attegit.ContainsRelation},
		)
	}
	decoded, scriptBacked := target.Decoded.(scriptTarget)
	if !scriptBacked {
		return relations, nil
	}
	if decoded.Script == "" {
		return nil, fmt.Errorf("target %q has no script", target.Name)
	}
	if strings.HasPrefix(decoded.Script, DecodingPathPrefix) {
		script := strings.TrimPrefix(decoded.Script, DecodingPathPrefix)
		targetBlob, err := reference.ParseBlob(script)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", target.File, err)
		}
		if object, ok := repo.Obj[targetBlob]; !ok || object.Kind != attegit.Blob {
			return nil, fmt.Errorf("%q: script %q not found", target.File, targetBlob)
		}
		addEntity(graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
		relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(targetBlob), Kind: ScriptRelation})
	}
	for _, dependency := range decoded.Deps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if dependency.traversal != nil {
			dependencyID, err := resolveTargetTraversal(target.File, dependency.traversal, named)
			if err != nil {
				return nil, err
			}
			addEntity(graph.Entity{ID: dependencyID, Kind: Namespace + ":" + string(namedKind(dependencyID))})
			relations = append(relations, graph.Relationship{From: id, To: dependencyID, Kind: DependsOnRelation})
			continue
		}
		if dependency.entity != "" {
			kind, err := entityDependencyKind(dependency.entity)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(string(dependency.entity), Namespace+":") {
				if _, ok := declarations[dependency.entity]; !ok {
					return nil, fmt.Errorf("dependency target %q not declared", dependency.entity)
				}
			}
			addEntity(graph.Entity{ID: dependency.entity, Kind: kind})
			relations = append(relations, graph.Relationship{From: id, To: dependency.entity, Kind: DependsOnRelation})
			continue
		}
		value := dependency.value
		var targetBlob reference.Blob
		if strings.HasPrefix(value, DecodingPathPrefix) {
			var err error
			targetBlob, err = reference.ParseBlob(strings.TrimPrefix(value, DecodingPathPrefix))
			if err != nil {
				return nil, fmt.Errorf("%q: %w", target.File, err)
			}
		} else {
			var err error
			targetBlob, err = reference.ResolveBlobFromBlob(target.File, reference.SomePath(value))
			if err != nil {
				return nil, fmt.Errorf("%q: %w", target.File, err)
			}
		}
		object, ok := repo.Obj[targetBlob]
		if !ok || object.Kind != attegit.Blob {
			return nil, fmt.Errorf("%q: dependency %q not found", target.File, targetBlob)
		}
		addEntity(graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
		relations = append(relations, graph.Relationship{From: id, To: attegit.EntityID(targetBlob), Kind: DependsOnRelation})
	}
	return relations, nil
}

func resolveTargetTraversal(file reference.Blob, traversal hcl.Traversal, named map[targetReference]graph.EntityID) (graph.EntityID, error) {
	if len(traversal) != 2 {
		return "", fmt.Errorf("target dependency traversal must be kind.name")
	}
	root, ok := traversal[0].(hcl.TraverseRoot)
	if !ok {
		return "", fmt.Errorf("target dependency traversal must start with a target kind")
	}
	attribute, ok := traversal[1].(hcl.TraverseAttr)
	if !ok {
		return "", fmt.Errorf("target dependency traversal must name a target")
	}
	id, ok := named[targetReference{file: file, kind: Kind(root.Name), name: attribute.Name}]
	if !ok {
		return "", fmt.Errorf("target dependency %s.%s is not declared in %q", root.Name, attribute.Name, file)
	}
	return id, nil
}

func namedKind(id graph.EntityID) Kind {
	_, _, _, err := DecodeEntityID(id)
	if err != nil {
		return ""
	}
	parts := strings.SplitN(string(id), ":", 4)
	return Kind(parts[1])
}

func entityDependencyKind(id graph.EntityID) (string, error) {
	value := string(id)
	if strings.HasPrefix(value, attego.Namespace+":") {
		kind, _, _, err := attego.DecodeEntityID(id)
		if err != nil {
			return "", fmt.Errorf("decode Go dependency entity %q: %w", id, err)
		}
		return kind, nil
	}
	if strings.HasPrefix(value, Namespace+":") {
		kind, _, _, err := DecodeEntityID(id)
		if err != nil {
			return "", fmt.Errorf("decode HCL dependency entity %q: %w", id, err)
		}
		return kind, nil
	}
	return "", fmt.Errorf("dependency entity %q has unknown namespace", id)
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

// Detector adapts the experiment to the shared detector capabilities.
type Detector struct{ Provider graphset.FunctionProvider }

func NewDetector(provider graphset.FunctionProvider) Detector { return Detector{Provider: provider} }
func (Detector) Namespace() string                            { return Namespace }

func (detector Detector) Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
	graphOptions := make([]graphset.Option, 0, len(options)+1)
	graphOptions = append(graphOptions, graphset.WithAttachToTree(), WithFunctions(detector.Provider))
	graphOptions = append(graphOptions, options...)
	return Graph(ctx, repo, graphOptions...)
}

func (detector Detector) Targets(ctx context.Context, repo *attegit.Repo) ([]graphtarget.ID, error) {
	declarations, err := DeclaredTargets(ctx, repo)
	if err != nil {
		return nil, err
	}
	result := make([]graphtarget.ID, 0, len(declarations))
	for _, declaration := range declarations {
		name := displayName(declaration.Name, declaration.Index)
		result = append(result, graphtarget.ID{
			ID:        declaration.ID,
			Namespace: Namespace,
			Kind:      Namespace + ":" + string(declaration.Kind),
			Path:      declaration.File.String(),
			Name:      name,
			Index:     declaration.Index,
			Aliases: selector.Aliases(
				selector.Target{
					Path: declaration.File.String(),
					Kind: string(declaration.Kind), Name: name, Index: declaration.Index,
				},
			),
		})
	}
	return result, nil
}
