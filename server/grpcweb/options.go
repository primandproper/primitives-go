package grpcweb

import (
	"github.com/primandproper/primitives-go/v2/observability/logging"
)

// Option configures the handler NewHandler builds. The zero configuration
// works: an absent logger logs nowhere.
type Option func(*options)

type options struct {
	logger logging.Logger
}

func newOptions(opts []Option) *options {
	o := &options{}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}

	return o
}

// WithLogger attaches a logger.
func WithLogger(logger logging.Logger) Option {
	return func(o *options) { o.logger = logger }
}
