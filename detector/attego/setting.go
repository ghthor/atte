package attego

import (
	"context"
	"fmt"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl/hclblock"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// GoTestSettingBlock is the atte.hcl setting block that overrides the
// command used to run go_test targets. A block in a package directory applies
// to that package; a block in a module directory applies to every package in
// the module that has no closer override.
const GoTestSettingBlock = "go_test"

// DefaultGoTestCommand runs a package's tests when no go_test block applies.
var DefaultGoTestCommand = []string{"go", "test", "-v"}

var goTestConfigSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "command", Required: true}},
}

// GoTestSetting is the decoded go_test setting block.
type GoTestSetting struct {
	Command []string
}

// HCLSettingBlocks provides the setting blocks contributed by the Go
// Sensor during attachment.
func (Detector) HCLSettingBlocks() map[string]hclblock.SettingBlock {
	return map[string]hclblock.SettingBlock{
		GoTestSettingBlock: {
			Schema:  &goTestConfigSchema,
			Decoder: decodeGoTestSetting,
			Config:  configGoTestSetting,
		},
	}
}

func decodeGoTestSetting(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
	value, diagnostics := content.Attributes["command"].Expr.Value(ctx)
	if diagnostics.HasErrors() {
		return nil, diagnostics
	}
	if !value.IsWhollyKnown() || value.IsNull() {
		return nil, fmt.Errorf("command must be a known list of strings")
	}
	if !value.Type().IsListType() && !value.Type().IsTupleType() {
		return nil, fmt.Errorf("command must be a list of strings, got %s", value.Type().FriendlyName())
	}
	command := make([]string, 0, value.LengthInt())
	for _, item := range value.AsValueSlice() {
		if item.IsNull() || item.Type() != cty.String {
			return nil, fmt.Errorf("command elements must be strings, got %s", item.Type().FriendlyName())
		}
		command = append(command, item.AsString())
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("command must not be empty")
	}
	return GoTestSetting{Command: command}, nil
}

func configGoTestSetting(decoded any) (map[string]any, error) {
	config, ok := decoded.(GoTestSetting)
	if !ok {
		return nil, fmt.Errorf("%s block has an invalid decoded value %T", GoTestSettingBlock, decoded)
	}
	return map[string]any{"command": config.Command}, nil
}

// goTestCommand resolves the command for target from the nearest go_test
// setting block: the package directory, then the module directory.
func (d Detector) goTestCommand(ctx context.Context, repo *attegit.Repo, target graphtarget.ID) ([]string, error) {
	if d.scanner == nil || repo == nil {
		return DefaultGoTestCommand, nil
	}
	packageDir, err := reference.ParseTree(target.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid package directory %q: %w", target.Path, err)
	}
	_, moduleDir, _, err := DecodeEntityID(target.ID)
	if err != nil {
		return nil, err
	}
	trees := []reference.Tree{packageDir}
	if moduleDir != packageDir {
		trees = append(trees, moduleDir)
	}
	for _, tree := range trees {
		values, err := d.scanner.HCLSettings(ctx, repo, tree)
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			if value.Name != GoTestSettingBlock {
				continue
			}
			config, ok := value.Decoded.(GoTestSetting)
			if !ok {
				return nil, fmt.Errorf("%s block in %q has an invalid decoded value %T", GoTestSettingBlock, value.File, value.Decoded)
			}
			return config.Command, nil
		}
	}
	return DefaultGoTestCommand, nil
}
