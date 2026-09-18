package sse

import (
	"math"
	"testing"
	"time"

	loggingnoop "github.com/primandproper/primitives-go/v2/observability/logging/noop"
	tracingnoop "github.com/primandproper/primitives-go/v2/observability/tracing/noop"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestNewOptions(T *testing.T) {
	T.Parallel()

	T.Run("no options leaves every field unset", func(t *testing.T) {
		t.Parallel()

		o := newOptions(nil)

		must.NotNil(t, o)
		test.Nil(t, o.tracerProvider)
		test.EqOp(t, time.Duration(0), o.reconnectDelay.Duration())
	})

	T.Run("skips nil options", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{nil, WithTracerProvider(tracingnoop.NewTracerProvider()), nil})

		must.NotNil(t, o)
		test.NotNil(t, o.tracerProvider)
	})

	T.Run("applies every option", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{
			WithTracerProvider(tracingnoop.NewTracerProvider()),
		})

		must.NotNil(t, o)
		test.NotNil(t, o.tracerProvider)
	})
}

func TestWithTracerProvider(T *testing.T) {
	T.Parallel()

	T.Run("sets the tracer provider", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{WithTracerProvider(tracingnoop.NewTracerProvider())})

		must.NotNil(t, o)
		test.NotNil(t, o.tracerProvider)
	})

	T.Run("last option wins", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{WithTracerProvider(tracingnoop.NewTracerProvider()), WithTracerProvider(nil)})

		must.NotNil(t, o)
		test.Nil(t, o.tracerProvider)
	})
}

func TestWithLogger(T *testing.T) {
	T.Parallel()

	T.Run("sets the logger", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{WithLogger(loggingnoop.NewLogger())})

		must.NotNil(t, o)
		test.NotNil(t, o.logger)
	})

	T.Run("last option wins", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{WithLogger(loggingnoop.NewLogger()), WithLogger(nil)})

		must.NotNil(t, o)
		test.Nil(t, o.logger)
	})
}

func TestNewReconnectDelay(T *testing.T) {
	T.Parallel()

	T.Run("carries the delay it was given", func(t *testing.T) {
		t.Parallel()

		d, err := NewReconnectDelay(15 * time.Second)
		must.NoError(t, err)
		test.EqOp(t, 15*time.Second, d.Duration())
	})

	T.Run("the floor itself is accepted", func(t *testing.T) {
		t.Parallel()

		d, err := NewReconnectDelay(time.Millisecond)
		must.NoError(t, err)
		test.EqOp(t, time.Millisecond, d.Duration())
	})

	// There is no upper bound, and the largest duration there is still formats to
	// thirteen ASCII digits, which the wire format carries.
	T.Run("an enormous delay is accepted", func(t *testing.T) {
		t.Parallel()

		d, err := NewReconnectDelay(math.MaxInt64)
		must.NoError(t, err)
		test.EqOp(t, time.Duration(math.MaxInt64), d.Duration())
	})

	// The load-bearing case. Zero is the absent delay, so the caller who asks for
	// one gets an error rather than the silence the caller who asked for nothing
	// gets — which is the distinction a bare time.Duration cannot make, and the
	// reason this type exists.
	T.Run("zero is refused, and yields the absent delay", func(t *testing.T) {
		t.Parallel()

		d, err := NewReconnectDelay(0)
		test.ErrorIs(t, err, ErrInvalidReconnectDelay)
		test.EqOp(t, time.Duration(0), d.Duration())
	})

	T.Run("negative is refused", func(t *testing.T) {
		t.Parallel()

		_, err := NewReconnectDelay(-time.Second)
		test.ErrorIs(t, err, ErrInvalidReconnectDelay)
	})

	// Sub-millisecond is the band that truncates to "retry: 0", which a client
	// honors by reconnecting as fast as it can.
	T.Run("sub-millisecond is refused", func(t *testing.T) {
		t.Parallel()

		_, err := NewReconnectDelay(500 * time.Microsecond)
		test.ErrorIs(t, err, ErrInvalidReconnectDelay)
	})

	// The unit slip the floor exists to catch: 3000 written for "three seconds"
	// is three microseconds.
	T.Run("a bare integer meant as milliseconds is refused", func(t *testing.T) {
		t.Parallel()

		_, err := NewReconnectDelay(3000)
		test.ErrorIs(t, err, ErrInvalidReconnectDelay)
	})

	T.Run("names the delay it refused", func(t *testing.T) {
		t.Parallel()

		_, err := NewReconnectDelay(3000)
		must.Error(t, err)
		test.StrContains(t, err.Error(), "3µs")
	})
}

func TestWithReconnectDelay(T *testing.T) {
	T.Parallel()

	T.Run("sets the delay", func(t *testing.T) {
		t.Parallel()

		d := mustReconnectDelay(t, 15*time.Second)
		o := newOptions([]Option{WithReconnectDelay(d)})

		must.NotNil(t, o)
		test.EqOp(t, 15*time.Second, o.reconnectDelay.Duration())
	})

	// The zero value is constructible — it is what NewReconnectDelay returns
	// alongside its error — and naming it is naming nothing, which is the same
	// silence a caller who named no delay at all gets.
	T.Run("the zero delay is the absent one", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{WithReconnectDelay(ReconnectDelay{})})

		must.NotNil(t, o)
		test.EqOp(t, time.Duration(0), o.reconnectDelay.Duration())
	})

	T.Run("last option wins", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{
			WithReconnectDelay(mustReconnectDelay(t, time.Minute)),
			WithReconnectDelay(mustReconnectDelay(t, time.Second)),
		})

		must.NotNil(t, o)
		test.EqOp(t, time.Second, o.reconnectDelay.Duration())
	})
}

// mustReconnectDelay builds a delay the tests know is valid.
func mustReconnectDelay(t *testing.T, d time.Duration) ReconnectDelay {
	t.Helper()

	delay, err := NewReconnectDelay(d)
	must.NoError(t, err)

	return delay
}
