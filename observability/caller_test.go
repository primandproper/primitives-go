package observability

import (
	"errors"
	"runtime"
	"testing"

	"github.com/primandproper/primitives-go/v2/observability/logging"
	loggingnoop "github.com/primandproper/primitives-go/v2/observability/logging/noop"
	"github.com/primandproper/primitives-go/v2/observability/tracing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
)

// callSiteLogger records the call site of its last Error the way every backend
// resolves one: the frame above Error, passing over logging.Helper frames.
type callSiteLogger struct {
	logging.Logger
	frame runtime.Frame
}

func newCallSiteLogger() *callSiteLogger {
	return &callSiteLogger{Logger: loggingnoop.NewLogger()}
}

func (l *callSiteLogger) Error(string, error) {
	l.frame, _ = logging.CallerFrame(0)
}

func (l *callSiteLogger) WithSpan(trace.Span) logging.Logger { return l }

func TestErrorHelpers_callSite(T *testing.T) {
	T.Parallel()

	err := errors.New("blah")

	T.Run("PrepareAndLogError names its caller", func(t *testing.T) {
		t.Parallel()

		logger := newCallSiteLogger()
		_, span := tracing.StartSpan(t.Context())

		must.Error(t, PrepareAndLogError(err, logger, span, "doing the thing"))
		_, file, line, _ := runtime.Caller(0)

		test.EqOp(t, file, logger.frame.File)
		test.EqOp(t, line-1, logger.frame.Line)
	})

	T.Run("AcknowledgeError names its caller", func(t *testing.T) {
		t.Parallel()

		logger := newCallSiteLogger()
		_, span := tracing.StartSpan(t.Context())

		AcknowledgeError(err, logger, span, "doing the thing")
		_, file, line, _ := runtime.Caller(0)

		test.EqOp(t, file, logger.frame.File)
		test.EqOp(t, line-1, logger.frame.Line)
	})

	T.Run("PrepareAndLogGRPCStatus names its caller", func(t *testing.T) {
		t.Parallel()

		logger := newCallSiteLogger()
		_, span := tracing.StartSpan(t.Context())

		must.Error(t, PrepareAndLogGRPCStatus(err, logger, span, codes.Internal, "doing the thing"))
		_, file, line, _ := runtime.Caller(0)

		test.EqOp(t, file, logger.frame.File)
		test.EqOp(t, line-1, logger.frame.Line)
	})

	T.Run("Operation.Error names its caller", func(t *testing.T) {
		t.Parallel()

		logger := newCallSiteLogger()
		_, span := tracing.StartSpan(t.Context())
		op := newOperation(span, logger)

		must.Error(t, op.Error(err, "doing the thing"))
		_, file, line, _ := runtime.Caller(0)

		test.EqOp(t, file, logger.frame.File)
		test.EqOp(t, line-1, logger.frame.Line)
	})

	T.Run("Operation.Acknowledge names its caller", func(t *testing.T) {
		t.Parallel()

		logger := newCallSiteLogger()
		_, span := tracing.StartSpan(t.Context())
		op := newOperation(span, logger)

		op.Acknowledge(err, "doing the thing")
		_, file, line, _ := runtime.Caller(0)

		test.EqOp(t, file, logger.frame.File)
		test.EqOp(t, line-1, logger.frame.Line)
	})
}
