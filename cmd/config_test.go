package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
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
	config, err := attehcl.ConfigFor(t.Context(), repo, "child", attegit.PathHCLFunctions)
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
