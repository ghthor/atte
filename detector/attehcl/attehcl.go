// Package attehcl detects test, codegen, and lint definitions in atte.hcl files.
package attehcl

import (
	"github.com/ghthor/atte/detector/attehcltarget"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
)

const (
	Filename  = "atte.hcl"
	Namespace = "attehcl"

	TestKind                              = Namespace + ":test"
	CodegenKind                           = Namespace + ":codegen"
	LintKind                              = Namespace + ":lint"
	SourceFileRelation graph.RelationKind = attehcltarget.SourceFileRelation
	ScriptRelation     graph.RelationKind = "script"
	DependsOnRelation  graph.RelationKind = "depends-on"

	DecodingPathPrefix = "attehcl-path:"
)

type dependencyKind uint8

const (
	dependencyPath dependencyKind = iota
	dependencyEntity
	dependencyTarget
)

type dependency struct {
	kind      dependencyKind
	path      string
	entity    graph.EntityID
	target    targetReference
	traversal hcl.Traversal
}

type targetReference struct {
	file reference.Blob
	kind attehcltarget.Kind
	name string
}

type declarationIndex struct {
	byReference map[targetReference]targetDeclaration
	byID        map[graph.EntityID]targetDeclaration
}
