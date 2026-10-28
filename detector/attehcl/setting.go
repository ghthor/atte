package attehcl

import (
	"fmt"
	"reflect"

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
		schema := hcl.BodySchema{}
		if block.Schema != nil {
			schema = copyBodySchema(*block.Schema)
		}
		block.Schema = &schema
		result[name] = block
	}
	return result
}

func validateSettingDecoderResult(name string, decoded any) error {
	if decoded == nil {
		return fmt.Errorf("setting block %q decoder returned nil", name)
	}
	value := reflect.ValueOf(decoded)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return fmt.Errorf("setting block %q decoder returned a nil pointer", name)
	}
	return nil
}

func settingsFromEvaluated(settings []evaluatedSetting) []hclblock.SettingValue {
	values := make([]hclblock.SettingValue, 0, len(settings))
	for _, item := range settings {
		values = append(values, hclblock.SettingBind(hclblock.SettingValue{
			Name:    item.Name,
			File:    item.File,
			Decoded: item.Decoded,
			Source:  item.Source,
		}, item.Block))
	}
	return values
}
