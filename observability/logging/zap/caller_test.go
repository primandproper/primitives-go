package zap

import (
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"testing"

	"github.com/primandproper/primitives-go/v2/observability/logging"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// errorThroughHelper logs on its caller's behalf, as
// observability.PrepareAndLogError does.
//
//go:noinline
func errorThroughHelper(l logging.Logger) {
	logging.Helper()
	l.Error("doing the thing", errors.New("blah"))
}

func TestLogger_caller(T *testing.T) {
	T.Parallel()

	T.Run("names the caller of a logging.Helper", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		encoderConfig := zap.NewProductionEncoderConfig()
		encoderConfig.EncodeCaller = zapcore.FullCallerEncoder
		encoderConfig.FunctionKey = "function"
		core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.AddSync(&buf), zapcore.DebugLevel)
		l := &Logger{logger: zap.New(core), atomicLevel: zap.NewAtomicLevel()}

		errorThroughHelper(l)
		_, file, line, _ := runtime.Caller(0)

		var record struct {
			Caller   string `json:"caller"`
			Function string `json:"function"`
		}
		must.NoError(t, json.Unmarshal(buf.Bytes(), &record))

		test.EqOp(t, zapcore.EntryCaller{Defined: true, File: file, Line: line - 1}.FullPath(), record.Caller)
		test.StrHasSuffix(t, ".TestLogger_caller.func1", record.Function)
	})
}
