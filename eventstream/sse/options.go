package sse

import (
	"time"

	"github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability/logging"
	"github.com/primandproper/primitives-go/v2/observability/tracing"
)

// Option configures the Upgrader this package constructs. The zero
// configuration works: an absent logger logs nowhere, an absent tracer
// provider traces nowhere, and an absent reconnect delay emits no "retry:"
// field.
type Option func(*options)

type options struct {
	logger            logging.Logger
	tracerProvider    tracing.Provider
	reconnectDelay    time.Duration
	reconnectDelaySet bool
}

func newOptions(opts []Option) *options {
	cfg := &options{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}

	return cfg
}

// validate reports the option values NewUpgrader refuses. It is a method rather
// than a check inside newOptions because applying options and judging the
// result are different jobs, and only the constructor has somewhere to return
// an error to.
func (o *options) validate() error {
	// Tested on reconnectDelaySet rather than on the value, because the zero
	// duration means two different things: "no option was given", which is every
	// caller that predates the option and emits no field, and "zero was asked
	// for", which is a hot reconnect loop and is refused.
	if o.reconnectDelaySet && o.reconnectDelay < minReconnectDelay {
		return errors.Wrapf(ErrInvalidReconnectDelay, "reconnect delay %s", o.reconnectDelay)
	}

	return nil
}

// WithLogger attaches a logger, which every stream the Upgrader produces
// inherits — so a write that fails to reach a subscriber is reported somewhere
// other than in the error returned to a handler that has already sent its
// headers.
func WithLogger(logger logging.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// WithTracerProvider attaches a tracer provider.
func WithTracerProvider(tracerProvider tracing.Provider) Option {
	return func(o *options) { o.tracerProvider = tracerProvider }
}

// WithReconnectDelay sets the reconnection time every stream this Upgrader
// produces opens with: the SSE "retry:" field, which tells a client how long to
// wait before reconnecting after the connection drops.
//
// Naming no delay emits no field and leaves the client on its own default,
// which the HTML spec puts at "an implementation-defined value, probably in the
// region of a few seconds" — three seconds in Chromium and WebKit, five in
// Gecko. See the package documentation for what a client does with the value.
//
// The field carries whole milliseconds, so a delay is truncated toward zero and
// one under a millisecond is refused by NewUpgrader rather than emitted as
// "retry: 0". That floor is also what catches WithReconnectDelay(3000) written
// for "three seconds", which is three microseconds.
func WithReconnectDelay(d time.Duration) Option {
	return func(o *options) { o.reconnectDelay, o.reconnectDelaySet = d, true }
}
