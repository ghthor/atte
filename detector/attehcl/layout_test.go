package attehcl

import (
	"strings"
	"testing"

	"github.com/ghthor/atte/detector/attehcl/hclblock"
	"github.com/ghthor/atte/detector/graph/graphtest"
	"github.com/ghthor/atte/reference"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

const buildHCLLayout = `
atte {
  filename = "build.hcl"
}
`

func TestLayoutFile(t *testing.T) {
	file := func(layout Layout, dir reference.Tree, want reference.Blob) {
		t.Helper()
		test.EqOp(t, want, layout.File(dir), test.Sprintf("layout=%+v dir=%q", layout, dir))
	}

	file(DefaultLayout(), reference.Root, "atte.hcl")
	file(DefaultLayout(), "sub", "sub/atte.hcl")
	file(Layout{Filename: "build.hcl"}, reference.Root, "atte.hcl")
	file(Layout{Filename: "build.hcl"}, "sub", "sub/build.hcl")
	file(Layout{Filename: "build.hcl"}, "a/b", "a/b/build.hcl")
}

func TestLayoutFilenames(t *testing.T) {
	test.SliceEqOp(t, []string{"atte.hcl"}, DefaultLayout().Filenames())
	test.SliceEqOp(t, []string{"atte.hcl", "build.hcl"}, Layout{Filename: "build.hcl"}.Filenames())
}

func TestLoadLayout(t *testing.T) {
	load := func(files map[string]string, want Layout) {
		t.Helper()
		layout, err := LoadLayout(t.Context(), newHCLFixture(t, files))
		test.NoError(t, err)
		test.EqOp(t, want, layout)
	}
	fail := func(files map[string]string, want string) {
		t.Helper()
		_, err := LoadLayout(t.Context(), newHCLFixture(t, files))
		test.ErrorContains(t, err, want)
	}

	t.Run("defaults", func(t *testing.T) {
		load(map[string]string{"README.md": "x"}, DefaultLayout())
		load(map[string]string{"atte.hcl": `test "a" { script = "a" }`}, DefaultLayout())
		load(map[string]string{"sub/atte.hcl": `test "a" { script = "a" }`}, DefaultLayout())
	})

	t.Run("declared", func(t *testing.T) {
		load(map[string]string{"atte.hcl": buildHCLLayout}, Layout{Filename: "build.hcl"})
		load(map[string]string{"atte.hcl": buildHCLLayout + `test "a" { script = "a" }`}, Layout{Filename: "build.hcl"})
		load(map[string]string{"atte.hcl": `atte { filename = "atte.hcl" }`}, DefaultLayout())
	})

	t.Run("block shape", func(t *testing.T) {
		fail(map[string]string{"atte.hcl": `atte "named" { filename = "build.hcl" }`}, "setting blocks take no labels")
		fail(map[string]string{"atte.hcl": "atte { filename = \"a.hcl\" }\natte { filename = \"b.hcl\" }"}, "duplicate atte block")
		fail(map[string]string{"atte.hcl": `atte {}`}, "decode HCL \"atte.hcl\" atte block")
		fail(map[string]string{"atte.hcl": `atte { unknown = true }`}, "decode HCL \"atte.hcl\" atte block")
	})

	t.Run("filename must be a literal", func(t *testing.T) {
		fail(map[string]string{"atte.hcl": "locals {\n  name = \"build.hcl\"\n}\natte {\n  filename = local.name\n}"}, "decode HCL \"atte.hcl\" atte block")
		fail(map[string]string{"atte.hcl": `atte { filename = upper("build.hcl") }`}, "decode HCL \"atte.hcl\" atte block")
		fail(map[string]string{"atte.hcl": `atte { filename = 1 }`}, "decode HCL \"atte.hcl\" atte block")
	})

	t.Run("filename validation", func(t *testing.T) {
		invalid := func(filename, want string) {
			t.Helper()
			fail(map[string]string{"atte.hcl": "atte {\n  filename = " + `"` + filename + `"` + "\n}"}, want)
		}

		invalid("", "filename must not be empty")
		invalid("dir/build.hcl", "without a directory")
		invalid("..", "without a directory")
		invalid(".", "without a directory")
		invalid("build.toml", "must end in .hcl")
		invalid("build", "must end in .hcl")
		invalid(".hcl", "must end in .hcl")
	})
}

func TestCustomFilenameDiscovery(t *testing.T) {
	targetFiles := func(files map[string]string) []reference.Blob {
		t.Helper()
		grouped, err := testTargets(t.Context(), newHCLFixture(t, files), nil)
		must.NoError(t, err)
		found := make([]reference.Blob, 0, 4)
		for _, target := range SortedTargets(grouped) {
			found = append(found, target.File)
		}
		return found
	}

	test.SliceEqOp(t, []reference.Blob{"atte.hcl", "sub/build.hcl", "sub/deeper/build.hcl"}, targetFiles(map[string]string{
		"atte.hcl":              buildHCLLayout + `test "root" { script = "r" }`,
		"sub/build.hcl":         `test "sub" { script = "s" }`,
		"sub/deeper/build.hcl":  `test "deeper" { script = "d" }`,
		"sub/other.hcl":         `test "other" { script = "o" }`,
		"sub/deeper/notes.txt":  "not configuration",
		"unrelated/build.hcl.0": "not configuration",
	}), test.Sprintf("configured filename should be discovered in every directory, including the root's atte.hcl"))

	test.SliceEqOp(t, []reference.Blob{"atte.hcl", "sub/atte.hcl"}, targetFiles(map[string]string{
		"atte.hcl":      `test "root" { script = "r" }`,
		"sub/atte.hcl":  `test "sub" { script = "s" }`,
		"sub/build.hcl": `not even hcl {{{`,
	}), test.Sprintf("files with other names are ignored without a configured filename"))
}

func TestCustomFilenameRejectsMisplacedConfigurationFiles(t *testing.T) {
	fail := func(files map[string]string, wants ...string) {
		t.Helper()
		_, err := testTargets(t.Context(), newHCLFixture(t, files), nil)
		for _, want := range wants {
			test.ErrorContains(t, err, want)
		}
	}

	fail(map[string]string{
		"atte.hcl":     buildHCLLayout,
		"sub/atte.hcl": `test "sub" { script = "s" }`,
	}, `"sub/atte.hcl"`, `"build.hcl"`)

	fail(map[string]string{
		"atte.hcl":      buildHCLLayout,
		"build.hcl":     `test "root" { script = "r" }`,
		"sub/build.hcl": `test "sub" { script = "s" }`,
	}, `"build.hcl"`, `"atte.hcl"`)
}

func TestLayoutBlockOnlyValidInRootFile(t *testing.T) {
	fail := func(files map[string]string, wants ...string) {
		t.Helper()
		_, err := testTargets(t.Context(), newHCLFixture(t, files), nil)
		for _, want := range wants {
			test.ErrorContains(t, err, want)
		}
	}

	fail(map[string]string{
		"atte.hcl":      buildHCLLayout,
		"sub/build.hcl": buildHCLLayout,
	}, `"sub/build.hcl"`, "only valid in the repository root atte.hcl")

	fail(map[string]string{
		"sub/atte.hcl": buildHCLLayout,
	}, `"sub/atte.hcl"`, "only valid in the repository root atte.hcl")
}

func TestTargetFunctionResolvesConfiguredFilename(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": strings.TrimLeft(`
atte {
  filename = "build.hcl"
}

test "root" {
  script = "r"
}

test "via_relative" {
  script     = "r"
  depends_on = [target("sub", "test.peer")]
}

test "via_rooted" {
  script     = "r"
  depends_on = [target("//sub", "test.peer")]
}

test "via_self" {
  script     = "r"
  depends_on = [target("", "test.root")]
}
`, "\n"),
		"sub/build.hcl": strings.TrimLeft(`
test "peer" {
  script = "p"
}

test "via_self" {
  script     = "p"
  depends_on = [target("", "test.peer")]
}

test "via_dot" {
  script     = "p"
  depends_on = [target(".", "test.peer")]
}

test "via_root" {
  script     = "p"
  depends_on = [target("//", "test.root")]
}

test "via_parent" {
  script     = "p"
  depends_on = [target("../", "test.root")]
}
`, "\n"),
	})

	got, err := Graph(t.Context(), repo, testCapabilities{})
	must.NoError(t, err)

	const (
		rootFile = "atte.hcl"
		subFile  = "sub/build.hcl"
	)
	depends := func(from reference.Blob, fromName string, to reference.Blob, toName string) {
		t.Helper()
		graphtest.MustHaveRelation(t, got, EntityID(TestKind, from, fromName), EntityID(TestKind, to, toName), DependsOnRelation)
	}

	depends(rootFile, "via_relative", subFile, "peer")
	depends(rootFile, "via_rooted", subFile, "peer")
	depends(rootFile, "via_self", rootFile, "root")
	depends(subFile, "via_self", subFile, "peer")
	depends(subFile, "via_dot", subFile, "peer")
	depends(subFile, "via_root", rootFile, "root")
	depends(subFile, "via_parent", rootFile, "root")
}

func TestTargetFunctionEmptyPathIsDeclaringFileWithDefaultLayout(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl": `test "root" { script = "r" }`,
		"sub/atte.hcl": strings.TrimLeft(`
test "peer" {
  script = "p"
}

test "via_self" {
  script     = "p"
  depends_on = [target("", "test.peer")]
}

test "via_root" {
  script     = "p"
  depends_on = [target("//", "test.root")]
}
`, "\n"),
	})

	got, err := Graph(t.Context(), repo, testCapabilities{})
	must.NoError(t, err)
	graphtest.MustHaveRelation(t, got, EntityID(TestKind, "sub/atte.hcl", "via_self"), EntityID(TestKind, "sub/atte.hcl", "peer"), DependsOnRelation)
	graphtest.MustHaveRelation(t, got, EntityID(TestKind, "sub/atte.hcl", "via_root"), EntityID(TestKind, "atte.hcl", "root"), DependsOnRelation)
}

func TestTargetFunctionRejectsEscapingPath(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"sub/atte.hcl": `test "a" {
  script = "a"
  depends_on = [target("../..", "test.root")]
}`,
	})
	_, err := testTargets(t.Context(), repo, nil)
	test.ErrorContains(t, err, "escapes repository root")
}

func TestConfigForUsesConfiguredFilename(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{
		"atte.hcl":      buildHCLLayout + `test "root" { script = "r" }`,
		"sub/build.hcl": `test "sub" { script = "s" }`,
	})

	t.Run("subdirectory", func(t *testing.T) {
		config, err := testConfigFor(t.Context(), repo, "sub", nil)
		must.NoError(t, err)
		targets := SortedTargets(config.Targets)
		must.Len(t, 1, targets)
		test.EqOp(t, reference.Blob("sub/build.hcl"), targets[0].File)
		test.Len(t, 0, config.Settings)
	})

	t.Run("root", func(t *testing.T) {
		config, err := testConfigFor(t.Context(), repo, "", nil)
		must.NoError(t, err)
		targets := SortedTargets(config.Targets)
		must.Len(t, 1, targets)
		test.EqOp(t, reference.Blob("atte.hcl"), targets[0].File)

		must.Len(t, 1, config.Settings)
		test.EqOp(t, LayoutBlock, config.Settings[0].Name)
		test.EqOp(t, reference.Blob("atte.hcl"), config.Settings[0].File)
		values, err := config.Settings[0].Configuration()
		must.NoError(t, err)
		test.Eq(t, map[string]any{"filename": "build.hcl"}, values)
	})

	t.Run("directory without a file", func(t *testing.T) {
		config, err := testConfigFor(t.Context(), repo, "missing", nil)
		must.NoError(t, err)
		test.Len(t, 0, SortedTargets(config.Targets))
	})
}

func TestConfigForDecodesLayoutValue(t *testing.T) {
	repo := newHCLFixture(t, map[string]string{"atte.hcl": buildHCLLayout})
	config, err := testConfigFor(t.Context(), repo, "", nil)
	must.NoError(t, err)
	must.Len(t, 1, config.Settings)
	layout, err := hclblock.DecodedAs[Layout](config.Settings[0])
	must.NoError(t, err)
	test.EqOp(t, Layout{Filename: "build.hcl"}, layout)
}
