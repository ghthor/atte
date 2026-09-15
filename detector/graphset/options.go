// Package graphset contains shared detector graph configuration types.
package graphset

// Options contains the options shared by detector graph builders.
type Options struct {
	AttachToTree bool
}

// Option configures a detector graph builder.
type Option func(*Options)

// WithAttachToTree attaches detector entities to their repository tree and source files.
func WithAttachToTree() Option {
	return func(options *Options) { options.AttachToTree = true }
}
