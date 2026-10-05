package querygen

import (
	"strings"
	"testing"

	"github.com/primandproper/primitives-go/v2/database/dialect"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestGenerator_RandomQuery(T *testing.T) {
	T.Parallel()

	T.Run("renders the whole statement", func(t *testing.T) {
		t.Parallel()

		q := For(dialect.Postgres).RandomQuery("GetRandomGadget", keyedTable, keyedColumns(),
			Match{Column: BelongsToAccountColumn})

		test.EqOp(t, "GetRandomGadget", q.Annotation.Name)

		// :one, because the statement is a pick rather than a page.
		test.EqOp(t, OneType, q.Annotation.Type)

		test.EqOp(t, "-- ORDER BY random() sorts every matching row to return one, so this is a full scan.\n"+
			"-- Past a few thousand rows, prefer TABLESAMPLE or a pick over a range of ids.\n"+
			"SELECT\n\t"+strings.Join(QualifyAll(keyedTable, keyedColumns()), ",\n\t")+"\n"+
			"FROM "+keyedTable+"\n"+
			"WHERE "+Qualify(keyedTable, ArchivedAtColumn)+" IS NULL\n"+
			"\tAND "+Qualify(keyedTable, BelongsToAccountColumn)+" = sqlc.arg("+BelongsToAccountColumn+")\n"+
			"ORDER BY random()\n"+
			"LIMIT 1;", q.Content)
	})

	// The one thing a hand-written copy gets wrong on a second engine.
	T.Run("orders by the dialect's randomness function", func(t *testing.T) {
		t.Parallel()

		want := map[dialect.Dialect]string{
			dialect.Postgres: "random()",
			dialect.MySQL:    "RAND()",
			dialect.SQLite:   "random()",
		}

		for _, d := range everyDialect() {
			q := For(d).RandomQuery("GetRandomGadget", keyedTable, keyedColumns())

			test.StrContains(t, q.Content, "\nORDER BY "+want[d]+"\nLIMIT 1;", test.Sprintf("dialect %q", d))
		}
	})

	// The caveat is in the statement rather than only here, so sqlc carries it
	// onto the generated method a caller reads.
	T.Run("says it is a full scan", func(t *testing.T) {
		t.Parallel()

		for _, d := range everyDialect() {
			rendered := For(d).RandomQuery("GetRandomGadget", keyedTable, keyedColumns()).Render()

			test.True(t, strings.HasPrefix(rendered, "-- name: GetRandomGadget :one\n-- ORDER BY "),
				test.Sprintf("dialect %q", d))
			test.StrContains(t, rendered, "full scan", test.Sprintf("dialect %q", d))
		}
	})

	T.Run("the column list decides the archived predicate", func(t *testing.T) {
		t.Parallel()

		for _, d := range everyDialect() {
			g := For(d)
			archived := Qualify(keyedTable, ArchivedAtColumn) + " IS NULL"

			test.StrContains(t, g.RandomQuery("GetRandomGadget", keyedTable, keyedColumns()).Content, archived,
				test.Sprintf("dialect %q", d))

			without := g.RandomQuery("GetRandomGadget", keyedTable, without(keyedColumns(), ArchivedAtColumn)).Content

			test.StrNotContains(t, without, archived, test.Sprintf("dialect %q", d))

			// Nothing left to filter on is no WHERE at all, rather than an
			// empty one.
			test.StrNotContains(t, without, "WHERE", test.Sprintf("dialect %q", d))
		}
	})

	T.Run("keys on every match", func(t *testing.T) {
		t.Parallel()

		for _, d := range everyDialect() {
			q := For(d).RandomQuery("GetRandomGadget", keyedTable, keyedColumns(),
				Match{Column: BelongsToAccountColumn}, Match{Column: "kind", Arg: "gadget_kind"})

			test.StrContains(t, q.Content, Qualify(keyedTable, BelongsToAccountColumn)+" = sqlc.arg("+BelongsToAccountColumn+")",
				test.Sprintf("dialect %q", d))
			test.StrContains(t, q.Content, Qualify(keyedTable, "kind")+" = sqlc.arg(gadget_kind)",
				test.Sprintf("dialect %q", d))
		}
	})

	T.Run("panics on an invalid identifier", func(t *testing.T) {
		t.Parallel()

		for _, call := range []func(){
			func() { For(dialect.Postgres).RandomQuery("GetRandomGadget", "gadgets; DROP", keyedColumns()) },
			func() { For(dialect.Postgres).RandomQuery("GetRandomGadget", keyedTable, []string{"id", "name--"}) },
		} {
			err := recovered(call)
			must.ErrorIs(t, err, dialect.ErrInvalidIdentifier)
		}
	})
}
