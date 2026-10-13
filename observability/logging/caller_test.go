package logging

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// recordCallSite stands in for a backend's emit method: it asks for the frame
// above itself, the way Error asks for whoever called Error.
//
//go:noinline
func recordCallSite() (runtime.Frame, bool) {
	return CallerFrame(0)
}

//go:noinline
func recordCallSitePC() uintptr {
	return CallerPC(0)
}

// unmarkedWrapper logs on its caller's behalf without saying so.
//
//go:noinline
func unmarkedWrapper() (runtime.Frame, bool) {
	return recordCallSite()
}

//go:noinline
func markedHelper() (runtime.Frame, bool) {
	Helper()
	return recordCallSite()
}

// inlinableHelper is small enough for the compiler to inline into its caller,
// which leaves its logical frame sharing a PC with that caller's. A call to
// Helper would put it over the inlining budget, so it is marked from outside,
// by name, the way Helper would mark it.
func inlinableHelper() (runtime.Frame, bool) {
	return recordCallSite()
}

//go:noinline
func helperCallingHelper() (runtime.Frame, bool) {
	Helper()
	return markedHelper()
}

//go:noinline
func markedHelperPC() uintptr {
	Helper()
	return recordCallSitePC()
}

func inlinableHelperPC() uintptr {
	return recordCallSitePC()
}

var _ = markAsHelper(inlinableHelper, inlinableHelperPC)

// markAsHelper does what Helper does for each of fns, from outside them. It
// runs at package initialization, before anything can resolve a call site
// through them.
func markAsHelper(fns ...any) struct{} {
	for _, fn := range fns {
		helpers.Store(runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name(), struct{}{})
	}

	return struct{}{}
}

// thisFunction names the function that calls it.
func thisFunction() string {
	pc, _, _, ok := runtime.Caller(1)
	if !ok {
		return ""
	}

	return runtime.FuncForPC(pc).Name()
}

// lineAbove reports the line before the one that calls it.
func lineAbove() int {
	_, _, line, ok := runtime.Caller(1)
	if !ok {
		return 0
	}

	return line - 1
}

func TestCallerFrame(T *testing.T) {
	T.Parallel()

	T.Run("names the caller of the emitting function", func(t *testing.T) {
		t.Parallel()

		frame, ok := recordCallSite()
		line := lineAbove()

		must.True(t, ok)
		test.EqOp(t, thisFunction(), frame.Function)
		test.EqOp(t, line, frame.Line)
	})

	T.Run("an unmarked wrapper is the call site", func(t *testing.T) {
		t.Parallel()

		frame, ok := unmarkedWrapper()

		must.True(t, ok)
		test.StrHasSuffix(t, ".unmarkedWrapper", frame.Function)
	})

	T.Run("passes over a marked helper", func(t *testing.T) {
		t.Parallel()

		frame, ok := markedHelper()
		line := lineAbove()

		must.True(t, ok)
		test.EqOp(t, thisFunction(), frame.Function)
		test.EqOp(t, line, frame.Line)
	})

	T.Run("passes over a marked helper the compiler inlined", func(t *testing.T) {
		t.Parallel()

		frame, ok := inlinableHelper()
		line := lineAbove()

		must.True(t, ok)
		test.EqOp(t, thisFunction(), frame.Function)
		test.EqOp(t, line, frame.Line)
	})

	T.Run("passes over helpers in a row", func(t *testing.T) {
		t.Parallel()

		frame, ok := helperCallingHelper()
		line := lineAbove()

		must.True(t, ok)
		test.EqOp(t, thisFunction(), frame.Function)
		test.EqOp(t, line, frame.Line)
	})

	T.Run("answers the same from the memo", func(t *testing.T) {
		t.Parallel()

		var lines [3]int
		for i := range lines {
			frame, ok := markedHelper()
			must.True(t, ok)
			lines[i] = frame.Line
		}

		test.EqOp(t, lines[0], lines[1])
		test.EqOp(t, lines[1], lines[2])
	})
}

func TestCallerPC(T *testing.T) {
	T.Parallel()

	// slog resolves a Record's PC with runtime.CallersFrames, taking the
	// innermost frame; these resolve it the same way.
	resolve := func(pc uintptr) runtime.Frame {
		frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
		return frame
	}

	T.Run("resolves to the caller of a marked helper", func(t *testing.T) {
		t.Parallel()

		pc := markedHelperPC()
		line := lineAbove()

		must.NotEq(t, 0, pc)
		frame := resolve(pc)
		test.EqOp(t, thisFunction(), frame.Function)
		test.EqOp(t, line, frame.Line)
	})

	T.Run("resolves to the caller of an inlined helper", func(t *testing.T) {
		t.Parallel()

		pc := inlinableHelperPC()
		line := lineAbove()

		must.NotEq(t, 0, pc)
		frame := resolve(pc)
		test.EqOp(t, thisFunction(), frame.Function)
		test.EqOp(t, line, frame.Line)
	})
}
