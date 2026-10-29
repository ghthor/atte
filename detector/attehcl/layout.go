package attehcl

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attehcl/hclblock"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// LayoutBlock is the name of the repository-wide setting block that configures
// a Layout. It is only valid in the repository root's RootFilename file.
const LayoutBlock = "atte"

const layoutFilenameAttribute = "filename"

// hclExtension is the extension a configured configuration filename must have.
const hclExtension = ".hcl"

// Layout describes where a repository keeps its HCL configuration files.
//
// Every directory has at most one configuration file. The repository root's
// file is always named RootFilename so it can be found without any
// configuration; it may declare an atte block that renames the file used by
// every other directory.
type Layout struct {
	// Filename names the configuration file in every directory except the
	// repository root.
	Filename string
}

// DefaultLayout is the Layout of a repository whose root file declares no atte
// block: RootFilename in every directory.
func DefaultLayout() Layout {
	return Layout{Filename: RootFilename}
}

// File returns the configuration file for dir.
func (layout Layout) File(dir reference.Tree) reference.Blob {
	name := layout.Filename
	if dir == reference.Root {
		name = RootFilename
	}
	return reference.Blob(path.Join(dir.String(), name))
}

// Filenames returns the distinct configuration filenames the layout uses, root
// filename first.
func (layout Layout) Filenames() []string {
	if layout.Filename == RootFilename {
		return []string{RootFilename}
	}
	return []string{RootFilename, layout.Filename}
}

// mayBeConfigFile reports whether a file with this base name is, or is
// misplaced as, a configuration file.
func (layout Layout) mayBeConfigFile(base string) bool {
	return base == RootFilename || base == layout.Filename
}

// owns reports whether file is a configuration file of the layout. A file
// named like a configuration file of a different kind of directory (a
// non-root RootFilename, or a root file with the configured name) is an error,
// since silently ignoring it would hide declarations its author expects to
// take effect.
func (layout Layout) owns(file reference.Blob) (bool, error) {
	if !layout.mayBeConfigFile(path.Base(file.String())) {
		return false, nil
	}
	if want := layout.File(file.Tree()); file != want {
		return false, fmt.Errorf(
			"HCL file %q is not a configuration file: the %s block in %s sets the configuration filename to %q, so this directory's configuration file is %q",
			file,
			LayoutBlock,
			RootFilename,
			layout.Filename,
			want,
		)
	}
	return true, nil
}

// validate reports whether Filename can name a configuration file.
func (layout Layout) validate() error {
	name := layout.Filename
	switch {
	case name == "":
		return errors.New("filename must not be empty")
	case name == "." || name == ".." || strings.ContainsAny(name, `/\`):
		return fmt.Errorf("filename %q must be a file name without a directory", name)
	case path.Ext(name) != hclExtension || name == hclExtension:
		return fmt.Errorf("filename %q must end in %s", name, hclExtension)
	}
	return nil
}

var layoutSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: layoutFilenameAttribute, Required: true}},
}

// layoutSettingBlock is the built-in atte setting block. It is attached to every
// evaluation by attehcl itself, so Sensors cannot provide it.
func layoutSettingBlock() hclblock.SettingBlock {
	return hclblock.NewSettingBlock(&layoutSchema, decodeLayout, configLayout)
}

func decodeLayout(content *hcl.BodyContent, ctx *hcl.EvalContext) (Layout, error) {
	value, diagnostics := content.Attributes[layoutFilenameAttribute].Expr.Value(ctx)
	if diagnostics.HasErrors() {
		return Layout{}, targetDiagnosticsError{diagnostics: diagnostics}
	}
	if !value.IsWhollyKnown() || value.IsNull() || value.Type() != cty.String {
		return Layout{}, fmt.Errorf("%s must be a known string", layoutFilenameAttribute)
	}
	layout := Layout{Filename: value.AsString()}
	if err := layout.validate(); err != nil {
		return Layout{}, err
	}
	return layout, nil
}

func configLayout(layout Layout) (map[string]any, error) {
	return map[string]any{layoutFilenameAttribute: layout.Filename}, nil
}

// LoadLayout reads the repository root's RootFilename file and returns the
// Layout declared by its atte block, or DefaultLayout when the file or the
// block is absent. The block is decoded with an empty evaluation context, so
// its filename must be a literal.
func LoadLayout(ctx context.Context, repo *attegit.Repo) (Layout, error) {
	if repo == nil {
		return Layout{}, fmt.Errorf("repository is nil")
	}
	if err := checkContext(ctx); err != nil {
		return Layout{}, err
	}
	root := reference.Blob(RootFilename)
	if obj, ok := repo.Obj[root]; !ok || obj.Kind != attegit.Blob {
		return DefaultLayout(), nil
	}
	contents, err := repo.Show(root)
	if err != nil {
		return Layout{}, fmt.Errorf("read %q: %w", root, err)
	}
	body, err := parseFile(root, contents)
	if err != nil {
		return Layout{}, err
	}
	var declared *hclsyntax.Block
	for _, block := range body.Blocks {
		if block.Type != LayoutBlock {
			continue
		}
		if len(block.Labels) > 0 {
			return Layout{}, fmt.Errorf("parse HCL %q %s block at %s: setting blocks take no labels", root, block.Type, block.Range().String())
		}
		if declared != nil {
			return Layout{}, fmt.Errorf("parse HCL %q: duplicate %s block at %s", root, block.Type, block.Range().String())
		}
		declared = block
	}
	if declared == nil {
		return DefaultLayout(), nil
	}
	content, diagnostics := declared.Body.Content(&layoutSchema)
	if diagnostics.HasErrors() {
		return Layout{}, fmt.Errorf(
			"decode HCL %q %s block at %s: %w",
			root,
			LayoutBlock,
			declared.Range().String(),
			hclDiagnosticError(repo, root, diagnostics),
		)
	}
	layout, err := decodeLayout(content, &hcl.EvalContext{})
	if err != nil {
		if diagnostic, ok := errors.AsType[targetDiagnosticsError](err); ok {
			err = hclDiagnosticError(repo, root, diagnostic.diagnostics)
		}
		return Layout{}, fmt.Errorf("decode HCL %q %s block at %s: %w", root, LayoutBlock, declared.Range().String(), err)
	}
	return layout, nil
}
