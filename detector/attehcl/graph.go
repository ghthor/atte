package attehcl

import (
	"context"
	"fmt"
	"strings"

	"github.com/ghthor/atte/detector/attehcltarget"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphset"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
)

func graphScriptTarget(
	ctx context.Context,
	repo *attegit.Repo,
	target attehcltarget.Target,
	graphContext attehcltarget.GraphContext,
	attachToTree bool,
) (attehcltarget.Graph, error) {
	if err := ctx.Err(); err != nil {
		return attehcltarget.Graph{}, err
	}
	decoded, ok := target.Decoded.(decodedTarget)
	if !ok {
		return attehcltarget.Graph{}, fmt.Errorf("target %q has an invalid built-in decoded value", target.ID)
	}
	if decoded.Script == "" {
		return attehcltarget.Graph{}, fmt.Errorf("target %q has no script", target.Name)
	}
	result := target.GraphProjectionBase(attachToTree)
	script, isPath := strings.CutPrefix(decoded.Script, DecodingPathPrefix)
	if isPath && script != "" {
		targetBlob, err := reference.ResolveBlobFromBlob(target.File, reference.SomePath(script))
		if err != nil {
			return attehcltarget.Graph{}, fmt.Errorf("%q: %w", target.File, err)
		}
		if obj, ok := repo.Obj[targetBlob]; !ok || obj.Kind != attegit.Blob {
			return attehcltarget.Graph{}, fmt.Errorf("%q: script %q not found", target.File, targetBlob)
		}
		result.Entities = append(result.Entities, graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind})
		result.Relationships = append(result.Relationships, graph.Relationship{From: target.ID, To: attegit.EntityID(targetBlob), Kind: ScriptRelation})
	}
	for _, dependency := range decoded.Deps {
		entity, relationship, err := projectDependency(ctx, repo, target, dependency, graphContext)
		if err != nil {
			return attehcltarget.Graph{}, err
		}
		result.Entities = append(result.Entities, entity)
		result.Relationships = append(result.Relationships, relationship)
	}
	return result, nil
}

// projectDependency resolves one decoded dependency to a graph entity and relation.
func projectDependency(
	ctx context.Context,
	repo *attegit.Repo,
	target attehcltarget.Target,
	dependency dependency,
	graphContext attehcltarget.GraphContext,
) (graph.Entity, graph.Relationship, error) {
	if err := ctx.Err(); err != nil {
		return graph.Entity{}, graph.Relationship{}, err
	}

	var entity graph.Entity
	switch dependency.kind {
	case dependencyTarget:
		var (
			resolved graph.Entity
			err      error
		)
		if dependency.target.file != "" {
			if graphContext.ResolveTargetAt == nil {
				return graph.Entity{}, graph.Relationship{}, fmt.Errorf("cross-file target dependencies are not supported by this graph projector")
			}
			resolved, err = graphContext.ResolveTargetAt(dependency.target.file, dependency.traversal)
		} else {
			resolved, err = graphContext.ResolveTarget(dependency.traversal)
		}
		if err != nil {
			return graph.Entity{}, graph.Relationship{}, err
		}
		entity = resolved
	case dependencyEntity:
		kind, err := graphContext.EntityKind(dependency.entity)
		if err != nil {
			return graph.Entity{}, graph.Relationship{}, err
		}
		entity = graph.Entity{ID: dependency.entity, Kind: kind}
	case dependencyPath:
		targetBlob, err := reference.ResolveBlobFromBlob(target.File, reference.SomePath(dependency.path))
		if err != nil {
			return graph.Entity{}, graph.Relationship{}, fmt.Errorf("%q: %w", target.File, err)
		}
		obj, ok := repo.Obj[targetBlob]
		if !ok || obj.Kind != attegit.Blob {
			return graph.Entity{}, graph.Relationship{}, fmt.Errorf("%q: dependency %q not found", target.File, targetBlob)
		}
		entity = graph.Entity{ID: attegit.EntityID(targetBlob), Kind: attegit.BlobKind}
	default:
		return graph.Entity{}, graph.Relationship{}, fmt.Errorf("dependency has unknown kind %d", dependency.kind)
	}
	return entity, graph.Relationship{From: target.ID, To: entity.ID, Kind: DependsOnRelation}, nil
}

// Graph builds the HCL Sensor graph using capabilities from the supplied Scanner.
func Graph(ctx context.Context, repo *attegit.Repo, scanner Capabilities, options ...graphset.Option) (*graph.Graph, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	config := graphset.Options{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}

	return graphFor(ctx, repo, scanner, config)
}

func graphFor(ctx context.Context, repo *attegit.Repo, scanner Capabilities, options graphset.Options) (*graph.Graph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, fmt.Errorf("repository is nil")
	}
	evaluator, err := newEvaluator(ctx, repo, scanner)
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
	declarations := evaluator.declarations
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
		r, err := addEvaluatedTargetGraph(ctx, repo, block, declarations, addEntity, options.AttachToTree, scanner)
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
	decodeID EntityCapabilities,
) ([]graph.Relationship, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	native := targetFromEvaluated(target)
	project := native.GraphProjection()
	if project == nil {
		return nil, nil
	}
	graphContext := attehcltarget.GraphContext{
		ResolveTarget: func(traversal hcl.Traversal) (graph.Entity, error) {
			declaration, err := resolveTargetTraversal(native.File, traversal, declarations)
			if err != nil {
				return graph.Entity{}, err
			}
			return graph.Entity{ID: declaration.ID, Kind: graph.EntityKind(Namespace + ":" + string(declaration.Kind))}, nil
		},
		ResolveTargetAt: func(file reference.Blob, traversal hcl.Traversal) (graph.Entity, error) {
			declaration, err := resolveTargetTraversal(file, traversal, declarations)
			if err != nil {
				return graph.Entity{}, err
			}
			return graph.Entity{ID: declaration.ID, Kind: graph.EntityKind(Namespace + ":" + string(declaration.Kind))}, nil
		},
		EntityKind: func(id graph.EntityID) (graph.EntityKind, error) {
			return entityDependencyKind(decodeID, id)
		},
	}
	projected, err := project(ctx, repo, native, graphContext, containment)
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
	kind := attehcltarget.Kind(traversal.RootName())
	declaration, ok := declarations.byReference[targetReference{file: file, kind: kind, name: attribute.Name}]
	if !ok {
		return targetDeclaration{}, fmt.Errorf("%q: target dependency %s.%s not found", file, kind, attribute.Name)
	}
	return declaration, nil
}

func entityDependencyKind(decodeID EntityCapabilities, id graph.EntityID) (graph.EntityKind, error) {
	if decodeID == nil {
		return "", fmt.Errorf("no entity decoder configured for dependency %q", id)
	}
	entity, err := decodeID.DecodeID(id)
	if err != nil {
		return "", fmt.Errorf("decode dependency entity %q: %w", id, err)
	}
	return entity.Kind, nil
}
