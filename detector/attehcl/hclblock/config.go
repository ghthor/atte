package hclblock

import "github.com/hashicorp/hcl/v2"

// ConfigBlock describes an HCL configuration block schema and its decoder.
type ConfigBlock struct {
	// Schema describes the configuration block body.
	Schema *hcl.BodySchema
	// Decoder converts the schema-validated block body into a value.
	Decoder func(*hcl.BodyContent, *hcl.EvalContext) (any, error)
}
