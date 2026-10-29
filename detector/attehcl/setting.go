package attehcl

import (
	"github.com/ghthor/atte/detector/attehcl/hclblock"
	"github.com/hashicorp/hcl/v2"
)

// settingBlocks returns the setting blocks available to HCL evaluation: the
// built-in LayoutBlock plus those the scanner provides.
func settingBlocks(scanner hclblock.SettingBlockCapabilities) map[string]hclblock.SettingBlock {
	if scanner == nil {
		return map[string]hclblock.SettingBlock{LayoutBlock: layoutSettingBlock()}
	}
	provided := scanner.SettingBlocks()
	result := make(map[string]hclblock.SettingBlock, len(provided)+1)
	for name, block := range provided {
		if block.Schema() == nil {
			block = block.WithSchema(&hcl.BodySchema{})
		}
		result[name] = block
	}
	result[LayoutBlock] = layoutSettingBlock()
	return result
}
