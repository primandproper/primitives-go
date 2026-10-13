package zerolog

import (
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/primandproper/primitives-go/v2/observability/logging"

	"github.com/rs/zerolog"
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

func TestLogger_caller(T *testing.T) {
	T.Parallel()

	T.Run("names the caller of a logging.Helper", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		l := &Logger{logger: zerolog.New(&buf)}

		errorThroughHelper(l)
		_, file, line, _ := runtime.Caller(0)

		var record map[string]any
		must.NoError(t, json.Unmarshal(buf.Bytes(), &record))

		test.EqOp[any](t, strings.TrimPrefix(file, here)+", line "+strconv.Itoa(line-1), record[zerolog.CallerFieldName])
	})
}
