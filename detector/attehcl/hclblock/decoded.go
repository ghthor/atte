package hclblock

import (
	"fmt"
	"reflect"

	"github.com/hashicorp/hcl/v2"
)

// copyBodySchema returns a copy of schema that shares no slices with it.
func copyBodySchema(schema *hcl.BodySchema) *hcl.BodySchema {
	if schema == nil {
		return nil
	}
	return &hcl.BodySchema{
		Attributes: append([]hcl.AttributeSchema(nil), schema.Attributes...),
		Blocks:     append([]hcl.BlockHeaderSchema(nil), schema.Blocks...),
	}
}

// checkDecoded rejects decoder results that carry no value. what describes the
// decoder owner, such as a target kind or setting block name.
func checkDecoded(what string, decoded any) error {
	if decoded == nil {
		return fmt.Errorf("%s decoder returned nil", what)
	}
	value := reflect.ValueOf(decoded)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return fmt.Errorf("%s decoder returned a nil pointer", what)
	}
	return nil
}

// decodedAs asserts that decoded holds a T. what describes the value's owner
// in the error.
func decodedAs[T any](what string, decoded any) (T, error) {
	value, ok := decoded.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("%s has decoded value of type %T, want %s", what, decoded, reflect.TypeFor[T]())
	}
	return value, nil
}
