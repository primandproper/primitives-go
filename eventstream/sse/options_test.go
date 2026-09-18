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
		test.False(t, o.reconnectDelaySet)
		test.EqOp(t, time.Duration(0), o.reconnectDelay)
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

func TestWithReconnectDelay(T *testing.T) {
	T.Parallel()

	T.Run("sets the delay and marks it set", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{WithReconnectDelay(15 * time.Second)})

		must.NotNil(t, o)
		test.EqOp(t, 15*time.Second, o.reconnectDelay)
		test.True(t, o.reconnectDelaySet)
	})

	// The load-bearing case. An explicit zero has to stay distinguishable from
	// never having named one, because the two get opposite answers: silence for
	// the caller who named nothing, an error for the caller who asked for a
	// reconnect delay of zero.
	T.Run("an explicit zero is recorded as set", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{WithReconnectDelay(0)})

		must.NotNil(t, o)
		test.EqOp(t, time.Duration(0), o.reconnectDelay)
		test.True(t, o.reconnectDelaySet)
	})

	T.Run("last option wins", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{WithReconnectDelay(time.Minute), WithReconnectDelay(time.Second)})

		must.NotNil(t, o)
		test.EqOp(t, time.Second, o.reconnectDelay)
	})
}

func TestOptions_validate(T *testing.T) {
	T.Parallel()

	T.Run("an unnamed delay is valid", func(t *testing.T) {
		t.Parallel()

		test.NoError(t, newOptions(nil).validate())
	})

	T.Run("the floor itself is valid", func(t *testing.T) {
		t.Parallel()

		test.NoError(t, newOptions([]Option{WithReconnectDelay(time.Millisecond)}).validate())
	})

	// There is no upper bound, and the largest duration there is still formats to
	// thirteen ASCII digits, which the wire format carries.
	T.Run("an enormous delay is valid", func(t *testing.T) {
		t.Parallel()

		test.NoError(t, newOptions([]Option{WithReconnectDelay(math.MaxInt64)}).validate())
	})

	T.Run("zero is refused", func(t *testing.T) {
		t.Parallel()

		test.ErrorIs(t, newOptions([]Option{WithReconnectDelay(0)}).validate(), ErrInvalidReconnectDelay)
	})

	T.Run("negative is refused", func(t *testing.T) {
		t.Parallel()

		test.ErrorIs(t, newOptions([]Option{WithReconnectDelay(-time.Second)}).validate(), ErrInvalidReconnectDelay)
	})

	// Sub-millisecond is the band that truncates to "retry: 0", which a client
	// honors by reconnecting as fast as it can.
	T.Run("sub-millisecond is refused", func(t *testing.T) {
		t.Parallel()

		test.ErrorIs(t, newOptions([]Option{WithReconnectDelay(500 * time.Microsecond)}).validate(), ErrInvalidReconnectDelay)
	})

	// The unit slip the floor exists to catch: 3000 written for "three seconds"
	// is three microseconds.
	T.Run("a bare integer meant as milliseconds is refused", func(t *testing.T) {
		t.Parallel()

		test.ErrorIs(t, newOptions([]Option{WithReconnectDelay(3000)}).validate(), ErrInvalidReconnectDelay)
	})
}
