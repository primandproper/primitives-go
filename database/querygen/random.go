package querygen

import (
	"fmt"
	"slices"
	"strings"

	"github.com/primandproper/primitives-go/v2/database/dialect"
)

// RandomQuery renders one live row at random: the table's projection, ordered
// by the dialect's randomness function, limited to one.
//
// It is its own statement rather than a hand-written one beside the generated
// set because the randomness function is not portable — Postgres and SQLite
// spell it random(), MySQL spells it RAND() — and a statement written by hand
// is written for one engine by construction. The second engine is where it
// breaks.
//
// # It is a full scan
//
// ORDER BY a random value sorts every row the WHERE admits in order to return
// one of them, and no index helps. On a table past a few thousand rows it is
// the slowest statement in the set. The rendered statement says so in a comment
// above it, which sqlc carries onto the generated method, so the caveat reaches
// whoever calls it rather than staying in this file. A consumer with a large
// table wants TABLESAMPLE or a pick over a range of ids instead, which this
// package does not render.
//
// # The row is live
//
// The archived predicate is rendered when the column list carries archived_at
// and not otherwise, the same idiom the single-row reads use: a random row is a
// random live row, and one that forgot the predicate would return an archived
// row one time in N.
//
// matches are the equality predicates the pick is keyed on — the tenancy scope,
// conventionally — so a random row within a tenant is never a random row
// across them.
//
// It is not part of [Generator.StandardCRUD]. Most tables never want it, and
// the standard set is the set every table has.
//
// name must be unique across the consumer's whole sqlc package, as every
// [QueryAnnotation].Name must.
//
// It panics rather than returning an error, in the manner of the rest of this
// package: its arguments are string literals in a generator binary. The panic
// value is an error wrapping dialect.ErrInvalidIdentifier.
func (g *Generator) RandomQuery(name, table string, columns []string, matches ...Match) *Query {
	return &Query{
		Annotation: QueryAnnotation{Name: name, Type: OneType},
		Content:    g.randomStatement(table, columns, matches),
	}
}

// randomStatement renders the random pick: the caveat, the projection, the
// predicates the column list and the matches justify, and the random ordering.
func (g *Generator) randomStatement(table string, columns []string, matches []Match) string {
	mustIdentifier("table name", table)

	for _, column := range columns {
		mustIdentifier("column name", column)
	}

	var predicates []string

	if slices.Contains(columns, ArchivedAtColumn) {
		predicates = append(predicates, Qualify(table, ArchivedAtColumn)+" IS NULL")
	}

	predicates = append(predicates, g.matchPredicates(table, true, matches)...)

	var where string
	if len(predicates) > 0 {
		where = "\nWHERE " + joinPredicates(predicates, "\t")
	}

	random := g.randomFunction()

	return fmt.Sprintf("-- ORDER BY %s sorts every matching row to return one, so this is a full scan.\n"+
		"-- Past a few thousand rows, prefer TABLESAMPLE or a pick over a range of ids.\n"+
		"SELECT\n\t%s\nFROM %s%s\nORDER BY %s\nLIMIT 1;",
		random,
		strings.Join(QualifyAll(table, columns), ",\n\t"),
		table,
		where,
		random,
	)
}

// randomFunction renders the dialect's uniformly random value, which is all
// RandomQuery orders by.
func (g *Generator) randomFunction() string {
	if g.dialect == dialect.MySQL {
		return "RAND()"
	}

	return "random()"
}
