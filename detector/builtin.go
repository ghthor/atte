package detector

import (
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
)

// NewDefaultBuilder returns a Builder containing Atte's built-in Sensors,
// target kinds, and HCL functions.
func NewDefaultBuilder() (*Builder, error) {
	builder := NewBuilder()

	if err := builder.AttachSensor(attegit.Detector{}); err != nil {
		return nil, err
	}
	if err := builder.AttachSensor(attego.Detector{}); err != nil {
		return nil, err
	}
	if err := builder.AttachSensor(attehcl.NewDetector(nil)); err != nil {
		return nil, err
	}
	return builder, nil
}
