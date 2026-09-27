package scanner

import (
	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attego"
	"github.com/ghthor/atte/detector/attehcl"
)

// NewDefault returns a Builder containing Atte's built-in Sensors,
// target kinds, and HCL functions.
func NewDefault() (*detector.Builder, error) {
	builder := detector.NewBuilder()

	if err := builder.AttachSensor(attegit.Detector{}); err != nil {
		return nil, err
	}
	if err := builder.AttachSensor(attego.NewDetector()); err != nil {
		return nil, err
	}
	if err := builder.AttachSensor(attehcl.NewDetector()); err != nil {
		return nil, err
	}
	return builder, nil
}
