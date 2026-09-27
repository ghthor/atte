package cmd

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graphtarget"
	"github.com/ghthor/atte/reference/selector"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"github.com/spf13/cobra"
)

func runTestTarget(presentation selector.Target, label string) runTarget {
	canonical := presentation.String()
	id := graphtarget.ID{
		ID:        graph.EntityID(canonical),
		Namespace: "fixture",
		Kind:      presentation.Kind,
		Path:      presentation.Path,
		Name:      presentation.Name,
		Label:     label,
		Index:     presentation.Index,
		Aliases:   presentation.Aliases,
	}
	return runTarget{id: id, selector: canonical, presentation: presentation, label: label}
}

func runTestScanner(t *testing.T, targets []runTarget) detector.Scanner {
	t.Helper()
	ids := make([]graphtarget.ID, 0, len(targets))
	presentations := make(map[graph.EntityID]selector.Target, len(targets))
	for _, target := range targets {
		ids = append(ids, target.id)
		presentations[target.id.ID] = target.presentation
	}
	builder := detector.NewBuilder()
	must.NoError(t, builder.Attach(detector.SensorSpec{
		Namespace: "fixture",
		Targets: func(context.Context, *attegit.Repo) ([]graphtarget.ID, error) {
			return ids, nil
		},
		TargetSelector: func(target graphtarget.ID) selector.Target {
			return presentations[target.ID]
		},
		ExecuteTarget: func(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error) {
			return graphtarget.Execution{Args: []string{"true"}}, nil
		},
	}))
	return compileTestDetector(t, builder)
}

func compileTestDetector(t *testing.T, builder *detector.Builder) detector.Scanner {
	t.Helper()
	scanner, err := builder.Compile()
	must.NoError(t, err)
	return scanner
}

