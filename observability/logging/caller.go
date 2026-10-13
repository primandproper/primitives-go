package logging

import (
	"runtime"
	"sync"
)

var (
	// helpers holds the fully qualified name of every function that has called
	// Helper. A name, rather than an entry PC, because a helper the compiler
	// inlined has no entry of its own: its logical frame shares a PC with its
	// caller's, and the name is the only thing that tells them apart.
	helpers sync.Map // map[string]struct{}

	// helperSites memoizes the Helper call sites already recorded, so a helper
	// pays for resolving its own name once rather than on every call.
	helperSites sync.Map // map[uintptr]struct{}

	// callSites memoizes what each return PC on a logging stack resolves to.
	// The set of PCs that log is small and fixed, and a resolved frame is
	// stable: a helper marks itself before it can log, so a PC is never resolved
	// while a helper whose frame it carries is still unmarked.
	callSites sync.Map // map[uintptr]callSite
)

// callSite is what one return PC resolves to: the first logical frame at that
// PC that is not a helper, or, when every frame there is one, a note to keep
// walking.
type callSite struct {
	frame      runtime.Frame
	allHelpers bool
}

// Helper marks the function that calls it as a logging helper: a function that
// logs on its caller's behalf, like observability.PrepareAndLogError. A backend
// attributing a record to a call site passes over helpers, so the record names
// the code that called the helper rather than the line inside it that called
// the Logger.
//
// It is testing.T.Helper's contract, for the same reason. Call it before
// anything that might log, since the marking is what the call does; after an
// early return that logs nothing is fine, and keeps the cost off a path that
// has nothing to report. A helper that calls a helper may mark both; the walk
// passes over as many as it finds in a row.
//
// The marking is process-wide and permanent. A function is a helper by what it
// is, not by which Logger it was handed.
func Helper() {
	var pcs [1]uintptr
	// Skip [runtime.Callers, Helper] to land on the function being marked.
	if runtime.Callers(2, pcs[:]) < 1 {
		return
	}

	if _, seen := helperSites.Load(pcs[0]); seen {
		return
	}

	frame, _ := runtime.CallersFrames(pcs[:]).Next()
	helpers.Store(frame.Function, struct{}{})
	helperSites.Store(pcs[0], struct{}{})
}

// CallerFrame reports the call site skip frames above the function calling
// CallerFrame, passing over every frame Helper marked. A skip of 0 names the
// caller of CallerFrame's caller — from inside a Logger's Error method, the code
// that called Error.
//
// The bool is false when the stack holds nothing but helpers past skip, which a
// backend should treat as an unknown call site.
func CallerFrame(skip int) (runtime.Frame, bool) {
	// Skip [runtime.Callers, callerSite, CallerFrame, CallerFrame's caller].
	return callerSite(skip + 4)
}

// CallerPC is CallerFrame's answer as a program counter, the form log/slog's
// Record takes. It is zero when CallerFrame's bool would be false, which slog
// reads as no source.
//
// The PC is the frame's own plus one, because slog resolves a Record's PC the
// way runtime.CallersFrames resolves a return address — by looking up the
// instruction before it. Handing it the frame's PC unadjusted resolves the line
// above, and, where the frame was inlined into its caller, the wrong function.
func CallerPC(skip int) uintptr {
	// Skip [runtime.Callers, callerSite, CallerPC, CallerPC's caller].
	frame, ok := callerSite(skip + 4)
	if !ok {
		return 0
	}

	return frame.PC + 1
}

// callerSite walks the stack from skip, a runtime.Callers skip count, to the
// first frame that is not a helper.
func callerSite(skip int) (runtime.Frame, bool) {
	// A handful of frames covers the deepest helper chain in this module with
	// room to spare; a deeper one costs another runtime.Callers rather than
	// a wrong answer.
	var pcs [8]uintptr

	for {
		n := runtime.Callers(skip, pcs[:])
		for _, pc := range pcs[:n] {
			if site := resolveCallSite(pc); !site.allHelpers {
				return site.frame, true
			}
		}

		if n < len(pcs) {
			return runtime.Frame{}, false
		}

		skip += n
	}
}

// resolveCallSite expands one return PC into its logical frames, innermost
// first, and keeps the first that is not a helper.
func resolveCallSite(pc uintptr) callSite {
	if cached, ok := callSites.Load(pc); ok {
		if site, isSite := cached.(callSite); isSite {
			return site
		}
	}

	site := callSite{allHelpers: true}
	frames := runtime.CallersFrames([]uintptr{pc})
	for {
		frame, more := frames.Next()
		if _, isHelper := helpers.Load(frame.Function); !isHelper {
			site = callSite{frame: frame}
			break
		}

		if !more {
			break
		}
	}

	callSites.Store(pc, site)

	return site
}
