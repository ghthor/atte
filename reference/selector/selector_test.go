package selector

import (
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestParseCanonicalSelector(t *testing.T) {
	got, err := Parse("//pkg/atte.hcl#test.default")
	must.NoError(t, err)
	must.EqOp(t, "pkg/atte.hcl", got.Path)
	must.EqOp(t, "test.default", got.Identifier)
	must.EqOp(t, "//pkg/atte.hcl#test.default", got.String())
}

func TestParseKeepsIdentifierOpaque(t *testing.T) {
	got, err := Parse("//pkg#plugin.identifier.with.dots")
	must.NoError(t, err)
	must.EqOp(t, "plugin.identifier.with.dots", got.Identifier)
}

func TestResolveRelativePath(t *testing.T) {
	got, err := Resolve("../atte.hcl#test.go", "detector/attego")
	must.NoError(t, err)
	must.EqOp(t, "detector/atte.hcl", got.Path)
}

func TestResolveRejectsRepositoryEscape(t *testing.T) {
	_, err := Resolve("../../../atte.hcl#test.go", "detector/attego")
	must.ErrorContains(t, err, "escapes repository root")
}

func TestPathMatchesFileAndContainingDirectory(t *testing.T) {
	must.True(t, PathMatches("detector/atte.hcl", "", "detector/atte.hcl", "detector"), must.Sprint("full HCL file path should match"))
	must.True(t, PathMatches("detector", "", "detector/atte.hcl", "detector"), must.Sprint("HCL containing directory should match"))
	must.True(t, PathMatches("../atte.hcl", "detector/attego", "detector/atte.hcl", "detector"), must.Sprint("relative HCL file path should resolve and match"))
	must.False(t, PathMatches("../outside", "detector", "detector/atte.hcl", "detector"), must.Sprint("path outside the target should not match"))
}

func TestContainingDir(t *testing.T) {
	must.EqOp(t, "", ContainingDir("atte.hcl"))
	must.EqOp(t, "detector", ContainingDir("detector/atte.hcl"))
	must.EqOp(t, "detector/attego", ContainingDir("detector/attego"), must.Sprint("a non-HCL path is returned unchanged"))
}

func TestPathIsWithin(t *testing.T) {
	must.True(t, PathIsWithin("detector", "detector"), must.Sprint("a path is within itself"))
	must.True(t, PathIsWithin("detector/attego", "detector"), must.Sprint("a descendant is within its ancestor"))
	must.True(t, PathIsWithin("detector", ""), must.Sprint("every path is within the repository root"))
	must.False(t, PathIsWithin("detectorx", "detector"), must.Sprint("a sibling sharing a string prefix must not match"))
	must.False(t, PathIsWithin("foobar", "foo"), must.Sprint("segment boundaries must be respected, not raw string prefixes"))
}

func TestRelativePath(t *testing.T) {
	rel, ok := RelativePath("detector/attego", "detector")
	must.True(t, ok, must.Sprint("a descendant path should resolve relative to its ancestor"))
	must.EqOp(t, "attego", rel)

	rel, ok = RelativePath("detector", "detector")
	must.True(t, ok, must.Sprint("a path is within itself"))
	must.EqOp(t, "", rel)

	_, ok = RelativePath("detectorx", "detector")
	must.False(t, ok, must.Sprint("a sibling sharing a string prefix must not resolve"))
}

func TestPathHasSegmentPrefix(t *testing.T) {
	must.True(t, PathHasSegmentPrefix("reference", "r"), must.Sprint("a partial final segment should match via raw string prefix"))
	must.True(t, PathHasSegmentPrefix("detector/attego", "detector"), must.Sprint("a complete leading segment should match its descendant"))
	must.True(t, PathHasSegmentPrefix("detector/attego", "detector/att"), must.Sprint("a partial final segment should match after exact leading segments"))
	must.False(t, PathHasSegmentPrefix("detector/attego", "reference"), must.Sprint("a mismatched leading segment must not match"))
	must.False(t, PathHasSegmentPrefix("detector", "detector/attego"), must.Sprint("prefix must not be longer than candidate"))
	must.True(t, PathHasSegmentPrefix("detector", ""), must.Sprint("an empty prefix matches everything"))
}

func TestIsImmediateChild(t *testing.T) {
	must.True(t, IsImmediateChild("reference", ""), must.Sprint("a top-level path is an immediate child of the repository root"))
	must.False(t, IsImmediateChild("detector/atte.hcl", ""), must.Sprint("a nested path is not an immediate child of the repository root"))
	must.True(t, IsImmediateChild("detector/attego", "detector"), must.Sprint("a direct subdirectory is an immediate child of its parent"))
	must.False(t, IsImmediateChild("detector/attego/sub", "detector"), must.Sprint("a grandchild is not an immediate child"))
	must.False(t, IsImmediateChild("reference", "detector"), must.Sprint("a path outside dir is not its child"))
	must.False(t, IsImmediateChild("detector", "detector"), must.Sprint("a path is not its own child"))
}

func TestTargetMatchesHCL(t *testing.T) {
	target := HCL("atte.hcl", "test", "go", 0)
	match := func(input string, want bool) {
		t.Helper()
		must.EqOp(t, want, target.Matches(input, ""))
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

func TestTargetMatchesHCLRelative(t *testing.T) {
	target := HCL("detector/atte.hcl", "test", "go", 0)
	match := func(input, relative string, want bool) {
		t.Helper()
		must.EqOp(t, want, target.Matches(input, relative))
	}

	match("../atte.hcl#test.go", "detector/attego", true)
	match("atte.hcl#test.go", "detector", true)
	match("../atte.hcl#test.go", "detector", false)
	match("..#test.go", "cmd", false)
	match("detector/atte.hcl#test.go", "", true)
	match("detector#test.go", "", true)
	match("../../atte.hcl#test.go", "detector/attego", false)
	match("../outside#test.go", "detector", false)
	match("detectorx#test.go", "", false)
}

func TestTargetMatchesGoTest(t *testing.T) {
	target := GoTest("detector/attego")
	must.True(t, target.Matches("attego#go_test", "detector"), must.Sprint("relative package selector should match from the current directory"))
	must.True(t, target.Matches("go_test", "detector/attego"), must.Sprint("bare go_test alias should match from the package directory"))
	must.False(t, target.Matches("attego#go_test", "detector/attegox"), must.Sprint("a sibling package sharing a string prefix must not match a path-qualified selector"))
}

func TestSelectorNoHCLFile(t *testing.T) {
	f := func(in, want string) {
		t.Helper()

		s, err := Parse(in)
		must.NoError(t, err)

		test.Eq(t, s.Tree().String(), want)
	}

	f("atte.hcl#test", "")
	f("//atte.hcl#test", "")

	f("subdir#go_test", "subdir")
	f("//subdir#go_test", "subdir")

	f("subdir/atte.hcl#test", "subdir")
	f("//subdir/atte.hcl#test", "subdir")
}