func TestMatchesRunTarget(t *testing.T) {
	target := runTestTarget(selector.HCL("atte.hcl", "test", "go", 0), "")
	scanner := runTestScanner(t, []runTarget{target})
	match := func(input string, want bool) {
		t.Helper()
		test.EqOp(t, want, matchesRunTargetAt(scanner, input, target, ""))
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
	hclTarget := runTestTarget(selector.HCL("detector/atte.hcl", "test", "go", 0), "")
	goTarget := runTestTarget(selector.GoTest("detector/attego"), "")
	targets := []runTarget{hclTarget, goTarget}
	scanner := runTestScanner(t, targets)
	match := func(input, relative string, want bool) {
		t.Helper()
		test.EqOp(t, want, matchesRunTargetAt(scanner, input, hclTarget, relative))
	}

	match("atte.hcl#test.go", "detector", true)
	test.True(
		t,
		matchesRunTargetAt(scanner, "attego#go_test", goTarget, "detector"),
		test.Sprintf("relative package selector should match from the current directory"),
	)
	match("../atte.hcl#test.go", "detector", false)
	match("..#test.go", "cmd", false)
	match("detector/atte.hcl#test.go", "", true)
	match("detector#test.go", "", true)
	match("../../atte.hcl#test.go", "detector/attego", false)
	match("../outside#test.go", "detector", false)
}

func TestRunTargetPaths(t *testing.T) {
	target := runTestTarget(selector.HCL("detector/atte.hcl", "test", "go", 0), "")
	canonicalPath, canonicalDir := runTargetPaths(target)
	test.EqOp(t, "detector/atte.hcl", canonicalPath)
	test.EqOp(t, "detector", canonicalDir)
}

func TestRunCmdValidArgs(t *testing.T) {
	targets := []runTarget{
		runTestTarget(selector.HCL("atte.hcl", "test", "go", 0), ""),
		runTestTarget(selector.HCL("detector/atte.hcl", "test", "py", 0), ""),
		runTestTarget(selector.GoTest("detector/attego"), ""),
		runTestTarget(selector.GoTest("reference"), ""),
	}
	repoRoot := filepath.FromSlash("/repo")
	complete := func(relative, prefix string, want []string) {
		t.Helper()
		matches, directive := runCmdValidArgsFromTargets(nil, prefix, repoRoot, filepath.Join(repoRoot, relative), targets)
		test.SliceEqOp(t, want, matches)
		test.EqOp(t, cobra.ShellCompDirectiveNoFileComp, directive)
		for _, want := range want {
			test.StrHasPrefix(t, prefix, want, test.Sprintf("completions must prefix match with the completion request or the shell will ignore them"))
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
	test.Nil(t, matches)
	test.EqOp(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestResolveRunTargetTypedErrors(t *testing.T) {
	targets := []runTarget{
		runTestTarget(selector.HCL("one/atte.hcl", "test", "go", 0), ""),
		runTestTarget(selector.HCL("two/atte.hcl", "test", "go", 0), ""),
	}
	scanner := runTestScanner(t, targets)

	_, err := resolveRunTargetAt(t.Context(), scanner, "missing", targets, "")
	var noMatch *selector.NoMatchError
	test.True(t, errors.As(err, &noMatch), test.Sprintf("missing targets should return a selector no-match error"))
	test.EqOp(t, "missing", noMatch.Input)

	_, err = resolveRunTargetAt(t.Context(), scanner, "test", targets, "")
	var ambiguous *selector.AmbiguousError
	test.True(t, errors.As(err, &ambiguous), test.Sprintf("ambiguous targets should return a selector ambiguity error"))
	test.SliceEqOp(t, []selector.AmbiguousCandidate{
		{TargetID: "//one/atte.hcl#test.go", Selector: "//one/atte.hcl#test.go"},
		{TargetID: "//two/atte.hcl#test.go", Selector: "//two/atte.hcl#test.go"},
	}, ambiguous.Candidates)
}

func TestResolveRunTargetCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := resolveRunTargetAt(ctx, runTestScanner(t, nil), "missing", nil, "")
	test.EqOp(t, context.Canceled, err)
}

func TestResolveRunTarget(t *testing.T) {
	targets := []runTarget{
		runTestTarget(selector.HCL("atte.hcl", "test", "go", 0), ""),
		runTestTarget(selector.HCL("atte.hcl", "test", "py", 1), ""),
		runTestTarget(selector.HCL("atte.hcl", "test", "2", 2), ""),
	}
	resolve := func(input, relative string, targetList []runTarget, want string) {
		t.Helper()
		scanner := runTestScanner(t, targetList)
		resolved, err := resolveRunTargetAt(t.Context(), scanner, input, targetList, relative)
		test.NoError(t, err)
		test.EqOp(t, want, resolved.selector)
	}

	resolve("//atte.hcl#test.go", "", targets, "//atte.hcl#test.go")
	resolve("test.go", "", targets, "//atte.hcl#test.go")
	resolve(".#test.go", "", targets, "//atte.hcl#test.go")
	resolve("test.1", "", targets, "//atte.hcl#test.py")

	relativeTargets := []runTarget{
		runTestTarget(selector.HCL("atte.hcl", "test", "", 0), ""),
		runTestTarget(selector.HCL("detector/atte.hcl", "test", "go", 0), ""),
	}
	resolve("..#test", "detector", relativeTargets, "//atte.hcl#test")
	resolve("../atte.hcl#test.go", "detector/attego", relativeTargets, "//detector/atte.hcl#test.go")

	rootLabeledTargets := []runTarget{runTestTarget(selector.HCL("atte.hcl", "test", "go", 0), "nightly")}
	resolve("..#test", "cmd", rootLabeledTargets, "//atte.hcl#test.go")
	resolve("..#test.go", "cmd", rootLabeledTargets, "//atte.hcl#test.go")

	goTargets := []runTarget{runTestTarget(selector.GoTest("detector/attego"), "")}
	resolve("../detector/attego#go_test", "cmd", goTargets, "//detector/attego#go_test")

	t.Run("local alias", func(t *testing.T) {
		localTargets := []runTarget{
			runTestTarget(selector.GoTest("cmd"), ""),
			runTestTarget(selector.GoTest("reference"), ""),
			runTestTarget(selector.GoTest("reference/selector"), ""),
		}
		scanner := runTestScanner(t, localTargets)
		resolved, err := resolveRunTargetAt(t.Context(), scanner, "go_test", localTargets, "reference")
		test.NoError(t, err)
		test.EqOp(t, "//reference#go_test", resolved.selector)
	})

	t.Run("local descendant fallback", func(t *testing.T) {
		localTargets := []runTarget{
			runTestTarget(selector.GoTest("outside"), ""),
			runTestTarget(selector.GoTest("reference/selector"), ""),
		}
		scanner := runTestScanner(t, localTargets)
		resolved, err := resolveRunTargetAt(t.Context(), scanner, "go_test", localTargets, "reference")
		test.NoError(t, err)
		test.EqOp(t, "//reference/selector#go_test", resolved.selector)
	})

	t.Run("HCL label fallback", func(t *testing.T) {
		labelTarget := runTestTarget(selector.HCL("atte.hcl", "test", "go", 0), "nightly")
		targets := []runTarget{labelTarget}
		scanner := runTestScanner(t, targets)
		resolved, err := resolveRunTargetAt(t.Context(), scanner, "nightly", targets, "")
		test.NoError(t, err)
		test.EqOp(t, labelTarget.selector, resolved.selector)

		_, err = resolveRunTargetAt(t.Context(), scanner, "atte.hcl#nightly", targets, "")
		test.ErrorContains(t, err, "not found")
	})

	t.Run("selector match precedes label fallback", func(t *testing.T) {
		labelTarget := runTestTarget(selector.HCL("atte.hcl", "test", "go", 0), "nightly")
		selectorTargetPresentation := selector.GoTest("reference/selector")
		selectorTargetPresentation.Aliases = []string{"nightly"}
		selectorTarget := runTestTarget(selectorTargetPresentation, "")
		targets := []runTarget{labelTarget, selectorTarget}
		scanner := runTestScanner(t, targets)
		resolved, err := resolveRunTargetAt(t.Context(), scanner, "nightly", targets, "")
		test.NoError(t, err)
		test.EqOp(t, selectorTarget.selector, resolved.selector)
	})

	t.Run("duplicate canonical selector", func(t *testing.T) {
		first := runTestTarget(selector.GoTest("same"), "")
		second := runTestTarget(selector.GoTest("same"), "")
		first.id.ID = "fixture:first"
		second.id.ID = "fixture:second"
		duplicates := []runTarget{second, first}
		scanner := runTestScanner(t, duplicates)
		_, err := resolveRunTargetAt(t.Context(), scanner, first.selector, duplicates, "")
		var ambiguous *selector.AmbiguousError
		test.True(t, errors.As(err, &ambiguous), test.Sprintf("duplicate canonical selectors should not choose an arbitrary target"))
		test.SliceEqOp(t, []selector.AmbiguousCandidate{
			{TargetID: "fixture:first", Selector: first.selector},
			{TargetID: "fixture:second", Selector: first.selector},
		}, ambiguous.Candidates)
	})

	t.Run("ambiguity", func(t *testing.T) {
		scanner := runTestScanner(t, targets)
		_, err := resolveRunTargetAt(t.Context(), scanner, "test", targets, "")
		test.Error(t, err)
		test.EqOp(t, "selector \"test\" is ambiguous; possible commands:\n"+
			"atte run //atte.hcl#test.2\n"+
			"atte run //atte.hcl#test.go\n"+
			"atte run //atte.hcl#test.py", err.Error())
	})

	t.Run("missing target", func(t *testing.T) {
		scanner := runTestScanner(t, targets)
		_, err := resolveRunTargetAt(t.Context(), scanner, "missing", targets, "")
		test.Error(t, err)
		test.StrContains(t, err.Error(), "not found")
	})
}
