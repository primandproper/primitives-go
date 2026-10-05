package filtering

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"time"
	"weak"

	"github.com/primandproper/primitives-go/v2/observability/logging"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Observe normalizes a filter and attaches it to the span and logger, once.
// It is the first line of every list method:
//
//	filter, logger = filtering.Observe(ctx, logger, filter)
//
// A nil filter is the default filter, built fresh on every call. A shared
// package-level default would be a filter one request's SetCursor leaks into
// the next, so the nil check is here rather than in each caller, where the one
// that forgets it dereferences nil on the first unfiltered request.
//
// The span is the one on ctx, read with trace.SpanFromContext, so a caller with
// no span gets the logger half and nothing else. Both sinks get
// ObservabilityValues, the same map Operation.SetValues records, which makes
// what reaches the log and what reaches the span one decision rather than two:
// a field that is withheld from one is withheld from the other, and a span and
// a log line cannot disagree about what was asked for.
//
// It is idempotent per sink rather than per call. A request's filter is handed
// down through a handler, a manager and a store, and each of them calls this
// because none can assume the one above did. A span this filter has already
// been attached to gets nothing a second time, and a logger that Observe
// returned for this filter is handed back as it is, so the layer passing its
// logger down does not have every field written onto it again. A layer with its
// own span or its own logger still gets the filter on it — that is a sink that
// has not seen it, and a span without the filter is a span that has to be
// cross-referenced to be read. If the filter is changed between calls, what was
// attached no longer describes it, and the next call attaches again.
//
// "Normalizes" means the nil default and no more. Normalize is not called: it
// reports an unrecognized sort direction, and there is no error here to report
// it through — a decoder that wants the filter checked calls Normalize where it
// can answer the caller. The page size the span records is therefore the one
// asked for, and ToSQLArgs is what applies the clamp.
func Observe(ctx context.Context, logger logging.Logger, filter *QueryFilter) (*QueryFilter, logging.Logger) {
	if filter == nil {
		filter = DefaultQueryFilter()
	}

	logger = logging.EnsureLogger(logger)
	values := filter.ObservabilityValues()

	obs := observations.get(filter)

	obs.mu.Lock()
	defer obs.mu.Unlock()

	if !maps.Equal(obs.values, values) {
		obs.values = values
		obs.spans = nil
		obs.loggers = nil
	}

	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		sc := span.SpanContext()
		if !slices.ContainsFunc(obs.spans, sc.Equal) {
			span.SetAttributes(spanAttributes(values)...)
			obs.spans = append(obs.spans, sc)
		}
	}

	if slices.ContainsFunc(obs.loggers, func(l logging.Logger) bool { return sameLogger(l, logger) }) {
		return filter, logger
	}

	logger = logger.WithValues(values)
	obs.loggers = append(obs.loggers, logger)

	return filter, logger
}

// observation is what Observe has attached a filter to: the values it attached,
// the spans it attached them to, and the loggers it returned carrying them.
type observation struct {
	values  map[string]any
	spans   []trace.SpanContext
	loggers []logging.Logger
	mu      sync.Mutex
}

// observationRegistry holds the marker Observe keeps per filter.
//
// The marker is held beside the filter rather than in it. A field on
// QueryFilter would be copied with the struct — and a filter copied and then
// changed would carry a marker that describes the original — and a lock in one
// would make every copy of a filter a vet warning in a consumer's code. Keyed
// weakly, the marker lives exactly as long as the filter it describes: a
// cleanup removes it when the filter is collected, so a request's filter takes
// its marker with it.
type observationRegistry struct {
	m  map[weak.Pointer[QueryFilter]]*observation
	mu sync.Mutex
}

var observations = &observationRegistry{m: map[weak.Pointer[QueryFilter]]*observation{}}

func (r *observationRegistry) get(filter *QueryFilter) *observation {
	key := weak.Make(filter)

	r.mu.Lock()
	defer r.mu.Unlock()

	if obs, ok := r.m[key]; ok {
		return obs
	}

	obs := &observation{}
	r.m[key] = obs
	runtime.AddCleanup(filter, r.forget, key)

	return obs
}

func (r *observationRegistry) forget(key weak.Pointer[QueryFilter]) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.m, key)
}

// sameLogger compares two loggers without panicking on an implementation whose
// dynamic type is not comparable, which an interface comparison would. Such a
// logger is never the same as anything, and is attached to every time.
func sameLogger(a, b logging.Logger) bool {
	if reflect.TypeOf(a) != reflect.TypeOf(b) || !reflect.TypeOf(a).Comparable() {
		return false
	}

	return a == b
}

// spanAttributes renders ObservabilityValues as span attributes. It is the
// subset of tracing.AttachToSpan's conversion that a filter's value types
// need, restated because observability/tracing imports this package.
func spanAttributes(values map[string]any) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(values))

	for k, v := range values {
		switch x := v.(type) {
		case bool:
			attrs = append(attrs, attribute.Bool(k, x))
		case uint16:
			attrs = append(attrs, attribute.Int64(k, int64(x)))
		case string:
			attrs = append(attrs, attribute.String(k, x))
		case time.Time:
			attrs = append(attrs, attribute.String(k, x.Format(time.RFC3339Nano)))
		default:
			attrs = append(attrs, attribute.String(k, fmt.Sprintf("%+v", x)))
		}
	}

	return attrs
}
