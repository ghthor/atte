package hclblock

import (
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

type sampleValue struct {
	Name string
}

func decodeSample(*hcl.BodyContent, *hcl.EvalContext) (*sampleValue, error) {
	return &sampleValue{Name: "decoded"}, nil
}

func TestDecodedAsReturnsTypedSettingValue(t *testing.T) {
	block := NewSettingBlock(&hcl.BodySchema{}, decodeSample, nil)
	value, err := block.Decode("sample", &hcl.BodyContent{}, nil)
	must.NoError(t, err)

	got, err := DecodedAs[*sampleValue](value)
	must.NoError(t, err)
	test.EqOp(t, "decoded", got.Name)
}

func TestDecodedAsNamesExpectedAndActualTypes(t *testing.T) {
	block := NewSettingBlock(&hcl.BodySchema{}, decodeSample, nil)
	value, err := block.Decode("sample", &hcl.BodyContent{}, nil)
	must.NoError(t, err)
	value.File = reference.Blob("pkg/atte.hcl")

	_, err = DecodedAs[string](value)
	test.ErrorContains(t, err, "sample block in \"pkg/atte.hcl\"")
	test.ErrorContains(t, err, "*hclblock.sampleValue")
	test.ErrorContains(t, err, "want string")
}

func TestTargetDecodedAsNamesExpectedAndActualTypes(t *testing.T) {
	spec := NewTargetKindSpec(&hcl.BodySchema{}, decodeSample, TargetKindOptions[*sampleValue]{})
	target, err := spec.Decode("sample", &hcl.BodyContent{}, nil)
	must.NoError(t, err)

	got, err := TargetDecodedAs[*sampleValue](target)
	must.NoError(t, err)
	test.EqOp(t, "decoded", got.Name)

	_, err = TargetDecodedAs[int](target)
	test.ErrorContains(t, err, "*hclblock.sampleValue")
	test.ErrorContains(t, err, "want int")
}

func TestOptionalProjectionsMayBeNil(t *testing.T) {
	block := NewSettingBlock(&hcl.BodySchema{}, decodeSample, nil)
	value, err := block.Decode("sample", &hcl.BodyContent{}, nil)
	must.NoError(t, err)
	projected, err := value.Configuration()
	test.NoError(t, err)
	test.Nil(t, projected)

	spec := NewTargetKindSpec(&hcl.BodySchema{}, decodeSample, TargetKindOptions[*sampleValue]{})
	target, err := spec.Decode("sample", &hcl.BodyContent{}, nil)
	must.NoError(t, err)
	test.False(t, target.Runnable(), test.Sprint("target without an execution projection should not be runnable"))
	test.Nil(t, target.GraphProjection())
	resolved, err := target.ResolveScript(&attegit.Repo{})
	test.NoError(t, err)
	test.EqOp(t, reference.Blob(""), resolved.Script)
	_, err = target.Configuration()
	test.NoError(t, err)
}

func TestProjectionsReceiveTypedValue(t *testing.T) {
	block := NewSettingBlock(&hcl.BodySchema{}, decodeSample, func(value *sampleValue) (map[string]any, error) {
		return map[string]any{"name": value.Name}, nil
	})
	value, err := block.Decode("sample", &hcl.BodyContent{}, nil)
	must.NoError(t, err)
	projected, err := value.Configuration()
	test.NoError(t, err)
	test.Eq(t, map[string]any{"name": "decoded"}, projected)

	spec := NewTargetKindSpec(&hcl.BodySchema{}, decodeSample, TargetKindOptions[*sampleValue]{
		Execution: func(_ Target, value *sampleValue, root string) (TargetCommand, error) {
			return TargetCommand{Dir: root, Args: []string{"echo", value.Name}}, nil
		},
		Config: func(_ Target, value *sampleValue) (map[string]any, error) {
			return map[string]any{"value": value.Name}, nil
		},
	})
	target, err := spec.Decode("sample", &hcl.BodyContent{}, nil)
	must.NoError(t, err)
	command, err := target.Execution("/root")
	test.NoError(t, err)
	test.Eq(t, []string{"echo", "decoded"}, command.Args)
	computed, err := target.Configuration()
	test.NoError(t, err)
	test.Eq(t, map[string]any{"value": "decoded"}, computed.Meta)
}

func TestDecodeRejectsMissingValues(t *testing.T) {
	nilPointer := func(*hcl.BodyContent, *hcl.EvalContext) (*sampleValue, error) { return nil, nil }
	nilInterface := func(*hcl.BodyContent, *hcl.EvalContext) (any, error) { return nil, nil }

	_, err := NewSettingBlock(&hcl.BodySchema{}, nilPointer, nil).Decode("sample", &hcl.BodyContent{}, nil)
	test.ErrorContains(t, err, "decoder returned a nil pointer")
	_, err = NewSettingBlock(&hcl.BodySchema{}, nilInterface, nil).Decode("sample", &hcl.BodyContent{}, nil)
	test.ErrorContains(t, err, "decoder returned nil")

	_, err = NewTargetKindSpec(&hcl.BodySchema{}, nilPointer, TargetKindOptions[*sampleValue]{}).Decode("sample", &hcl.BodyContent{}, nil)
	test.ErrorContains(t, err, "decoder returned a nil pointer")
	_, err = NewTargetKindSpec(&hcl.BodySchema{}, nilInterface, TargetKindOptions[any]{}).Decode("sample", &hcl.BodyContent{}, nil)
	test.ErrorContains(t, err, "decoder returned nil")
}

func TestSchemasAreCopied(t *testing.T) {
	schema := hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "value"}}}
	block := NewSettingBlock(&schema, decodeSample, nil)
	spec := NewTargetKindSpec(&schema, decodeSample, TargetKindOptions[*sampleValue]{})
	schema.Attributes[0].Name = "changed"

	test.EqOp(t, "value", block.Schema().Attributes[0].Name)
	test.EqOp(t, "value", spec.Schema().Attributes[0].Name)
	block.Schema().Attributes[0].Name = "mutated"
	test.EqOp(t, "value", block.Schema().Attributes[0].Name)
}
