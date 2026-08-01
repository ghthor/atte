package reference

import (
	"testing"

	"github.com/shoenig/test/must"
)

func TestResolveFromBlobRelative(t *testing.T) {
	got, err := ResolveFromBlob("pkg/atte.hcl", "./script.sh")
	must.NoError(t, err)
	must.EqOp(t, Blob("pkg/script.sh"), got)
}

func TestResolveFromBlobParentRelative(t *testing.T) {
	got, err := ResolveFromBlob("pkg/sub/atte.hcl", "../script.sh")
	must.NoError(t, err)
	must.EqOp(t, Blob("pkg/script.sh"), got)
}

func TestResolveFromBlobRepoRoot(t *testing.T) {
	got, err := ResolveFromBlob("pkg/sub/atte.hcl", "//config.yaml")
	must.NoError(t, err)
	must.EqOp(t, Blob("config.yaml"), got)
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
	must.EqOp(t, Blob("pkg/script.sh"), got)
}

func TestResolveFromDirParentRelative(t *testing.T) {
	got, err := ResolveFromDir("pkg/sub", "../script.sh")
	must.NoError(t, err)
	must.EqOp(t, Blob("pkg/script.sh"), got)
}

func TestResolveFromDirRepoRoot(t *testing.T) {
	got, err := ResolveFromDir("pkg/sub", "//config.yaml")
	must.NoError(t, err)
	must.EqOp(t, Blob("config.yaml"), got)
}

func TestTreeParent(t *testing.T) {
	tests := map[string]struct {
		path Tree
		want Tree
	}{
		"nested":    {path: Tree("pkg/sub"), want: Tree("pkg")},
		"top-level": {path: Tree("pkg"), want: Tree("")},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			must.EqOp(t, tt.want, tt.path.Parent())
		})
	}
}

func TestResolveFromDirEscapingRepoRootIsRejected(t *testing.T) {
	_, err := ResolveFromDir(".", "../outside.sh")
	must.ErrorContains(t, err, "escapes repository root")
}

func TestMatchFromBlobLiteralReference(t *testing.T) {
	candidates := []Blob{"pkg/other.go", "pkg/script.sh", "pkg/sub/script.sh"}
	got, err := MatchFromBlob("pkg/atte.hcl", "./script.sh", candidates)
	must.NoError(t, err)
	must.Eq(t, []Blob{"pkg/script.sh"}, got)
}

func TestMatchFromBlobLiteralReferenceWithoutCandidateIsEmpty(t *testing.T) {
	candidates := []Blob{"pkg/other.go"}
	got, err := MatchFromBlob("pkg/atte.hcl", "./script.sh", candidates)
	must.NoError(t, err)
	must.SliceEmpty(t, got)
}

func TestMatchFromBlobSingleStarWithinDirectory(t *testing.T) {
	candidates := []Blob{"pkg/a.go", "pkg/b.go", "pkg/sub/c.go", "other/d.go"}
	got, err := MatchFromBlob("pkg/atte.hcl", "./*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []Blob{"pkg/a.go", "pkg/b.go"}, got)
}

func TestMatchFromBlobDoubleStarIsRecursive(t *testing.T) {
	candidates := []Blob{"pkg/a.go", "pkg/sub/b.go", "pkg/sub/deep/c.go", "other/d.go"}
	got, err := MatchFromBlob("pkg/atte.hcl", "./**/*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []Blob{"pkg/a.go", "pkg/sub/b.go", "pkg/sub/deep/c.go"}, got)
}

func TestMatchFromBlobRepoRootGlob(t *testing.T) {
	candidates := []Blob{"cmd/main.go", "pkg/a.go", "pkg/sub/b.go"}
	got, err := MatchFromBlob("pkg/atte.hcl", "//pkg/**/*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []Blob{"pkg/a.go", "pkg/sub/b.go"}, got)
}

func TestMatchFromBlobPreservesCandidateOrder(t *testing.T) {
	candidates := []Blob{"pkg/b.go", "pkg/a.go"}
	got, err := MatchFromBlob("pkg/atte.hcl", "./*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []Blob{"pkg/b.go", "pkg/a.go"}, got)
}

func TestMatchFromBlobInvalidReferenceIsRejected(t *testing.T) {
	_, err := MatchFromBlob("pkg/atte.hcl", "", []Blob{"pkg/a.go"})
	must.ErrorContains(t, err, "invalid repository reference")
}

func TestMatchFromBlobEscapingRepoRootIsRejected(t *testing.T) {
	_, err := MatchFromBlob("atte.hcl", "../*.go", []Blob{"a.go"})
	must.ErrorContains(t, err, "escapes repository root")
}

func TestMatchFromBlobBadPatternIsRejected(t *testing.T) {
	_, err := MatchFromBlob("pkg/atte.hcl", "./[.go", []Blob{"pkg/a.go"})
	must.ErrorContains(t, err, "invalid repository reference")
}

func TestMatchFromDirSingleStarWithinDirectory(t *testing.T) {
	candidates := []Blob{"pkg/a.go", "pkg/b.go", "pkg/sub/c.go", "other/d.go"}
	got, err := MatchFromDir("pkg", "./*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []Blob{"pkg/a.go", "pkg/b.go"}, got)
}

func TestMatchFromDirDoubleStarIsRecursive(t *testing.T) {
	candidates := []Blob{"pkg/a.go", "pkg/sub/b.go", "pkg/sub/deep/c.go", "other/d.go"}
	got, err := MatchFromDir("pkg", "./**/*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []Blob{"pkg/a.go", "pkg/sub/b.go", "pkg/sub/deep/c.go"}, got)
}

func TestMatchFromDirRepoRootGlob(t *testing.T) {
	candidates := []Blob{"cmd/main.go", "pkg/a.go", "pkg/sub/b.go"}
	got, err := MatchFromDir("pkg", "//pkg/**/*.go", candidates)
	must.NoError(t, err)
	must.Eq(t, []Blob{"pkg/a.go", "pkg/sub/b.go"}, got)
}
