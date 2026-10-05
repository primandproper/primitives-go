package filtering

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/primandproper/primitives-go/v2/observability/keys"
	"github.com/primandproper/primitives-go/v2/observability/logging"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// countingLogger is a recordingLogger that also counts how many times values
// were written onto it, which is what idempotence is about: recordingLogger
// merges, so a field written twice would look like a field written once.
type countingLogger struct {
	*recordingLogger
	writes *int
}

func newCountingLogger() *countingLogger {
	return &countingLogger{recordingLogger: newRecordingLogger(), writes: new(0)}
}

func (l *countingLogger) WithValues(v map[string]any) logging.Logger {
	*l.writes++

	rl, _ := l.recordingLogger.WithValues(v).(*recordingLogger)

	return &countingLogger{recordingLogger: rl, writes: l.writes}
}

// countingSpan counts SetAttributes calls on the span it wraps.
type countingSpan struct {
	trace.Span

	calls int
}

func (s *countingSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.calls++
	s.Span.SetAttributes(kv...)
}

// startSpan starts a recording span on a fresh provider and returns the context
// carrying it and the recorder it ends into.
func startSpan(t *testing.T, ctx context.Context) (context.Context, trace.Span, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { test.NoError(t, provider.Shutdown(context.Background())) })

	ctx, span := provider.Tracer(t.Name()).Start(ctx, t.Name())

	return ctx, span, recorder
}

func endedAttributes(t *testing.T, span trace.Span, recorder *tracetest.SpanRecorder) []attribute.KeyValue {
	t.Helper()

	span.End()

	ended := recorder.Ended()
	must.SliceLen(t, 1, ended)

	return ended[0].Attributes()
}

func attributeMap(attrs []attribute.KeyValue) map[string]attribute.Value {
	m := make(map[string]attribute.Value, len(attrs))
	for i := range attrs {
		m[string(attrs[i].Key)] = attrs[i].Value
	}

	return m
}

