package attehcl

import (
	"context"
	"fmt"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
)

func graphScriptTarget(ctx context.Context, repo *attegit.Repo, target Target, graphContext TargetGraphContext, attachToTree bool) (TargetGraph, error) {
	if err := ctx.Err(); err != nil {
		return TargetGraph{}, err
	}
	decoded, ok := target.Decoded.(decodedTarget)
	if !ok {
		return TargetGraph{}, fmt.Errorf("target %q has an invalid built-in decoded value", target.ID)
	}
	if decoded.Script == "" {
		return TargetGraph{}, fmt.Errorf("target %q has no script", target.Name)
	}
	result := target.GraphProjectionBase(attachToTree)
	script := strings.TrimPrefix(decoded.Script, DecodingPathPrefix)
	if script != "" {
		targetBlob, err := reference.ResolveBlobFromBlob(target.File, reference.SomePath(script))
		if err != nil {
			return TargetGraph{}, fmt.Errorf("%q: %w", target.File, err)
		}
		if obj, ok := repo.Obj[targetBlob]; !ok || obj.Kind != attegit.Blob {
			return TargetGraph{}, fmt.Errorf("%q: script %q not found", target.File, targetBlob)
		}
		result.Entities = append(result.Entities, graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
		result.Relationships = append(result.Relationships, graph.Relationship{From: target.ID, To: attegit.EntityID(targetBlob), Kind: ScriptRelation})
	}
	for _, dep := range decoded.Deps {
		if err := ctx.Err(); err != nil {
			return TargetGraph{}, err
		}
		if dep.traversal != nil {
			entity, err := graphContext.ResolveTarget(dep.traversal)
			if err != nil {
				return TargetGraph{}, err
			}
			result.Entities = append(result.Entities, entity)
			result.Relationships = append(result.Relationships, graph.Relationship{From: target.ID, To: entity.ID, Kind: DependsOnRelation})
			continue
		}
		if dep.entity != "" {
			kind, err := graphContext.EntityKind(dep.entity)
			if err != nil {
				return TargetGraph{}, err
			}
			result.Entities = append(result.Entities, graph.Entity{ID: dep.entity, Kind: kind})
			result.Relationships = append(result.Relationships, graph.Relationship{From: target.ID, To: dep.entity, Kind: DependsOnRelation})
			continue
		}
		value := strings.TrimPrefix(dep.value, DecodingPathPrefix)
		targetBlob, err := reference.ResolveBlobFromBlob(target.File, reference.SomePath(value))
		if err != nil {
			return TargetGraph{}, fmt.Errorf("%q: %w", target.File, err)
		}
		obj, ok := repo.Obj[targetBlob]
		if !ok || obj.Kind != attegit.Blob {
			return TargetGraph{}, fmt.Errorf("%q: dependency %q not found", target.File, targetBlob)
		}
		result.Entities = append(result.Entities, graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
		result.Relationships = append(result.Relationships, graph.Relationship{From: target.ID, To: attegit.EntityID(targetBlob), Kind: DependsOnRelation})
	}
	return result, nil
}

// Graph builds the HCL detector graph using the supplied options.
func Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
	if err := checkContext(ctx); err != nil {
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	evaluator, err := newEvaluator(ctx, repo, options.Functions)
	if err != nil {
		return nil, err
	}
	blocks, err := evaluator.evaluatedTargets()
	if err != nil {
		return nil, err
	}
	entities := make([]graph.Entity, 0)
	seenEntities := make(map[graph.EntityID]struct{})
	relations := make([]graph.Relationship, 0)
	declarations := declarationIndex{
		byReference: make(map[targetReference]targetDeclaration, len(blocks)),
		byID:        make(map[graph.EntityID]targetDeclaration, len(blocks)),
	}
	for i := range blocks {
		declaration := declarationFromEvaluated(blocks[i])
		declarations.byID[declaration.ID] = declaration
		if declaration.Name != "" {
			declarations.byReference[targetReference{file: declaration.File, kind: declaration.Kind, name: declaration.Name}] = declaration
		}
	}
	addEntity := func(e graph.Entity) {
		if _, exists := seenEntities[e.ID]; exists {
			return
		}
		seenEntities[e.ID] = struct{}{}
		entities = append(entities, e)
	}
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := addEvaluatedTargetGraph(ctx, repo, block, declarations, addEntity, options.AttachToTree)
		if err != nil {
			return nil, err
		}
		relations = append(relations, r...)
	}
	return graph.New(entities, relations)
}

// addEvaluatedTargetGraph builds graph entities and relations for one decoded block.
func addEvaluatedTargetGraph(
	ctx context.Context,
	repo *attegit.Repo,
	target evaluatedTarget,
	declarations declarationIndex,
	addEntity func(graph.Entity),
	containment bool,
) ([]graph.Relationship, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	native := targetFromEvaluated(target)
	if native.graph == nil {
		return nil, nil
	}
	graphContext := TargetGraphContext{
		ResolveTarget: func(traversal hcl.Traversal) (graph.Entity, error) {
			declaration, err := resolveTargetTraversal(native.File, traversal, declarations)
			if err != nil {
				return graph.Entity{}, err
			}
			return graph.Entity{ID: declaration.ID, Kind: Namespace + ":" + string(declaration.Kind)}, nil
		},
		EntityKind: entityDependencyKind,
	}
	projected, err := native.graph(ctx, repo, native, graphContext, containment)
	if err != nil {
		return nil, fmt.Errorf("project graph for target %q: %w", native.ID, err)
	}
	for _, entity := range projected.Entities {
		addEntity(entity)
	}
	return projected.Relationships, nil
}

func resolveTargetTraversal(file reference.Blob, traversal hcl.Traversal, declarations declarationIndex) (targetDeclaration, error) {
	if len(traversal) != 2 {
		return targetDeclaration{}, fmt.Errorf("%q: target dependency must be a kind.name traversal", file)
	}
	attribute, ok := traversal[1].(hcl.TraverseAttr)
	if !ok {
		return targetDeclaration{}, fmt.Errorf("%q: target dependency must be a kind.name traversal", file)
	}
	kind := Kind(traversal.RootName())
	declaration, ok := declarations.byReference[targetReference{file: file, kind: kind, name: attribute.Name}]
	if !ok {
		return targetDeclaration{}, fmt.Errorf("%q: target dependency %s.%s not found in the same file", file, kind, attribute.Name)
	}
	return declaration, nil
}

func entityDependencyKind(id graph.EntityID) (string, error) {
	value := string(id)
	if strings.HasPrefix(value, "attego:") {
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
