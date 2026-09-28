package querygen

import (
	"testing"

	"github.com/primandproper/primitives-go/v2/database/dialect"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func everyLockMode() []LockMode {
	return []LockMode{LockNone, LockExclusive, LockShared, LockExclusiveSkipLocked}
}

// lockLines is the line each mode ends a statement with on each dialect, spelled
// out cell by cell rather than derived, so the table is the specification the
// renderings below are checked against. An empty cell renders no line at all.
var lockLines = map[dialect.Dialect]map[LockMode]string{
	dialect.Postgres: {
		LockNone:                "",
		LockExclusive:           "FOR UPDATE",
		LockShared:              "FOR SHARE",
		LockExclusiveSkipLocked: "FOR UPDATE SKIP LOCKED",
	},
	dialect.MySQL: {
		LockNone:                "",
		LockExclusive:           "FOR UPDATE",
		LockShared:              "FOR SHARE",
		LockExclusiveSkipLocked: "FOR UPDATE SKIP LOCKED",
	},
	// No row locks, so every mode is the unlocked statement.
	dialect.SQLite: {
		LockNone:                "",
		LockExclusive:           "",
		LockShared:              "",
		LockExclusiveSkipLocked: "",
	},
}

// withLock is the golden statement body with its lock line, terminated.
func withLock(body, lock string) string {
	if lock == "" {
		return body + ";"
	}

	return body + "\n" + lock + ";"
}

func Test_lockClause(T *testing.T) {
	T.Parallel()

	T.Run("no row locks renders nothing, whatever was asked", func(t *testing.T) {
		t.Parallel()

		for _, mode := range everyLockMode() {
			for _, skip := range []bool{false, true} {
				test.EqOp(t, "", lockClause(mode, false, skip), test.Sprintf("mode %s, skip %t", mode, skip))
			}
		}
	})

	T.Run("a dialect that locks and skips", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, "", lockClause(LockNone, true, true))
		test.EqOp(t, "\nFOR UPDATE", lockClause(LockExclusive, true, true))
		test.EqOp(t, "\nFOR SHARE", lockClause(LockShared, true, true))
		test.EqOp(t, "\nFOR UPDATE SKIP LOCKED", lockClause(LockExclusiveSkipLocked, true, true))
	})

	// No supported dialect is here yet. The rule is that the exclusion survives
	// and the skip does not: a claim that waits is slow, and one that takes no
	// lock is wrong.
	T.Run("a dialect that locks but cannot skip keeps the lock", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, "", lockClause(LockNone, true, false))
		test.EqOp(t, "\nFOR UPDATE", lockClause(LockExclusive, true, false))
		test.EqOp(t, "\nFOR SHARE", lockClause(LockShared, true, false))
		test.EqOp(t, "\nFOR UPDATE", lockClause(LockExclusiveSkipLocked, true, false))
	})

	// An unknown mode is refused even where nothing would render, so the misuse
	// surfaces in the SQLite build of a corpus as well as the other two.
	T.Run("an unknown mode panics on every dialect", func(t *testing.T) {
		t.Parallel()

		for _, d := range everyDialect() {
			err := recovered(func() { _ = For(d).lockClause(LockMode(99)) })

			must.Error(t, err, must.Sprintf("dialect %q", d))
			test.ErrorIs(t, err, ErrUnknownLockMode, test.Sprintf("dialect %q", d))
			test.StrContains(t, err.Error(), "99", test.Sprintf("dialect %q", d))
		}
	})
}

func TestLockMode_String(T *testing.T) {
	T.Parallel()

	T.Run("names every mode", func(t *testing.T) {
		t.Parallel()

		seen := map[string]bool{}

		for _, mode := range everyLockMode() {
			name := mode.String()
			test.StrNotContains(t, name, "unknown", test.Sprintf("mode %d", int(mode)))
			test.False(t, seen[name], test.Sprintf("mode %d shares a name", int(mode)))

			seen[name] = true
		}

		test.StrContains(t, LockMode(99).String(), "unknown")
	})
}

