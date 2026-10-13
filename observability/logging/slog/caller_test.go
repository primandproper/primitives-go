package slog

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime"
	"testing"

	"github.com/primandproper/primitives-go/v2/observability/logging"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// errorThroughHelper logs on its caller's behalf, as
// observability.PrepareAndLogError does.
//
//go:noinline
func errorThroughHelper(l logging.Logger) {
	logging.Helper()
	l.Error("doing the thing", errors.New("blah"))
}

func TestLogger_source(T *testing.T) {
	T.Parallel()

	T.Run("names the caller of a logging.Helper", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		l := &Logger{logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{AddSource: true}))}

		errorThroughHelper(l)
		_, file, line, _ := runtime.Caller(0)

		var record struct {
			Source slog.Source `json:"source"`
		}
		must.NoError(t, json.Unmarshal(buf.Bytes(), &record))

		test.StrHasSuffix(t, ".TestLogger_source.func1", record.Source.Function)
		test.EqOp(t, file, record.Source.File)
		test.EqOp(t, line-1, record.Source.Line)
	})
}
