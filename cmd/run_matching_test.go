package cmd

import (
	"path/filepath"
	"testing"

	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
	"github.com/shoenig/test/must"
	"github.com/spf13/cobra"
)

func TestMatchesRunTarget(t *testing.T) {
	target := runTarget{
		selector: "//atte.hcl#test.go",
		kind:     attehcl.TestKind,
		path:     "atte.hcl",
		name:     "go",
		index:    0,
	}
	match := func(selector string, want bool) {
		t.Helper()
		must.EqOp(t, want, matchesRunTarget(selector, target))
	}

	match("//atte.hcl#test.go", true)
	match("//#test.go", true)
	match("atte.hcl#test.go", true)
	match("#test.go", true)
	match("test", true)
	match("test.go", true)
	match("test.0", true)
	match("test.py", false)
	match("lint", false)
	match("sub/atte.hcl#test.go", false)
	match("../atte.hcl#test.go", false)
}

func TestMatchesRunTargetAt(t *testing.T) {
	target := runTarget{
		selector: "//detector/atte.hcl#test.go",
		kind:     attehcl.TestKind,
		path:     "detector/atte.hcl",
		name:     "go",
		index:    0,
	}
	match := func(selector, relative string, want bool) {
		t.Helper()
		must.EqOp(t, want, matchesRunTargetAt(selector, target, relative))
	}

	match("atte.hcl#test.go", "detector", true)
	goTarget := runTarget{selector: "//detector/attego#go_test", kind: attego.PackageTestKind, path: "detector/attego", name: "go_test"}
	must.True(t, matchesRunTargetAt("attego#go_test", goTarget, "detector"), must.Sprint("relative package selector should match from the current directory"))
	match("../atte.hcl#test.go", "detector", false)
	match("..#test.go", "cmd", false)
	match("detector/atte.hcl#test.go", "", true)
	match("detector#test.go", "", true)
	match("../../atte.hcl#test.go", "detector/attego", false)
	match("../outside#test.go", "detector", false)
}

func TestRunTargetPaths(t *testing.T) {
	target := runTarget{selector: "//detector/atte.hcl#test.go"}
	canonicalPath, canonicalDir := runTargetPaths(target)
	must.EqOp(t, "detector/atte.hcl", canonicalPath)
	must.EqOp(t, "detector", canonicalDir)
}

func TestRunCmdValidArgs(t *testing.T) {
	targets := []runTarget{
		{selector: "//atte.hcl#test.go", kind: attehcl.TestKind, path: "atte.hcl", name: "go", index: 0},
		{selector: "//detector/atte.hcl#test.py", kind: attehcl.TestKind, path: "detector/atte.hcl", name: "py", index: 0},
		{selector: "//detector/attego#go_test", kind: attego.PackageTestKind, path: "detector/attego", name: "go_test"},
		{selector: "//reference#go_test", kind: attego.PackageTestKind, path: "reference", name: "go_test"},
	}
	complete := func(relative, prefix string, want []string) {
		t.Helper()
		matches, directive := runCmdValidArgsFromTargets(nil, prefix, "/repo", filepath.Join("/repo", relative), targets)
		must.SliceEqOp(t, want, matches)
		must.EqOp(t, cobra.ShellCompDirectiveNoFileComp, directive)
		for _, want := range want {
			must.StrHasPrefix(t, prefix, want, must.Sprint("completions must prefix match with the completion request or the shell will ignore them"))
		}
	}

	complete("", "t", []string{"test.go"})
	complete("", "test.", []string{"test.go"})
	complete("", "test.go", []string{"test.go"})
	complete("detector", "t", []string{"test.py"})
	complete("detector", "", []string{"test.py", "atte.hcl#test.py", "attego#go_test"})
	complete("detector", "atte", []string{"atte.hcl#test.py", "attego#go_test"})
	complete("detector", "attego#", []string{"attego#go_test"})
	complete("", "//atte", []string{"//atte.hcl#test.go"})
	complete("", "//detector/", []string{"//detector/atte.hcl#test.py", "//detector/attego#go_test"})

	matches, directive := runCmdValidArgsFromTargets([]string{"existing"}, "", "/repo", "/repo", targets)
	must.Nil(t, matches)
	must.EqOp(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestResolveRunTarget(t *testing.T) {
	targets := []runTarget{
		{selector: "//atte.hcl#test.go", kind: attehcl.TestKind, path: "atte.hcl", name: "go", index: 0},
		{selector: "//atte.hcl#test.py", kind: attehcl.TestKind, path: "atte.hcl", name: "py", index: 1},
		{selector: "//atte.hcl#test.2", kind: attehcl.TestKind, path: "atte.hcl", name: "2", index: 2},
	}
	resolve := func(selector, relative string, targetList []runTarget, want string) {
		t.Helper()
		resolved, err := resolveRunTargetAt(selector, targetList, relative)
		must.NoError(t, err)
		must.EqOp(t, want, resolved.selector)
	}

	resolve("test.go", "", targets, "//atte.hcl#test.go")
	resolve(".#test.go", "", targets, "//atte.hcl#test.go")
	resolve("test.1", "", targets, "//atte.hcl#test.py")

	relativeTargets := []runTarget{
		{selector: "//#test", kind: attehcl.TestKind, path: "atte.hcl", name: "", index: 0},
		{selector: "//detector/atte.hcl#test.go", kind: attehcl.TestKind, path: "detector/atte.hcl", name: "go", index: 0},
	}
	resolve("..#test", "detector", relativeTargets, "//#test")
	resolve("../atte.hcl#test.go", "detector/attego", relativeTargets, "//detector/atte.hcl#test.go")

	rootLabeledTargets := []runTarget{{selector: "//atte.hcl#test.go", kind: attehcl.TestKind, path: "atte.hcl", name: "go", index: 0}}
	resolve("..#test", "cmd", rootLabeledTargets, "//atte.hcl#test.go")
	resolve("..#test.go", "cmd", rootLabeledTargets, "//atte.hcl#test.go")

	goTargets := []runTarget{{selector: "//detector/attego#go_test", kind: attego.PackageTestKind, path: "detector/attego", name: "go_test"}}
	resolve("../detector/attego#go_test", "cmd", goTargets, "//detector/attego#go_test")

	t.Run("local alias", func(t *testing.T) {
		localTargets := []runTarget{
			{selector: "//cmd#go_test", kind: attego.PackageTestKind, path: "cmd", name: "go_test"},
			{selector: "//reference#go_test", kind: attego.PackageTestKind, path: "reference", name: "go_test"},
			{selector: "//reference/selector#go_test", kind: attego.PackageTestKind, path: "reference/selector", name: "go_test"},
		}
		resolved, err := resolveRunTargetAt("go_test", localTargets, "reference")
		must.NoError(t, err)
		must.EqOp(t, "//reference#go_test", resolved.selector)
	})

	t.Run("ambiguity", func(t *testing.T) {
		_, err := resolveRunTarget("test", targets)
		must.Error(t, err)
		must.StrContains(t, err.Error(), "ambiguous")
		must.StrContains(t, err.Error(), "atte run //atte.hcl#test.go")
		must.StrContains(t, err.Error(), "atte run //atte.hcl#test.py")
		must.StrContains(t, err.Error(), "atte run //atte.hcl#test.2")
	})
	t.Run("missing target", func(t *testing.T) {
		_, err := resolveRunTarget("missing", targets)
		must.Error(t, err)
		must.StrContains(t, err.Error(), "not found")
	})
}
