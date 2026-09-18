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
	logger         logging.Logger
	tracerProvider tracing.Provider
	reconnectDelay ReconnectDelay
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

// minReconnectDelay is the shortest reconnect delay the wire format can carry.
// "retry:" is whole milliseconds, so anything under one truncates to "retry: 0"
// — which is well-formed, and which a client honors by reconnecting as fast as
// it can.
const minReconnectDelay = time.Millisecond

// ErrInvalidReconnectDelay indicates NewReconnectDelay was given a delay shorter
// than a millisecond, zero and negative included.
//
// It is an error rather than an omitted field because the two outcomes a caller
// most needs told apart — "I did not set one" and "the one I set was nonsense" —
// would otherwise be the same silence, and the nonsense one is the expensive
// half: a fleet reconnecting without pause is the load the field exists to
// prevent.
var ErrInvalidReconnectDelay = errors.New("sse: reconnect delay must be at least a millisecond")

// ReconnectDelay is a reconnection time the wire format can carry: at least a
// millisecond, which is the unit "retry:" is counted in.
//
// It is a type of its own rather than a time.Duration so that the one value this
// package refuses is refused where it is made, which is what lets NewUpgrader
// stay infallible — an Upgrader is assembled from options, and an option that
// has already been validated cannot fail to be applied.
//
// The zero ReconnectDelay is the absent one. No delay this package accepts is
// zero, so "never named one" and "named zero" — which a bare Duration renders as
// the same value, the first legal and the second the hot reconnect loop the
// field exists to prevent — are distinct here, and only the first is
// constructible.
type ReconnectDelay struct {
	d time.Duration
}

// NewReconnectDelay validates a reconnection time and returns the value
// WithReconnectDelay takes.
//
// The field carries whole milliseconds, so an accepted delay is truncated toward
// zero when it is written: 1500µs emits "retry: 1". Below a millisecond the
// truncation would reach "retry: 0", so that band is refused here instead — with
// it the unit slip that produces it, NewReconnectDelay(3000) written for "three
// seconds", which is three microseconds.
//
// There is no upper bound: a long delay is a coherent instruction to wait a long
// time, and which values are unreasonable is a judgment this package has no
// information to make.
func NewReconnectDelay(d time.Duration) (ReconnectDelay, error) {
	if d < minReconnectDelay {
		return ReconnectDelay{}, errors.Wrapf(ErrInvalidReconnectDelay, "reconnect delay %s", d)
	}

	return ReconnectDelay{d: d}, nil
}

// Duration returns the delay, and zero for the absent one.
func (r ReconnectDelay) Duration() time.Duration { return r.d }

// WithReconnectDelay sets the reconnection time every stream this Upgrader
// produces opens with: the SSE "retry:" field, which tells a client how long to
// wait before reconnecting after the connection drops.
//
// The delay comes from NewReconnectDelay, which is where one the wire format
// cannot carry is refused. Naming no delay — or the zero ReconnectDelay — emits
// no field and leaves the client on its own default, which the HTML spec puts at
// "an implementation-defined value, probably in the region of a few seconds":
// three seconds in Chromium and WebKit, five in Gecko. See the package
// documentation for what a client does with the value.
func WithReconnectDelay(d ReconnectDelay) Option {
	return func(o *options) { o.reconnectDelay = d }
}
