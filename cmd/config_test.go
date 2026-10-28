package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/detector/attehcl/hclblock"
	"github.com/ghthor/atte/detector/scanner"
	"github.com/hashicorp/hcl/v2"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"github.com/zclconf/go-cty/cty"
)

func TestConfigShowTargetCentric(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"child/atte.hcl": `
locals {
  inline_script = <<-EOF
    echo inline
  EOF
  file_script = path("./child.sh")
}

test "child" {
  script = local.inline_script
}
test "child2" {
  script = local.file_script
}
`,
		"child/child.sh": "#!/bin/sh\n",
	})
	builder, err := scanner.NewDefault()
	must.NoError(t, err)
	detector := compileTestDetector(t, builder)
	config, err := attehcl.ConfigFor(t.Context(), repo, "child", detector)
	test.NoError(t, err)
	targets := attehcl.SortedTargets(config.Targets)
	test.Len(t, 2, targets)
	test.EqOp(t, "child", targets[0].Name)
	test.EqOp(t, "echo inline\n", targets[0].Inline)
	test.EqOp(t, "", targets[0].Script.String())
	test.EqOp(t, "child2", targets[1].Name)
	test.EqOp(t, "child/child.sh", targets[1].Script.String())
	var got bytes.Buffer
	must.NoError(t, writeConfig(&got, config, "hcl"))
	assertConfigGolden(t, "target-centric", got.String())
}

func TestConfigShowSettingBlocks(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"child/atte.hcl": strings.TrimLeft(`
locals {
  shell = "bash"
}

settings {
  shell = local.shell
  flags = ["-e", "-u"]
}

test "child" {
  script = "echo child"
}
`, "\n"),
	})
	builder, err := scanner.NewDefault()
	must.NoError(t, err)
	must.NoError(t, detector.AttachHCLSettingBlock(builder, "settings", hclblock.SettingBlock{
		Schema: &hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "shell", Required: true}, {Name: "flags"}}},
		Decoder: func(content *hcl.BodyContent, ctx *hcl.EvalContext) (any, error) {
			values := make(map[string]any, len(content.Attributes))
			for name, attribute := range content.Attributes {
				value, diagnostics := attribute.Expr.Value(ctx)
				if diagnostics.HasErrors() {
					return nil, diagnostics
				}
				if value.Type() == cty.String {
					values[name] = value.AsString()
					continue
				}
				items := make([]any, 0, value.LengthInt())
				for _, item := range value.AsValueSlice() {
					items = append(items, item.AsString())
				}
				values[name] = items
			}
			return values, nil
		},
		Config: func(decoded any) (map[string]any, error) {
			return decoded.(map[string]any), nil
		},
	}))
	detector := compileTestDetector(t, builder)
	config, err := attehcl.ConfigFor(t.Context(), repo, "child", detector)
	must.NoError(t, err)
	must.Len(t, 1, config.Settings)

	var hclOutput bytes.Buffer
	must.NoError(t, writeConfig(&hclOutput, config, "hcl"))
	assertConfigGolden(t, "setting-blocks", hclOutput.String())

	var jsonOutput bytes.Buffer
	must.NoError(t, writeConfig(&jsonOutput, config, "json"))
	var decoded map[string]map[string]map[string]any
	must.NoError(t, json.Unmarshal(jsonOutput.Bytes(), &decoded))
	settings := decoded["settings"]["//child/atte.hcl#settings"]
	test.EqOp(t, "child/atte.hcl", settings["file"])
	test.EqOp(t, "settings", settings["name"])
	test.Eq(t, any(map[string]any{"shell": "bash", "flags": []any{"-e", "-u"}}), settings["values"])
}

func assertConfigGolden(t *testing.T, name, got string) {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	goldenPath := filepath.Join(filepath.Dir(filename), "testdata", "config", name+".hcl")
	must.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
	if os.Getenv("ATTE_CODEGEN") != "" {
		must.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(goldenPath)
	test.NoError(t, err)
	test.EqOp(t, string(want), got)
}
