package attego

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/graph/graphtest"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// contentTokens commits files to a new repository and returns the content
// tokens of its Go graph.
func contentTokens(t *testing.T, files map[string]string) (map[graph.EntityID]string, error) {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, files)
	git.CommitAll(t, "init")
	repo := openTestRepo(t, git.Dir())
	g, err := Graph(t.Context(), repo)
	must.NoError(t, err)
	return Detector{}.ContentTokens(t.Context(), repo, g)
}

// goMod returns a go.mod for a module that requires and replaces as given, one
// directive per line.
func goMod(directives ...string) string {
	return "module example.com/root\n\ngo 1.24\n\n" + strings.Join(directives, "\n") + "\n"
}

// externalToken returns the token of an external package imported by a module
// with the given go.mod.
func externalToken(t *testing.T, mod, importPath string) (string, error) {
	t.Helper()
	tokens, err := contentTokens(t, map[string]string{
		"go.mod": mod,
		"p/p.go": fmt.Sprintf("package p\n\nimport _ %q\n", importPath),
	})
	return tokens[EntityID(PackageExternalKind, "", importPath)], err
}

func TestModulesAreParsedOncePerRepo(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, map[string]string{
		"go.mod":        goMod(),
		"p/p.go":        "package p\n",
		"nested/go.mod": "module example.com/nested\n\ngo 1.24\n",
	})
	git.CommitAll(t, "init")
	repo := openTestRepo(t, git.Dir())

	before, err := modules(t.Context(), repo)
	must.NoError(t, err)
	must.SliceLen(t, 2, before)
	test.EqOp(t, "nested", string(before[0].dir))

	// Building the graph and the tokens reads the same parsed modules.
	g, err := Graph(t.Context(), repo)
	must.NoError(t, err)
	_, err = Detector{}.ContentTokens(t.Context(), repo, g)
	must.NoError(t, err)
	after, err := modules(t.Context(), repo)
	must.NoError(t, err)

	must.True(t, before[0] == after[0] && before[1] == after[1], must.Sprint("go.mod files should be parsed once per repository"))
}

func TestModulesDoNotRememberCanceledReads(t *testing.T) {
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, map[string]string{"go.mod": goMod()})
	git.CommitAll(t, "init")
	repo := openTestRepo(t, git.Dir())

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := modules(canceled, repo)
	test.ErrorIs(t, err, context.Canceled)

	mods, err := modules(t.Context(), repo)
	must.NoError(t, err)
	test.SliceLen(t, 1, mods)
}

func TestContentTokensExternalPackages(t *testing.T) {
	token := func(name, mod, importPath, want string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			got, err := externalToken(t, mod, importPath)
			test.NoError(t, err)
			test.EqOp(t, want, got)
		})
	}

	token("required module", goMod("require github.com/foo/bar v1.2.0"),
		"github.com/foo/bar/x", "go-module:github.com/foo/bar@v1.2.0")
	token("module with a major version suffix", goMod("require github.com/hashicorp/hcl/v2 v2.24.0"),
		"github.com/hashicorp/hcl/v2/hclsyntax", "go-module:github.com/hashicorp/hcl/v2@v2.24.0")
	token("longest required prefix wins",
		goMod("require github.com/foo/bar v1.0.0", "require github.com/foo/bar/sub v1.5.0"),
		"github.com/foo/bar/sub/x", "go-module:github.com/foo/bar/sub@v1.5.0")
	token("versioned replace applies to its version",
		goMod("require github.com/foo/bar v1.2.0", "replace github.com/foo/bar v1.2.0 => github.com/fork/bar v1.2.1-patched"),
		"github.com/foo/bar/x", "go-module:github.com/fork/bar@v1.2.1-patched")
	token("unversioned replace applies to every version",
		goMod("require github.com/foo/bar v1.2.0", "replace github.com/foo/bar => github.com/fork/bar v1.2.1-patched"),
		"github.com/foo/bar/x", "go-module:github.com/fork/bar@v1.2.1-patched")
	token("versioned replace for another version is ignored",
		goMod("require github.com/foo/bar v1.2.0", "replace github.com/foo/bar v1.1.0 => github.com/fork/bar v1.1.1-patched"),
		"github.com/foo/bar/x", "go-module:github.com/foo/bar@v1.2.0")
	token("versioned replace beats unversioned replace",
		goMod(
			"require github.com/foo/bar v1.2.0",
			"replace github.com/foo/bar => github.com/fork/bar v1.0.0",
			"replace github.com/foo/bar v1.2.0 => github.com/fork/bar v1.2.1-patched",
		),
		"github.com/foo/bar/x", "go-module:github.com/fork/bar@v1.2.1-patched")
}

