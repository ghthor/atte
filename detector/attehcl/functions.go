package attehcl

import (
	"fmt"
	"strings"

	"github.com/ghthor/atte/detector/attehcltarget"

	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/go-cty-funcs/cidr"
	"github.com/hashicorp/go-cty-funcs/crypto"
	"github.com/hashicorp/go-cty-funcs/encoding"
	"github.com/hashicorp/go-cty-funcs/filesystem"
	"github.com/hashicorp/go-cty-funcs/uuid"
	"github.com/hashicorp/hcl/v2/ext/tryfunc"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	ctyyaml "github.com/zclconf/go-cty-yaml"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

func targetHCLFunction(file reference.Blob, declarations declarationIndex) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "path", Type: cty.String},
			{Name: "target", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			targetFile, err := targetFileFromPath(file, args[0].AsString())
			if err != nil {
				return cty.NilVal, err
			}
			kind, name, err := parseTargetName(args[1].AsString())
			if err != nil {
				return cty.NilVal, err
			}
			declaration, ok := declarations.byReference[targetReference{
				file: targetFile,
				kind: kind,
				name: name,
			}]
			if !ok {
				return cty.NilVal, fmt.Errorf("target %s.%s in %q was not declared", kind, name, targetFile)
			}
			return cty.StringVal(targetReferenceValue(declaration.File, declaration.Kind, declaration.Name)), nil
		},
	})
}

func targetFileFromPath(file reference.Blob, raw string) (reference.Blob, error) {
	path := raw
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return reference.ResolveBlobFromTree(file.Tree(), reference.SomePath(path+Filename))
}

func parseTargetName(raw string) (attehcltarget.Kind, string, error) {
	kind, name, ok := strings.Cut(raw, ".")
	if !ok || strings.Contains(name, ".") || !hclsyntax.ValidIdentifier(kind) || !hclsyntax.ValidIdentifier(name) {
		return "", "", fmt.Errorf("target reference %q must be a kind.name identifier", raw)
	}
	if isNumericName(name) {
		return "", "", fmt.Errorf("target reference %q must use a named target", raw)
	}
	return attehcltarget.Kind(kind), name, nil
}

// baseHCLFunctions returns the common HCL functions used by HashiCorp
// configuration languages. Filesystem functions resolve paths from the tree
// containing the HCL file.
func baseHCLFunctions(file reference.Blob) map[string]function.Function {
	baseDir := file.Tree().String()
	return map[string]function.Function{
		"abs":             stdlib.AbsoluteFunc,
		"base64decode":    encoding.Base64DecodeFunc,
		"base64encode":    encoding.Base64EncodeFunc,
		"bcrypt":          crypto.BcryptFunc,
		"can":             tryfunc.CanFunc,
		"ceil":            stdlib.CeilFunc,
		"chomp":           stdlib.ChompFunc,
		"chunklist":       stdlib.ChunklistFunc,
		"cidrhost":        cidr.HostFunc,
		"cidrnetmask":     cidr.NetmaskFunc,
		"cidrsubnet":      cidr.SubnetFunc,
		"cidrsubnets":     cidr.SubnetsFunc,
		"coalesce":        stdlib.CoalesceFunc,
		"coalescelist":    stdlib.CoalesceListFunc,
		"compact":         stdlib.CompactFunc,
		"concat":          stdlib.ConcatFunc,
		"contains":        stdlib.ContainsFunc,
		"convert":         typeexpr.ConvertFunc,
		"csvdecode":       stdlib.CSVDecodeFunc,
		"distinct":        stdlib.DistinctFunc,
		"element":         stdlib.ElementFunc,
		"flatten":         stdlib.FlattenFunc,
		"floor":           stdlib.FloorFunc,
		"format":          stdlib.FormatFunc,
		"formatdate":      stdlib.FormatDateFunc,
		"formatlist":      stdlib.FormatListFunc,
		"indent":          stdlib.IndentFunc,
		"index":           stdlib.IndexFunc,
		"join":            stdlib.JoinFunc,
		"jsondecode":      stdlib.JSONDecodeFunc,
		"jsonencode":      stdlib.JSONEncodeFunc,
		"keys":            stdlib.KeysFunc,
		"length":          stdlib.LengthFunc,
		"log":             stdlib.LogFunc,
		"lookup":          stdlib.LookupFunc,
		"lower":           stdlib.LowerFunc,
		"max":             stdlib.MaxFunc,
		"merge":           stdlib.MergeFunc,
		"min":             stdlib.MinFunc,
		"parseint":        stdlib.ParseIntFunc,
		"pow":             stdlib.PowFunc,
		"range":           stdlib.RangeFunc,
		"reverse":         stdlib.ReverseFunc,
		"replace":         stdlib.ReplaceFunc,
		"regex_replace":   stdlib.RegexReplaceFunc,
		"rsadecrypt":      crypto.RsaDecryptFunc,
		"setintersection": stdlib.SetIntersectionFunc,
		"setproduct":      stdlib.SetProductFunc,
		"setunion":        stdlib.SetUnionFunc,
		"sha256":          crypto.Sha256Func,
		"sha512":          crypto.Sha512Func,
		"signum":          stdlib.SignumFunc,
		"slice":           stdlib.SliceFunc,
		"sort":            stdlib.SortFunc,
		"split":           stdlib.SplitFunc,
		"strlen":          stdlib.StrlenFunc,
		"strrev":          stdlib.ReverseFunc,
		"substr":          stdlib.SubstrFunc,
		"timeadd":         stdlib.TimeAddFunc,
		"title":           stdlib.TitleFunc,
		"trim":            stdlib.TrimFunc,
		"trimprefix":      stdlib.TrimPrefixFunc,
		"trimspace":       stdlib.TrimSpaceFunc,
		"trimsuffix":      stdlib.TrimSuffixFunc,
		"try":             tryfunc.TryFunc,
		"upper":           stdlib.UpperFunc,
		"urlencode":       encoding.URLEncodeFunc,
		"uuidv4":          uuid.V4Func,
		"uuidv5":          uuid.V5Func,
		"values":          stdlib.ValuesFunc,
		"yamldecode":      ctyyaml.YAMLDecodeFunc,
		"yamlencode":      ctyyaml.YAMLEncodeFunc,
		"zipmap":          stdlib.ZipmapFunc,

		"abspath":    filesystem.AbsPathFunc,
		"basename":   filesystem.BasenameFunc,
		"dirname":    filesystem.DirnameFunc,
		"file":       filesystem.MakeFileFunc(baseDir, false),
		"filebase64": filesystem.MakeFileFunc(baseDir, true),
		"fileexists": filesystem.MakeFileExistsFunc(baseDir),
		"fileset":    filesystem.MakeFileSetFunc(baseDir),
		"pathexpand": filesystem.PathExpandFunc,
		"md5":        crypto.Md5Func,
		"sha1":       crypto.Sha1Func,
	}
}
