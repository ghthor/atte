package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/reference/target"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"
)

type configOptions struct {
	ref         string
	workingTree bool
	format      string
}

func newConfigCommand() *cobra.Command {
	options := &configOptions{}
	configCmd := &cobra.Command{Use: "config", Short: "Inspect evaluated configuration"}
	configShowCmd := &cobra.Command{
		Use:   "show",
		Short: "Show evaluated configuration for the current directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cwd, err := commandWorkingDirectory(ctx)
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			root, relative, err := repositoryContext(ctx, cwd)
			if err != nil {
				return err
			}
			var openOptions []attegit.OpenOption
			if options.workingTree {
				openOptions = append(openOptions, attegit.WithWorkingTree())
			}
			repo, err := openRepository(ctx, root, options.ref, openOptions...)
			if err != nil {
				return err
			}
			detector, err := detectorForContext(ctx)
			if err != nil {
				return fmt.Errorf("register detectors: %w", err)
			}
			config, err := attehcl.ConfigFor(cmd.Context(), repo, relative, detector)
			if err != nil {
				return fmt.Errorf("evaluate attehcl configuration: %w", err)
			}
			return writeConfig(cmd.OutOrStdout(), config, options.format)
		},
	}
	configShowCmd.Flags().StringVarP(&options.ref, "ref", "r", "HEAD", "Git revision to inspect")
	configShowCmd.Flags().BoolVar(&options.workingTree, "working-tree", false, "Include modified and non-ignored untracked files")
	configShowCmd.Flags().StringVar(&options.format, "format", "json", "Output format (json or hcl)")
	configCmd.AddCommand(configShowCmd)
	return configCmd
}

type configOutput struct {
	Target map[string]target.Computed `json:"target"`
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
	targets := make(map[string]target.Computed, len(config.Targets))
	for _, item := range attehcl.SortedTargets(config.Targets) {
		key := fmt.Sprintf("//%s#%s.%s", item.File, strings.TrimPrefix(item.Kind, attehcl.Namespace+":"), item.DisplayName())
		computed, err := item.Configuration()
		if err != nil {
			return configOutput{}, err
		}
		targets[key] = computed
	}
	return configOutput{Target: targets}, nil
}

func writeConfigHCL(w interface{ Write([]byte) (int, error) }, output configOutput) error {
	file := hclwrite.NewEmptyFile()
	body := file.Body()
	values := map[string]any{"target": output.Target}
	for _, name := range []string{"target"} {
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
	case target.Computed:
		values := map[string]any{
			"kind":   value.Kind,
			"file":   value.File,
			"name":   value.Name,
			"label":  value.Label,
			"index":  value.Index,
			"script": value.Script,
			"inline": value.Inline,
		}
		if value.Meta != nil {
			values["meta"] = value.Meta
		}
		return anyCty(values)
	case map[string]target.Computed:
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
	default:
		return cty.NilVal, fmt.Errorf("unsupported value %T", value)
	}
}
