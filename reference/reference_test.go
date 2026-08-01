package reference

import (
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/shoenig/test/must"
)

func TestResolveFromBlobRelative(t *testing.T) {
	got, err := ResolveFromBlob("pkg/atte.hcl", "./script.sh")
	must.NoError(t, err)
	must.EqOp(t, attegit.Path("pkg/script.sh"), got)
}

func TestResolveFromBlobParentRelative(t *testing.T) {
	got, err := ResolveFromBlob("pkg/sub/atte.hcl", "../script.sh")
	must.NoError(t, err)
	must.EqOp(t, attegit.Path("pkg/script.sh"), got)
}

func TestResolveFromBlobRepoRoot(t *testing.T) {
	got, err := ResolveFromBlob("pkg/sub/atte.hcl", "//config.yaml")
	must.NoError(t, err)
	must.EqOp(t, attegit.Path("config.yaml"), got)
}

func TestResolveFromBlobEmptyIsInvalid(t *testing.T) {
	_, err := ResolveFromBlob("pkg/atte.hcl", "")
	must.ErrorContains(t, err, "invalid repository reference")
}

func TestResolveFromBlobSingleLeadingSlashIsInvalid(t *testing.T) {
	_, err := ResolveFromBlob("pkg/atte.hcl", "/config.yaml")
	must.ErrorContains(t, err, "invalid repository reference")
}

func TestResolveFromBlobEscapingRepoRootIsRejected(t *testing.T) {
	_, err := ResolveFromBlob("atte.hcl", "../outside.sh")
	must.ErrorContains(t, err, "escapes repository root")
}

func TestResolveFromBlobEscapingRepoRootExactlyIsRejected(t *testing.T) {
	_, err := ResolveFromBlob("pkg/atte.hcl", "..")
	must.ErrorContains(t, err, "escapes repository root")
}

func TestResolveFromDirRelative(t *testing.T) {
	got, err := ResolveFromDir("pkg", "./script.sh")
	must.NoError(t, err)
	must.EqOp(t, attegit.Path("pkg/script.sh"), got)
}

func TestResolveFromDirParentRelative(t *testing.T) {
	got, err := ResolveFromDir("pkg/sub", "../script.sh")
	must.NoError(t, err)
	must.EqOp(t, attegit.Path("pkg/script.sh"), got)
}

func TestResolveFromDirRepoRoot(t *testing.T) {
	got, err := ResolveFromDir("pkg/sub", "//config.yaml")
	must.NoError(t, err)
	must.EqOp(t, attegit.Path("config.yaml"), got)
}

func TestResolveFromDirEscapingRepoRootIsRejected(t *testing.T) {
	_, err := ResolveFromDir(".", "../outside.sh")
	must.ErrorContains(t, err, "escapes repository root")
}

func TestMatchFromBlobLiteralReference(t *testing.T) {
	candidates := []attegit.Path{"pkg/other.go", "pkg/script.sh", "pkg/sub/script.sh"}
	got, err := MatchFromBlob("pkg/atte.hcl", "./script.sh", candidates)
	must.NoError(t, err)
	must.Eq(t, []attegit.Path{"pkg/script.sh"}, got)
}

func TestMatchFromBlobLiteralReferenceWithoutCandidateIsEmpty(t *testing.T) {
	candidates := []attegit.Path{"pkg/other.go"}
	got, err := MatchFromBlob("pkg/atte.hcl", "./script.sh", candidates)
	must.NoError(t, err)
	must.SliceEmpty(t, got)
}

func TestMatchFromBlobSingleStarWithinDirectory(t *testing.T) {
	candidates := []attegit.Path{"pkg/a.go", "pkg/b.go", "pkg/sub/c.go", "other/d.go"}
	got, err := MatchFromBlob("pkg/atte.hcl", "./*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []attegit.Path{"pkg/a.go", "pkg/b.go"}, got)
}

func TestMatchFromBlobDoubleStarIsRecursive(t *testing.T) {
	candidates := []attegit.Path{"pkg/a.go", "pkg/sub/b.go", "pkg/sub/deep/c.go", "other/d.go"}
	got, err := MatchFromBlob("pkg/atte.hcl", "./**/*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []attegit.Path{"pkg/a.go", "pkg/sub/b.go", "pkg/sub/deep/c.go"}, got)
}

func TestMatchFromBlobRepoRootGlob(t *testing.T) {
	candidates := []attegit.Path{"cmd/main.go", "pkg/a.go", "pkg/sub/b.go"}
	got, err := MatchFromBlob("pkg/atte.hcl", "//pkg/**/*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []attegit.Path{"pkg/a.go", "pkg/sub/b.go"}, got)
}

func TestMatchFromBlobPreservesCandidateOrder(t *testing.T) {
	candidates := []attegit.Path{"pkg/b.go", "pkg/a.go"}
	got, err := MatchFromBlob("pkg/atte.hcl", "./*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []attegit.Path{"pkg/b.go", "pkg/a.go"}, got)
}

func TestMatchFromBlobInvalidReferenceIsRejected(t *testing.T) {
	_, err := MatchFromBlob("pkg/atte.hcl", "", []attegit.Path{"pkg/a.go"})
	must.ErrorContains(t, err, "invalid repository reference")
}

func TestMatchFromBlobEscapingRepoRootIsRejected(t *testing.T) {
	_, err := MatchFromBlob("atte.hcl", "../*.go", []attegit.Path{"a.go"})
	must.ErrorContains(t, err, "escapes repository root")
}

func TestMatchFromBlobBadPatternIsRejected(t *testing.T) {
	_, err := MatchFromBlob("pkg/atte.hcl", "./[.go", []attegit.Path{"pkg/a.go"})
	must.ErrorContains(t, err, "invalid repository reference")
}

func TestMatchFromDirSingleStarWithinDirectory(t *testing.T) {
	candidates := []attegit.Path{"pkg/a.go", "pkg/b.go", "pkg/sub/c.go", "other/d.go"}
	got, err := MatchFromDir("pkg", "./*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []attegit.Path{"pkg/a.go", "pkg/b.go"}, got)
}

func TestMatchFromDirDoubleStarIsRecursive(t *testing.T) {
	candidates := []attegit.Path{"pkg/a.go", "pkg/sub/b.go", "pkg/sub/deep/c.go", "other/d.go"}
	got, err := MatchFromDir("pkg", "./**/*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []attegit.Path{"pkg/a.go", "pkg/sub/b.go", "pkg/sub/deep/c.go"}, got)
}

func TestMatchFromDirRepoRootGlob(t *testing.T) {
	candidates := []attegit.Path{"cmd/main.go", "pkg/a.go", "pkg/sub/b.go"}
	got, err := MatchFromDir("pkg", "//pkg/**/*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []attegit.Path{"pkg/a.go", "pkg/sub/b.go"}, got)
}
