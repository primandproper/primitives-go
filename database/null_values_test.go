package database

import (
	"testing"
	"time"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestCoerceTime(T *testing.T) {
	T.Parallel()

	want := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)

	T.Run("passes a time.Time through", func(t *testing.T) {
		t.Parallel()

		got, ok := CoerceTime(want)
		must.True(t, ok)
		test.EqOp(t, want, got)
	})

	T.Run("parses every rendering the drivers produce", func(t *testing.T) {
		t.Parallel()

		for name, raw := range map[string]any{
			"go String()":       "2026-07-27 12:00:00 +0000 UTC",
			"RFC3339":           "2026-07-27T12:00:00Z",
			"space offset":      "2026-07-27 12:00:00+00:00",
			"naive fractional":  "2026-07-27 12:00:00.000000000",
			"naive second":      "2026-07-27 12:00:00",
			"byte slice":        []byte("2026-07-27 12:00:00 +0000 UTC"),
			"fractional string": "2026-07-27 12:00:00.000000000 +0000 UTC",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, ok := CoerceTime(raw)
				must.True(t, ok, must.Sprintf("case %q", name))
				test.True(t, want.Equal(got), test.Sprintf("case %q: got %s", name, got))
			})
		}
	})

	// A NULL is "no value" rather than the zero time: an empty backlog has no
	// oldest row, and reporting the zero time would show an age of 2,000 years.
	T.Run("reports absence for a NULL or unusable value", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []any{nil, "", "not a timestamp", 42, []byte("nope")} {
			_, ok := CoerceTime(raw)
			test.False(t, ok, test.Sprintf("value %v", raw))
		}
	})
}

func TestBlobOrNil(T *testing.T) {
	T.Parallel()

	// Nil and empty collapse deliberately: they say the same thing, and storing
	// two renderings would make the round trip depend on which call site wrote
	// the row.
	T.Run("an absent or empty encoding is NULL", func(t *testing.T) {
		t.Parallel()

		test.Nil(t, BlobOrNil(nil))
		test.Nil(t, BlobOrNil([]byte{}))
	})

	T.Run("a non-empty encoding is the bytes", func(t *testing.T) {
		t.Parallel()

		test.Eq(t, []byte(`{"a":"b"}`), BlobOrNil([]byte(`{"a":"b"}`)).([]byte))
	})
}

func TestCursorOrder(T *testing.T) {
	T.Parallel()

	// The halves have to agree and nothing checks that they do: a DESC page
	// keyed on "id > cursor" reads the wrong side of the boundary and skips
	// every row after the first page, with no error to show for it.
	T.Run("ascending pages forward", func(t *testing.T) {
		t.Parallel()

		direction, comparison := CursorOrder(false)

		test.EqOp(t, "ASC", direction)
		test.EqOp(t, " > ", comparison)
	})

	T.Run("descending pages backward", func(t *testing.T) {
		t.Parallel()

		direction, comparison := CursorOrder(true)

		test.EqOp(t, "DESC", direction)
		test.EqOp(t, " < ", comparison)
	})
}
