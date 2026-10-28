package hclblock

import (
	"fmt"

	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
)

// SettingDecoder decodes a schema-validated setting block body into a
// block-owned value.
type SettingDecoder func(*hcl.BodyContent, *hcl.EvalContext) (any, error)

// SettingProjection adds decoded configuration data to config show output.
type SettingProjection func(any) (map[string]any, error)

// SettingBlock describes an HCL setting block schema and its decoder.
// Schema and Decoder are required; Config is optional.
type SettingBlock struct {
	// Schema describes the setting block body.
	Schema *hcl.BodySchema
	// Decoder converts the schema-validated block body into a value.
	Decoder SettingDecoder
	// Config projects the decoded value into config show output.
	Config SettingProjection
}

// SettingBlockCapabilities exposes setting-block specifications to HCL
// evaluation.
type SettingBlockCapabilities interface {
	SettingBlocks() map[string]SettingBlock
}

// SettingValue describes one setting block declared in an atte.hcl file.
// The optional projection is bound from SettingBlock.
type SettingValue struct {
	Name    string
	File    reference.Blob
	Decoded any
	Source  hcl.Range

	config SettingProjection
}

// SettingBind returns value with the optional projection from block attached.
func SettingBind(value SettingValue, block SettingBlock) SettingValue {
	value.config = block.Config
	return value
}

// Configuration returns the attached block's optional configuration projection.
// Blocks without a projection produce nil.
func (value SettingValue) Configuration() (map[string]any, error) {
	if value.config == nil {
		return nil, nil
	}
	projected, err := value.config(value.Decoded)
	if err != nil {
		return nil, fmt.Errorf("project configuration for %s block %q: %w", value.Name, value.File, err)
	}
	return projected, nil
}