func TestContentTokensRejectUnresolvedExternalPackages(t *testing.T) {
	reject := func(name, mod, importPath, wantErr string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			_, err := externalToken(t, mod, importPath)
			test.ErrorContains(t, err, wantErr)
		})
	}

	reject("no require directive", goMod(), "github.com/foo/bar/x", "matches no require directive")
	reject("require for a different module", goMod("require github.com/foo/baz v1.0.0"), "github.com/foo/bar/x", "matches no require directive")
	reject("replaced by a path outside the repository",
		goMod("require github.com/foo/bar v1.2.0", "replace github.com/foo/bar => ../bar"),
		"github.com/foo/bar/x", "not a package in the repository")
}

func TestContentTokensGoVersion(t *testing.T) {
	version := func(name, mod, want string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			tokens, err := contentTokens(t, map[string]string{"go.mod": mod, "p/p.go": "package p\n"})
			must.NoError(t, err)
			test.EqOp(t, want, tokens[EntityID(GoVersionKind, "", "")])
		})
	}

	version("go directive", "module example.com/root\n\ngo 1.26.0\n", "go-version:go=1.26.0")
	version("toolchain directive", "module example.com/root\n\ngo 1.24.0\n\ntoolchain go1.24.3\n", "go-version:go=1.24.0,toolchain=go1.24.3")
	version("toolchain default is absent", "module example.com/root\n\ngo 1.24.0\n\ntoolchain default\n", "go-version:go=1.24.0")
	version("missing go directive", "module example.com/root\n", "go-version:go=1.16")
}

func TestContentTokensPerModule(t *testing.T) {
	tokens, err := contentTokens(t, map[string]string{
		"go.mod":        "module example.com/root\n\ngo 1.24\n\nrequire github.com/foo/bar v1.0.0\n",
		"a/a.go":        "package a\n\nimport _ \"github.com/foo/bar\"\n",
		"nested/go.mod": "module example.com/nested\n\ngo 1.25\n\nrequire github.com/foo/bar v2.0.0+incompatible\n",
		"nested/n/n.go": "package n\n\nimport _ \"github.com/foo/bar\"\n",
	})
	must.NoError(t, err)

	test.EqOp(t, "go-module:github.com/foo/bar@v1.0.0", tokens[EntityID(PackageExternalKind, "", "github.com/foo/bar")])
	test.EqOp(t, "go-module:github.com/foo/bar@v2.0.0+incompatible", tokens[EntityID(PackageExternalKind, "nested", "github.com/foo/bar")])
	test.EqOp(t, "go-version:go=1.24", tokens[EntityID(GoVersionKind, "", "")])
	test.EqOp(t, "go-version:go=1.25", tokens[EntityID(GoVersionKind, "nested", "")])
}

func TestContentTokensLeavePackagesEmpty(t *testing.T) {
	dir := newBasicFixture(t)
	repo := openTestRepo(t, dir)
	g, err := Graph(t.Context(), repo)
	must.NoError(t, err)

	tokens, err := Detector{}.ContentTokens(t.Context(), repo, g)
	must.NoError(t, err)

	for _, id := range g.EntityKeys {
		switch g.Entities[id].Kind {
		case PackageKind, PackageTestKind, PackageStdlibKind:
			test.MapNotContainsKey(t, tokens, id)
		}
	}
}

func TestGraphPackagesDependOnGoVersion(t *testing.T) {
	g := openTestGraph(t, newBasicFixture(t))

	goVersion := EntityID(GoVersionKind, "", "")
	test.EqOp(t, GoVersionKind, string(g.Entities[goVersion].Kind))
	for _, id := range []graph.EntityID{
		EntityID(PackageKind, "", "example.com/root/p"),
		EntityID(PackageTestKind, "", "example.com/root/p"),
		EntityID(PackageStdlibKind, "", "fmt"),
		EntityID(PackageExternalKind, "", "github.com/shoenig/test/must"),
	} {
		graphtest.MustHaveRelation(t, g, id, goVersion, GoVersionRelation)
	}
}

func TestDependencyRelations(t *testing.T) {
	test.SliceContainsAll(t,
		[]graph.RelationKind{ImportsRelation, SourceFileRelation, TestsRelation, GoVersionRelation},
		Detector{}.DependencyRelations(),
	)
}
