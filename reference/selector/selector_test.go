package selector

import (
	"testing"

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
