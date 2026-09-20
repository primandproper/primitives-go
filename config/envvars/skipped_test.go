package envvars

import (
	"strings"
	"testing"

	"github.com/shoenig/test"
)

// TestWriteSkippedWarning covers the report a silently-narrowed run used to
// make, which was none.
//
// Dependencies bounds what is parsed. A module outside it is walked past, its
// config structs contribute nothing, and the run exits zero having written a
// smaller file that looks plausible — so the only signal was a constant count
// nobody has a baseline for. That is what these assertions are about: not that
// a warning exists, but that it names the module somebody forgot to allow.
func TestWriteSkippedWarning(T *testing.T) {
	T.Parallel()

	T.Run("names every module that was dropped", func(t *testing.T) {
		t.Parallel()

		var out strings.Builder

		writeSkippedWarning(&out, []string{
			"github.com/primandproper/primitives-go/v2",
			"github.com/example/other",
		})

		got := out.String()
		test.StrContains(t, got, "github.com/primandproper/primitives-go/v2")
		test.StrContains(t, got, "github.com/example/other")
		test.StrContains(t, got, "2 module(s)")
	})

	T.Run("sorted, so two runs of one configuration read the same", func(t *testing.T) {
		t.Parallel()

		var first, second strings.Builder

		writeSkippedWarning(&first, []string{"b/two", "a/one"})
		writeSkippedWarning(&second, []string{"a/one", "b/two"})

		test.EqOp(t, first.String(), second.String())
	})

	T.Run("says nothing when nothing was dropped", func(t *testing.T) {
		t.Parallel()

		var out strings.Builder

		writeSkippedWarning(&out, nil)

		test.EqOp(t, "", out.String())
	})
}
