package cmd

import (
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

	check := func(selector string, want bool) {
		t.Helper()
		must.EqOp(t, want, matchesRunTarget(selector, target))
	}

	check("//atte.hcl#test.go", true)
	check("//#test.go", true)
	check("atte.hcl#test.go", true)
	check("#test.go", true)
	check("test", true)
	check("test.go", true)
	check("test.0", true)
	check("test.py", false)
	check("lint", false)
	check("sub/atte.hcl#test.go", false)
	check("../atte.hcl#test.go", false)
}

func TestMatchesRunTargetAt(t *testing.T) {
	target := runTarget{
		selector: "//detector/atte.hcl#test.go",
		kind:     attehcl.TestKind,
		path:     "detector/atte.hcl",
		name:     "go",
		index:    0,
	}

	check := func(selector, relative string, want bool) {
		t.Helper()
		must.EqOp(t, want, matchesRunTargetAt(selector, target, relative))
	}

	check("../atte.hcl#test.go", "detector/attego", true)
	check("../atte.hcl#test.go", "detector", false)
	check("..#test.go", "cmd", false)
	check("detector/atte.hcl#test.go", "", true)
	check("detector#test.go", "", true)
	check("../../atte.hcl#test.go", "detector/attego", false)
	check("../outside#test.go", "detector", false)
}

func TestRunCmdValidArgs(t *testing.T) {
	targets := []runTarget{
		{selector: "//atte.hcl#test.go", kind: attehcl.TestKind, path: "atte.hcl", name: "go", index: 0},
		{selector: "//detector/atte.hcl#test.py", kind: attehcl.TestKind, path: "detector/atte.hcl", name: "py", index: 0},
		{selector: "//detector/attego#go_test", kind: attego.PackageTestKind, path: "detector/attego", name: "go_test"},
	}

	complete := func(relative, prefix string, want []string) {
		t.Helper()
		matches, directive := runCmdValidArgsFromTargets(nil, prefix, relative, targets)
		must.SliceEqOp(t, want, matches)
		must.EqOp(t, cobra.ShellCompDirectiveNoFileComp, directive)
		for _, want := range want {
			must.StrHasPrefix(t, prefix, want, must.Sprint("completions must prefix match with the completion request or the shell will ignore them"))
		}
	}

	// TODO:
	// all of the wants need to strings.HasPrefix(want[0], prefix) == true
	// so you need to convert the canonical references after matching back to relative references
	complete("", "//atte", []string{"//atte.hcl#test.go"})
	complete("", "//detector/", []string{"//detector/atte.hcl#test.py", "//detector/attego#go_test"})
	complete("detector", "../", []string{"../atte.hcl#test.go"})
	complete("detector/attego", "../../detector/", []string{"../../detector/atte.hcl#test.py", "../../detector/attego#go_test"})
	complete("detector/attego", "../../atte.hcl#", []string{"../../atte.hcl#test.go"})
	complete("detector/attego", "../../atte.hcl#test", []string{"../../atte.hcl#test.go"})

	matches, directive := runCmdValidArgsFromTargets([]string{"existing"}, "", "", targets)
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

	t.Run("ambiguity", func(t *testing.T) {
		_, err := resolveRunTarget("test", targets)
		must.Error(t, err)
		must.StrContains(t, err.Error(), "ambiguous")
	})

	t.Run("missing target", func(t *testing.T) {
		_, err := resolveRunTarget("missing", targets)
		must.Error(t, err)
		must.StrContains(t, err.Error(), "not found")
	})
}
