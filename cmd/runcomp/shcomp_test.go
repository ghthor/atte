package runcomp

import (
	"path/filepath"
	"testing"

	"github.com/shoenig/test"
)

func TestHasRelative(t *testing.T) {
	test.False(t, hasRelative("/repo/path", ""))
	test.False(t, hasRelative("/repo/path", "sub"))
	test.True(t, hasRelative("/repo/path", ".."))
}

func TestMatchCompletionCorpus(t *testing.T) {
	targets := []string{
		"//atte.hcl#codegen.fmt",
		"//atte.hcl#codegen.go",
		"//atte.hcl#lint.go",
		"//atte.hcl#test.build",
		"//atte.hcl#test.go",

		"//cmd#go_test",
		"//cmd/runcomp#go_test",

		"//detector/testdata#codegen.0",
		"//detector/attegit#go_test",
		"//detector/attegittest#go_test",
		"//detector/attego#go_test",
		"//detector/attehcl#go_test",
		"//detector/atte.hcl#test.py",

		"//graph#go_test",
		"//reference#go_test",
		"//reference/selector#go_test",
		"//registry#go_test",
	}
	complete := func(wd, toComplete string, want ...string) {
		t.Helper()
		got, err := Match(filepath.FromSlash("/repo"), filepath.FromSlash(wd), toComplete, targets)
		test.NoError(t, err)
		test.SliceEqOp(t, want, got, test.Sprintf("wd=%s toComplete=%s", wd, toComplete))
		for _, completion := range got {
			test.StrHasPrefix(t, toComplete, completion, test.Sprintf("completion must preserve the requested prefix: wd=%s toComplete=%s", wd, toComplete))
		}
	}

	complete("/repo", "a",
		"atte.hcl#codegen.fmt",
		"atte.hcl#codegen.go",
		"atte.hcl#lint.go",
		"atte.hcl#test.build",
		"atte.hcl#test.go",
	)

	t.Run("working directory short form", func(t *testing.T) {
		complete("/repo", "t", "test.build", "test.go")
		complete("/repo", "test", "test.build", "test.go")
		complete("/repo", "test.go", "test.go")

		t.Run("in a repo relative subdirectory", func(t *testing.T) {
			complete("/repo/detector", "t", "test.py", "testdata#codegen.0")
			complete("/repo/detector", "", "test.py", "atte.hcl#test.py", "attegit#go_test", "attegittest#go_test", "attego#go_test", "attehcl#go_test", "testdata#codegen.0")
			complete("/repo/detector", "atte", "atte.hcl#test.py", "attegit#go_test", "attegittest#go_test", "attego#go_test", "attehcl#go_test")
			complete("/repo/detector", "attego#", "attego#go_test")
			complete("/repo/detector/attego", "", "go_test", "detector/attego#go_test")
		})
	})

	t.Run("canonical", func(t *testing.T) {
		complete("/repo", "/", targets...)
		complete("/repo", "//atte", "//atte.hcl#codegen.fmt", "//atte.hcl#codegen.go", "//atte.hcl#lint.go", "//atte.hcl#test.build", "//atte.hcl#test.go")
		complete("/repo", "//detector/", "//detector/atte.hcl#test.py", "//detector/attegit#go_test", "//detector/attegittest#go_test", "//detector/attego#go_test", "//detector/attehcl#go_test", "//detector/testdata#codegen.0")
	})

	t.Run("relative (not supported at this time)", func(t *testing.T) {
		// complete("/repo/detector", "../r", []string{"../reference#go_test"})
		// complete("/repo/detector", "../", []string{"../atte.hcl#test.go", "../reference#go_test"})
		// complete("/repo/detector/attego", "../../detector/", []string{"../../detector/atte.hcl#test.py", "../../detector/attego#go_test"})
		// complete("/repo/detector/attego", "../../atte.hcl#", []string{"../../atte.hcl#test.go"})
	})
}
