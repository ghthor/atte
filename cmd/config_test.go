package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/ghthor/atte/reference"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestConfigShowGlobalInheritanceGolden(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"atte.hcl": `

globals {
  go_ver = "1.26"
  script = path("./root.sh")
  root_path = path("./root.sh")
}
`,
		"root.sh": "#!/bin/sh\n",
		"child/atte.hcl": `

globals {
  script = path("./child.sh")
  go_ver = "1.27"
}

locals {
  version = global.go_ver
}

test "child" {
  script = <<-EOF
    echo inline
  EOF
}
test "child2" {
  script = path("./child.sh")
}
`,
		"child/child.sh": "#!/bin/sh\n",
	})
	config, err := attehcl.ConfigFor(t.Context(), repo, "child", attegit.PathHCLFunctions)
	test.NoError(t, err)
	test.EqOp(t, "1.27", config.Global["go_ver"].AsString())
	test.EqOp(t, "child/child.sh", (*config.Global["script"].EncapsulatedValue().(*reference.Blob)).String())
	test.Len(t, 2, config.Targets)
	test.EqOp(t, "child", config.Targets[0].Name)
	test.EqOp(t, "echo inline\n", config.Targets[0].Inline)
	test.EqOp(t, reference.Blob(""), config.Targets[0].Script)
	test.EqOp(t, "child2", config.Targets[1].Name)
	test.EqOp(t, "", config.Targets[1].Inline)
	test.EqOp(t, reference.Blob("child/child.sh"), config.Targets[1].Script)
	var got bytes.Buffer
	must.NoError(t, writeConfig(&got, config, "hcl"))
	assertConfigGolden(t, "global-inheritance", got.String())
}

func TestConfigShowGlobalInheritanceWithoutLocalConfig(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{
		"atte.hcl": `

globals {
  go_ver = "1.26"
}
`,
		"child/file.txt": "child\n",
	})
	config, err := attehcl.ConfigFor(t.Context(), repo, "child", attegit.PathHCLFunctions)
	test.NoError(t, err)
	test.EqOp(t, "1.26", config.Global["go_ver"].AsString())
	test.EqOp(t, 0, len(config.Local))
	test.EqOp(t, 0, len(config.Targets))

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
