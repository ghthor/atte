package attehcl

import (
	"github.com/ghthor/atte/detector/attehcl/hclblock"
	"github.com/hashicorp/hcl/v2"
)

func settingBlocks(scanner hclblock.SettingBlockCapabilities) map[string]hclblock.SettingBlock {
	if scanner == nil {
		return map[string]hclblock.SettingBlock{}
	}
	provided := scanner.SettingBlocks()
	result := make(map[string]hclblock.SettingBlock, len(provided))
	for name, block := range provided {
		if block.Schema() == nil {
			block = block.WithSchema(&hcl.BodySchema{})
		}
		result[name] = block
	}
	return result
}
