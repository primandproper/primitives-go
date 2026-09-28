package webauthn

import (
	"testing"

	"github.com/primandproper/primitives-go/v2/clock"
	loggingnoop "github.com/primandproper/primitives-go/v2/observability/logging/noop"
	metricsnoop "github.com/primandproper/primitives-go/v2/observability/metrics/noop"
	tracingnoop "github.com/primandproper/primitives-go/v2/observability/tracing/noop"

	"github.com/shoenig/test/must"
)

func TestNewOptions(T *testing.T) {
	T.Parallel()

	// Absent means noop, in all three pillars: a caller that wants none of them
	// names none of them. That such a relying party still runs a ceremony is
	// asserted beside the other ceremonies, in TestNewRelyingParty.
	T.Run("wants nothing", func(t *testing.T) {
		t.Parallel()

		o := newOptions(nil)

		must.Nil(t, o.logger)
		must.Nil(t, o.tracerProvider)
		must.Nil(t, o.metricsProvider)
	})

	T.Run("ignores a nil option", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{nil, WithLogger(loggingnoop.NewLogger())})
		must.NotNil(t, o.logger)
	})

	// The clock is the one option with a default rather than a noop, so a nil
	// value has to keep the wall clock rather than store one that panics the
	// first time a ceremony is saved.
	T.Run("keeps its default clock against a nil value", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{WithClock(nil)})
		must.NotNil(t, o.clock)
	})

	T.Run("takes what it is given", func(t *testing.T) {
		t.Parallel()

		o := newOptions([]Option{
			WithClock(clock.NewClock()),
			WithLogger(loggingnoop.NewLogger()),
			WithTracerProvider(tracingnoop.NewTracerProvider()),
			WithMetricsProvider(metricsnoop.NewMetricsProvider()),
		})

		must.NotNil(t, o.clock)
		must.NotNil(t, o.logger)
		must.NotNil(t, o.tracerProvider)
		must.NotNil(t, o.metricsProvider)
	})
}
