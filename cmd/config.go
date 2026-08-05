package cmd

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/registry"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"
)

var (
	configRef         string
	configWorkingTree bool
	configFormat      string
)

var configCmd = &cobra.Command{Use: "config", Short: "Inspect evaluated configuration"}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show evaluated configuration for the current directory",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		root, relative, err := repositoryContext(ctx, cwd)
		if err != nil {
			return err
		}
		var options []attegit.OpenOption
		if configWorkingTree {
			options = append(options, attegit.WithWorkingTree())
		}
		repo, err := attegit.Open(root, configRef, options...)
		if err != nil {
			return err
		}
		builtIns, err := registry.NewBuiltIn()
		if err != nil {
			return fmt.Errorf("register detectors: %w", err)
		}
		config, err := attehcl.ConfigFor(cmd.Context(), repo, relative, builtIns.FunctionProvider())
		if err != nil {
			return fmt.Errorf("evaluate attehcl configuration: %w", err)
		}
		return writeConfig(cmd.OutOrStdout(), config, configFormat)
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configShowCmd)
	configShowCmd.Flags().StringVarP(&configRef, "ref", "r", "HEAD", "Git revision to inspect")
	configShowCmd.Flags().BoolVar(&configWorkingTree, "working-tree", false, "Include modified and non-ignored untracked files")
	configShowCmd.Flags().StringVar(&configFormat, "format", "json", "Output format (json or hcl)")
}

type configOutput struct {
	Global map[string]any          `json:"global"`
	Local  map[string]any          `json:"local"`
	Target map[string]configTarget `json:"target"`
}

type configTarget struct {
	Kind   string `json:"kind"`
	File   string `json:"file"`
	Name   string `json:"name"`
	Label  string `json:"label,omitempty"`
	Index  int    `json:"index"`
	Script string `json:"script,omitempty"`
	Inline string `json:"inline,omitempty"`
}

func writeConfig(w interface{ Write([]byte) (int, error) }, config attehcl.Config, format string) error {
	output, err := configOutputFor(config)
	if err != nil {
		return err
	}
	switch strings.ToLower(format) {
	case "json":
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	case "hcl":
		return writeConfigHCL(w, output)
	default:
		return fmt.Errorf("unsupported format %q; choose json or hcl", format)
	}
}

func configOutputFor(config attehcl.Config) (configOutput, error) {
	global, err := ctyMap(config.Global)
	if err != nil {
		return configOutput{}, err
	}
	local, err := ctyMap(config.Local)
	if err != nil {
		return configOutput{}, err
	}
	target := make(map[string]configTarget, len(config.Targets))
	for _, item := range config.Targets {
		key := fmt.Sprintf("//%s#%s.%s", item.File, strings.TrimPrefix(item.Kind, attehcl.Namespace+":"), item.Name)
		target[key] = configTarget{Kind: item.Kind, File: item.File.String(), Name: item.Name, Label: item.Label, Index: item.Index, Script: item.Script.String(), Inline: item.Inline}
	}
	return configOutput{Global: global, Local: local, Target: target}, nil
}

func ctyMap(values map[string]cty.Value) (map[string]any, error) {
	result := make(map[string]any, len(values))
	for key, value := range values {
		converted, err := ctyJSON(value)
		if err != nil {
			return nil, fmt.Errorf("convert %q: %w", key, err)
		}
		result[key] = converted
	}
	return result, nil
}

func ctyJSON(value cty.Value) (any, error) {
	if !value.IsKnown() || value.IsNull() {
		return nil, nil
	}
	switch {
	case value.Type() == cty.String:
		return value.AsString(), nil
	case value.Type() == cty.Bool:
		return value.True(), nil
	case value.Type() == cty.Number:
		n := value.AsBigFloat()
		if i, acc := n.Int64(); acc == big.Exact {
			return i, nil
		}
		return n.String(), nil
	case value.CanIterateElements():
		items := make([]any, 0)
		it := value.ElementIterator()
		for it.Next() {
			_, item := it.Element()
			converted, err := ctyJSON(item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	case value.Type() == attegit.RepositoryPathType:
		return (*value.EncapsulatedValue().(*reference.Blob)).String(), nil
	case value.Type().IsObjectType() || value.Type().IsMapType():
		result := make(map[string]any)
		it := value.ElementIterator()
		for it.Next() {
			key, item := it.Element()
			converted, err := ctyJSON(item)
			if err != nil {
				return nil, err
			}
			result[key.AsString()] = converted
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported cty type %s", value.Type().FriendlyName())
	}
}

func writeConfigHCL(w interface{ Write([]byte) (int, error) }, output configOutput) error {
	file := hclwrite.NewEmptyFile()
	body := file.Body()
	values := map[string]any{"global": output.Global, "local": output.Local, "target": output.Target}
	for _, name := range []string{"global", "local", "target"} {
		value, err := anyCty(values[name])
		if err != nil {
			return fmt.Errorf("encode %s: %w", name, err)
		}
		body.SetAttributeRaw(name, hclwrite.TokensForValue(value))
	}
	_, err := w.Write(file.Bytes())
	return err
}

func anyCty(value any) (cty.Value, error) {
	switch value := value.(type) {
	case nil:
		return cty.NullVal(cty.DynamicPseudoType), nil
	case string:
		return cty.StringVal(value), nil
	case bool:
		return cty.BoolVal(value), nil
	case int:
		return cty.NumberIntVal(int64(value)), nil
	case map[string]any:
		values := make(map[string]cty.Value, len(value))
		for key, item := range value {
			converted, err := anyCty(item)
			if err != nil {
				return cty.NilVal, err
			}
			values[key] = converted
		}
		if len(values) == 0 {
			return cty.EmptyObjectVal, nil
		}
		return cty.ObjectVal(values), nil
	case map[string]configTarget:
		values := make(map[string]cty.Value, len(value))
		for key, item := range value {
			converted, err := anyCty(map[string]any{"kind": item.Kind, "file": item.File, "name": item.Name, "label": item.Label, "index": item.Index, "script": item.Script, "inline": item.Inline})
			if err != nil {
				return cty.NilVal, err
			}
			values[key] = converted
		}
		if len(values) == 0 {
			return cty.EmptyObjectVal, nil
		}
		return cty.ObjectVal(values), nil
	default:
		return cty.NilVal, fmt.Errorf("unsupported value %T", value)
	}
}