func TestGenerator_ReadQuery_Lock(T *testing.T) {
	T.Parallel()

	T.Run("every mode on every dialect", func(t *testing.T) {
		t.Parallel()

		const body = `SELECT
	tokens.id,
	tokens.secret,
	tokens.archived_at
FROM tokens
WHERE tokens.archived_at IS NULL
	AND tokens.id = sqlc.arg(id)`

		for _, d := range everyDialect() {
			for _, mode := range everyLockMode() {
				q := For(d).ReadQuery("GetTokenLocked", guardTable,
					[]string{IDColumn, "secret", ArchivedAtColumn}, Read{Lock: mode})

				test.EqOp(t, withLock(body, lockLines[d][mode]), q.Content,
					test.Sprintf("dialect %q, mode %s", d, mode))
			}
		}
	})

	// The lock is the last clause in a SELECT on both servers that take one,
	// so it goes after the LIMIT a picking read carries rather than before it.
	T.Run("follows the ordering and the limit", func(t *testing.T) {
		t.Parallel()

		const body = `SELECT
	tokens.id,
	tokens.secret
FROM tokens
WHERE tokens.id = sqlc.arg(id)
	AND tokens.secret = sqlc.arg(secret)
ORDER BY tokens.secret ASC
LIMIT 1`

		for _, d := range everyDialect() {
			for _, mode := range everyLockMode() {
				q := For(d).ReadQuery("GetTokenLocked", guardTable, []string{IDColumn, "secret"},
					Read{Order: "secret", Lock: mode}, Match{Column: "secret"})

				test.EqOp(t, withLock(body, lockLines[d][mode]), q.Content,
					test.Sprintf("dialect %q, mode %s", d, mode))
			}
		}
	})

	// Read is shared with the batched read, and a field that one statement
	// honored and the other ignored would be a lock a caller asked for and did
	// not get.
	T.Run("the batched read honors it too", func(t *testing.T) {
		t.Parallel()

		for _, d := range everyDialect() {
			for _, mode := range everyLockMode() {
				q := For(d).SetReadQuery("GetTokensLocked", guardTable, []string{IDColumn, "owner"},
					Read{Lock: mode}, SetKey{Column: "owner"})

				test.StrHasSuffix(t, "ORDER BY tokens.owner ASC"+withLock("", lockLines[d][mode]), q.Content,
					test.Sprintf("dialect %q, mode %s", d, mode))
			}
		}
	})

	T.Run("the zero value is the unlocked read", func(t *testing.T) {
		t.Parallel()

		for _, d := range everyDialect() {
			g := For(d)

			test.EqOp(t,
				g.GetQuery("GetToken", guardTable, guardColumns()).Content,
				g.ReadQuery("GetToken", guardTable, guardColumns(), Read{}).Content,
				test.Sprintf("dialect %q", d))
		}
	})
}

func TestGenerator_SweepQuery_Lock(T *testing.T) {
	T.Parallel()

	T.Run("every mode on every dialect", func(t *testing.T) {
		t.Parallel()

		// The limit is the one line the dialects render differently, and it
		// precedes the lock on each of them.
		bodies := map[dialect.Dialect]string{
			dialect.Postgres: `SELECT
	tokens.id
FROM tokens
WHERE tokens.expires_at <= CURRENT_TIMESTAMP
ORDER BY tokens.expires_at ASC, tokens.id ASC
LIMIT COALESCE(sqlc.narg(result_limit), 50)`,
			dialect.MySQL: `SELECT
	tokens.id
FROM tokens
WHERE tokens.expires_at <= CURRENT_TIMESTAMP(6)
ORDER BY tokens.expires_at ASC, tokens.id ASC
LIMIT ?`,
			dialect.SQLite: `SELECT
	tokens.id
FROM tokens
WHERE tokens.expires_at <= CURRENT_TIMESTAMP
ORDER BY tokens.expires_at ASC, tokens.id ASC
LIMIT COALESCE(sqlc.narg(result_limit), 50)`,
		}

		for _, d := range everyDialect() {
			for _, mode := range everyLockMode() {
				q := For(d).SweepQuery("ClaimDueTokens", guardTable, []string{IDColumn, "expires_at"},
					Sweep{Order: dueOrder(), Projection: []string{IDColumn}, Lock: mode},
					Match{Column: "expires_at", Against: CurrentTime})

				// A sweep's content is unterminated and Render supplies the
				// semicolon, so the check is on the rendered statement: that
				// is where a hand-appended suffix used to go wrong.
				want := "-- name: ClaimDueTokens :many\n" + withLock(bodies[d], lockLines[d][mode]) + "\n"

				test.EqOp(t, want, q.Render(), test.Sprintf("dialect %q, mode %s", d, mode))
			}
		}
	})

	// The bounded writes render the same scan as a subquery, and a lock there
	// is not what a caller of the read asked for — nor is it legal inside
	// MySQL's derived table.
	T.Run("the bounded writes take no lock clause", func(t *testing.T) {
		t.Parallel()

		for _, d := range everyDialect() {
			g := For(d)

			for _, q := range []*Query{
				g.SweepDeleteQuery("PurgeDueTokens", guardTable, guardColumns(), dueOrder(), dueMatches()...),
				g.SweepUpdateQuery("ExpireDueTokens", guardTable, guardColumns(),
					[]string{"secret"}, nil, dueOrder(), dueMatches()...),
			} {
				test.StrNotContains(t, q.Content, "FOR UPDATE", test.Sprintf("dialect %q, %s", d, q.Annotation.Name))
				test.StrNotContains(t, q.Content, "FOR SHARE", test.Sprintf("dialect %q, %s", d, q.Annotation.Name))
			}
		}
	})
}