func TestObserve(T *testing.T) {
	T.Parallel()

	T.Run("attaches the same values to the span and the logger", func(t *testing.T) {
		t.Parallel()

		ctx, span, recorder := startSpan(t, t.Context())
		qf := fullyPopulatedQueryFilter(t)

		got, logger := Observe(ctx, newRecordingLogger(), qf)
		test.EqOp(t, qf, got)

		rl, ok := logger.(*recordingLogger)
		must.True(t, ok)
		test.MapEq(t, qf.ObservabilityValues(), rl.values)

		attrs := attributeMap(endedAttributes(t, span, recorder))
		test.MapLen(t, len(qf.ObservabilityValues()), attrs)
		test.EqOp(t, *qf.Cursor, attrs[keys.FilterCursorKey].AsString())
		test.EqOp(t, int64(*qf.MaxResponseSize), attrs[keys.FilterLimitKey].AsInt64())
		test.EqOp(t, *qf.SortBy, attrs[keys.FilterSortByKey].AsString())
		test.EqOp(t, qf.CreatedBefore.Format(time.RFC3339Nano), attrs[keys.FilterCreatedBeforeKey].AsString())
		test.EqOp(t, *qf.IncludeArchived, attrs[keys.FilterIncludeArchivedKey].AsBool())
	})

	T.Run("a nil filter is a fresh default filter", func(t *testing.T) {
		t.Parallel()

		first, _ := Observe(t.Context(), nil, nil)
		second, _ := Observe(t.Context(), nil, nil)

		must.NotNil(t, first)
		test.Eq(t, DefaultQueryFilter(), first)

		// Not one shared value: a cursor set on one request's filter must not
		// be the next request's starting point.
		test.NotEqOp(t, first, second)
		first.SetCursor(new("leaked"))
		test.Nil(t, second.Cursor)
	})

	T.Run("a nil filter is recorded as the default, not as nil", func(t *testing.T) {
		t.Parallel()

		_, logger := Observe(t.Context(), newRecordingLogger(), nil)

		rl, ok := logger.(*recordingLogger)
		must.True(t, ok)
		test.MapNotContainsKey(t, rl.values, keys.FilterIsNilKey)
		test.MapContainsKey(t, rl.values, keys.FilterLimitKey)
	})

	T.Run("without a span gets the logger half", func(t *testing.T) {
		t.Parallel()

		qf := fullyPopulatedQueryFilter(t)

		_, logger := Observe(context.Background(), newRecordingLogger(), qf)

		rl, ok := logger.(*recordingLogger)
		must.True(t, ok)
		test.MapEq(t, qf.ObservabilityValues(), rl.values)
	})

	T.Run("with a nil logger", func(t *testing.T) {
		t.Parallel()

		_, logger := Observe(t.Context(), nil, nil)
		test.NotNil(t, logger)
	})

	T.Run("attaches nothing twice to the same span or the logger it returned", func(t *testing.T) {
		t.Parallel()

		ctx, span, _ := startSpan(t, t.Context())
		defer span.End()

		// The SDK overwrites an attribute set under a key it already holds, so
		// the ended span's attributes cannot tell one attach from three; the
		// calls are counted instead.
		counted := &countingSpan{Span: span}
		ctx = trace.ContextWithSpan(ctx, counted)

		qf := fullyPopulatedQueryFilter(t)
		base := newCountingLogger()

		// Handler, manager, store: each normalizes the filter it was handed,
		// and passes the logger it got back down.
		qf1, l1 := Observe(ctx, base, qf)
		qf2, l2 := Observe(ctx, l1, qf1)
		qf3, l3 := Observe(ctx, l2, qf2)

		test.EqOp(t, qf, qf3)
		test.EqOp(t, l1, l3)
		test.EqOp(t, 1, *base.writes)
		test.EqOp(t, 1, counted.calls)
	})

	T.Run("attaches to each layer's own span and logger", func(t *testing.T) {
		t.Parallel()

		ctx, outer, outerRecorder := startSpan(t, t.Context())
		qf := fullyPopulatedQueryFilter(t)

		qf, _ = Observe(ctx, newRecordingLogger(), qf)

		innerCtx, inner := outer.TracerProvider().Tracer(t.Name()).Start(ctx, "inner")
		storeLogger := newCountingLogger()

		_, logger := Observe(innerCtx, storeLogger, qf)
		test.EqOp(t, 1, *storeLogger.writes)
		test.NotEqOp(t, logging.Logger(storeLogger), logger)

		inner.End()
		outer.End()

		ended := outerRecorder.Ended()
		must.SliceLen(t, 2, ended)

		for _, s := range ended {
			test.MapLen(t, len(qf.ObservabilityValues()), attributeMap(s.Attributes()), test.Sprintf("span %q", s.Name()))
		}
	})

	T.Run("a filter changed since it was observed is attached again", func(t *testing.T) {
		t.Parallel()

		ctx, span, recorder := startSpan(t, t.Context())
		qf := fullyPopulatedQueryFilter(t)
		base := newCountingLogger()

		_, l1 := Observe(ctx, base, qf)
		qf.SetCursor(new("next"))
		_, l2 := Observe(ctx, l1, qf)

		test.EqOp(t, 2, *base.writes)

		rl, ok := l2.(*countingLogger)
		must.True(t, ok)
		test.EqOp(t, any("next"), rl.values[keys.FilterCursorKey])
		test.EqOp(t, "next", attributeMap(endedAttributes(t, span, recorder))[keys.FilterCursorKey].AsString())
	})

	T.Run("a copy of an observed filter is a filter of its own", func(t *testing.T) {
		t.Parallel()

		qf := fullyPopulatedQueryFilter(t)
		base := newCountingLogger()

		_, l1 := Observe(t.Context(), base, qf)

		copied := *qf
		_, l2 := Observe(t.Context(), l1, &copied)

		test.NotEqOp(t, l1, l2)
		test.EqOp(t, 2, *base.writes)
	})

	T.Run("is safe for concurrent use on one filter", func(t *testing.T) {
		t.Parallel()

		ctx, span, _ := startSpan(t, t.Context())
		defer span.End()

		qf := fullyPopulatedQueryFilter(t)

		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				Observe(ctx, newRecordingLogger(), qf)
			})
		}

		wg.Wait()
	})
}

//nolint:paralleltest // counts the registry, which every other Observe test writes to.
func TestObserve_ForgetsCollectedFilters(t *testing.T) {
	before := registeredObservations()

	for range 100 {
		Observe(t.Context(), nil, nil)
	}

	for range 10 {
		runtime.GC()

		if registeredObservations() <= before {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	test.LessEq(t, before, registeredObservations())
}

func registeredObservations() int {
	observations.mu.Lock()
	defer observations.mu.Unlock()

	return len(observations.m)
}

// A logger whose dynamic type is not comparable cannot be recognized as one
// Observe returned, and must be attached to rather than compared and panicked on.
type uncomparableLogger struct {
	logging.Logger

	_ []string
}

func (l uncomparableLogger) WithValues(map[string]any) logging.Logger { return l }

func TestObserve_UncomparableLogger(t *testing.T) {
	t.Parallel()

	qf := fullyPopulatedQueryFilter(t)
	logger := uncomparableLogger{Logger: newRecordingLogger()}

	_, l1 := Observe(t.Context(), logger, qf)
	_, l2 := Observe(t.Context(), l1, qf)

	test.NotNil(t, l2)
}
