package grpc

import (
	"errors"
	"runtime"
	"testing"

	"github.com/primandproper/primitives-go/v2/observability/logging"
	loggingnoop "github.com/primandproper/primitives-go/v2/observability/logging/noop"
	"github.com/primandproper/primitives-go/v2/observability/tracing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"google.golang.org/grpc/codes"
)

// callSiteLogger records the call site of its last Error the way every backend
// resolves one: the frame above Error, passing over logging.Helper frames.
type callSiteLogger struct {
	logging.Logger
	frame runtime.Frame
}

func (l *callSiteLogger) Error(string, error) {
	l.frame, _ = logging.CallerFrame(0)
}

func TestPrepareAndLogGRPCStatus_callSite(T *testing.T) {
	T.Parallel()

	T.Run("names its caller", func(t *testing.T) {
		t.Parallel()

		logger := &callSiteLogger{Logger: loggingnoop.NewLogger()}
		_, span := tracing.StartSpan(t.Context())

		must.Error(t, PrepareAndLogGRPCStatus(errors.New("blah"), logger, span, codes.Internal, "doing the thing"))
		_, file, line, _ := runtime.Caller(0)

		test.EqOp(t, file, logger.frame.File)
		test.EqOp(t, line-1, logger.frame.Line)
	})
}
