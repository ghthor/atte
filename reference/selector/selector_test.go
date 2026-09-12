package selector

import (
	"testing"

	"github.com/shoenig/test"
)

func TestParseCanonicalSelector(t *testing.T) {
	got, err := Parse("//pkg/atte.hcl#test.default")
	test.NoError(t, err)
	test.EqOp(t, "pkg/atte.hcl", got.Path)
	test.EqOp(t, "test.default", got.Identifier)
	test.EqOp(t, "//pkg/atte.hcl#test.default", got.String())
}

func TestParseKeepsIdentifierOpaque(t *testing.T) {
	got, err := Parse("//pkg#plugin.identifier.with.dots")
	test.NoError(t, err)
	test.EqOp(t, "plugin.identifier.with.dots", got.Identifier)
}

func TestResolveRelativePath(t *testing.T) {
	got, err := Resolve("../atte.hcl#test.go", "detector/attego")
	test.NoError(t, err)
	test.EqOp(t, "detector/atte.hcl", got.Path)
}

func TestResolveRejectsRepositoryEscape(t *testing.T) {
	_, err := Resolve("../../../atte.hcl#test.go", "detector/attego")
	test.ErrorContains(t, err, "escapes repository root")
}

func TestPathMatchesFileAndContainingDirectory(t *testing.T) {
	test.True(t, PathMatches("detector/atte.hcl", "", "detector/atte.hcl", "detector"), test.Sprintf("full HCL file path should match"))
	test.True(t, PathMatches("detector", "", "detector/atte.hcl", "detector"), test.Sprintf("HCL containing directory should match"))
	test.True(
		t,
		PathMatches("../atte.hcl", "detector/attego", "detector/atte.hcl", "detector"),
		test.Sprintf("relative HCL file path should resolve and match"),
	)
	test.False(t, PathMatches("../outside", "detector", "detector/atte.hcl", "detector"), test.Sprintf("path outside the target should not match"))
}

func TestContainingDir(t *testing.T) {
	test.EqOp(t, "", ContainingDir("atte.hcl"))
	test.EqOp(t, "detector", ContainingDir("detector/atte.hcl"))
	test.EqOp(t, "detector/attego", ContainingDir("detector/attego"), test.Sprintf("a non-HCL path is returned unchanged"))
}

func TestPathIsWithin(t *testing.T) {
	test.True(t, PathIsWithin("detector", "detector"), test.Sprintf("a path is within itself"))
	test.True(t, PathIsWithin("detector/attego", "detector"), test.Sprintf("a descendant is within its ancestor"))
	test.True(t, PathIsWithin("detector", ""), test.Sprintf("every path is within the repository root"))
	test.False(t, PathIsWithin("detectorx", "detector"), test.Sprintf("a sibling sharing a string prefix must not match"))
	test.False(t, PathIsWithin("foobar", "foo"), test.Sprintf("segment boundaries must be respected, not raw string prefixes"))
}

func TestRelativePath(t *testing.T) {
	rel, ok := RelativePath("detector/attego", "detector")
	test.True(t, ok, test.Sprintf("a descendant path should resolve relative to its ancestor"))
	test.EqOp(t, "attego", rel)

	rel, ok = RelativePath("detector", "detector")
	test.True(t, ok, test.Sprintf("a path is within itself"))
	test.EqOp(t, "", rel)

	_, ok = RelativePath("detectorx", "detector")
	test.False(t, ok, test.Sprintf("a sibling sharing a string prefix must not resolve"))
}

func TestTargetMatchesHCL(t *testing.T) {
	target := HCL("atte.hcl", "test", "go", 0)
	match := func(input string, want bool) {
		t.Helper()
		test.EqOp(t, want, target.Matches(input, ""))
	}

	match("//atte.hcl#test.go", true)
	match("//#test.go", true)
	match("atte.hcl#test.go", true)
	match("#test.go", true)
	match("test", true)
	match("test.go", true)
	match("test.0", true)
	match("#test.go", true)
	match("#test.0", true)
	match("test.py", false)
	match("lint", false)
	match("sub/atte.hcl#test.go", false)
	match("../atte.hcl#test.go", false)
}

func TestTargetMatchesHCLRelative(t *testing.T) {
	target := HCL("detector/atte.hcl", "test", "go", 0)
	match := func(input, relative string, want bool) {
		t.Helper()
		test.EqOp(t, want, target.Matches(input, relative))
	}

	match("../atte.hcl#test.go", "detector/attego", true)
	match("atte.hcl#test.go", "detector", true)
	match("../atte.hcl#test.go", "detector", false)
	match("..#test.go", "cmd", false)
	match("detector/atte.hcl#test.go", "", true)
	match("detector/atte.hcl#test.0", "", true)
	match("detector#test.go", "", true)
	match("detector#test.0", "", true)
	match("../../atte.hcl#test.go", "detector/attego", false)
	match("../outside#test.go", "detector", false)
	match("detectorx#test.go", "", false)
}

func TestTargetMatchesGoTest(t *testing.T) {
	target := GoTest("detector/attego")
	test.True(t, target.Matches("attego#go_test", "detector"), test.Sprintf("relative package selector should match from the current directory"))
	test.True(t, target.Matches("go_test", "detector/attego"), test.Sprintf("bare go_test alias should match from the package directory"))
	test.True(t, target.Matches("go_test.0", "unrelated"), test.Sprintf("indexed go_test alias should remain path-independent"))
	test.False(
		t,
		target.Matches("attego#go_test", "detector/attegox"),
		test.Sprintf("a sibling package sharing a string prefix must not match a path-qualified selector"),
	)
}

func TestSelectorNoHCLFile(t *testing.T) {
	f := func(in, want string) {
		t.Helper()

		s, err := Parse(in)
		test.NoError(t, err)

		test.Eq(t, s.Tree().String(), want)
	}

	f("atte.hcl#test", "")
	f("//atte.hcl#test", "")

	f("subdir#go_test", "subdir")
	f("//subdir#go_test", "subdir")

	f("subdir/atte.hcl#test", "subdir")
	f("//subdir/atte.hcl#test", "subdir")
}
