package hclblock

import (
	"fmt"

	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
)

// SettingBlock describes an HCL setting block schema and its decoder. Build
// one with NewSettingBlock; the zero value has no schema or decoder and is
// rejected on attachment.
type SettingBlock struct {
	schema  *hcl.BodySchema
	decode  func(*hcl.BodyContent, *hcl.EvalContext) (any, error)
	project func(any) (map[string]any, error)
}

// NewSettingBlock returns a SettingBlock whose decoder produces a T. The
// optional project function renders the decoded value in config show output.
// The schema is copied. A nil decode leaves the block without a decoder, which
// attachment rejects.
func NewSettingBlock[T any](
	schema *hcl.BodySchema,
	decode func(*hcl.BodyContent, *hcl.EvalContext) (T, error),
	project func(T) (map[string]any, error),
) SettingBlock {
	block := SettingBlock{schema: copyBodySchema(schema)}
	if decode != nil {
		block.decode = func(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
			return decode(content, ctx)
		}
	}
	if project != nil {
		block.project = func(decoded any) (map[string]any, error) {
			return project(decoded.(T))
		}
	}
	return block
}

// Schema returns a copy of the block body schema, or nil when none was set.
func (block SettingBlock) Schema() *hcl.BodySchema {
	return copyBodySchema(block.schema)
}

// WithSchema returns block with a copy of schema as its body schema.
func (block SettingBlock) WithSchema(schema *hcl.BodySchema) SettingBlock {
	block.schema = copyBodySchema(schema)
	return block
}

// HasDecoder reports whether block can decode a body.
func (block SettingBlock) HasDecoder() bool {
	return block.decode != nil
}

// Decode decodes content into a SettingValue named name with the optional
// projection attached. The caller supplies the value's File and Source.
func (block SettingBlock) Decode(name string, content *hcl.BodyContent, ctx *hcl.EvalContext) (SettingValue, error) {
	if block.decode == nil {
		return SettingValue{}, fmt.Errorf("setting block %q has no decoder", name)
	}
	decoded, err := block.decode(content, ctx)
	if err != nil {
		return SettingValue{}, err
	}
	if err := checkDecoded(fmt.Sprintf("setting block %q", name), decoded); err != nil {
		return SettingValue{}, err
	}
	return SettingValue{Name: name, decoded: decoded, project: block.project}, nil
}

// SettingBlockCapabilities exposes setting-block specifications to HCL
// evaluation.
type SettingBlockCapabilities interface {
	SettingBlocks() map[string]SettingBlock
}

// SettingValue describes one setting block declared in an atte.hcl file. Read
// the block-owned decoded value with DecodedAs.
type SettingValue struct {
	Name   string
	File   reference.Blob
	Source hcl.Range

	decoded any
	project func(any) (map[string]any, error)
}

// DecodedAs returns the block-owned decoded value of value as a T. The error
// names the expected and actual types.
func DecodedAs[T any](value SettingValue) (T, error) {
	return decodedAs[T](fmt.Sprintf("%s block in %q", value.Name, value.File), value.decoded)
}

// Configuration returns the attached block's optional configuration projection.
// Blocks without a projection produce nil.
func (value SettingValue) Configuration() (map[string]any, error) {
	if value.project == nil {
		return nil, nil
	}
	projected, err := value.project(value.decoded)
	if err != nil {
		return nil, fmt.Errorf("project configuration for %s block %q: %w", value.Name, value.File, err)
	}
	return projected, nil
}
