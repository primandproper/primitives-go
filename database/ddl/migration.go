package ddl

import (
	"strings"

	"github.com/primandproper/primitives-go/v2/database/dialect"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
)

// ErrVersionZero indicates a migration at version zero. Zero is the version a
// database that has run nothing is at, which is what makes Since(0) the fresh
// install; a migration claiming it would be a change nobody can be described as
// not having run yet.
var ErrVersionZero = platformerrors.New("migration version must be greater than zero")

// ErrVersionDuplicated indicates two migrations in one sequence claiming the
// same version. It is distinct from ErrVersionOutOfOrder because the fix is
// different: the sequence is ordered, and the second migration needs a version
// of its own rather than a move.
var ErrVersionDuplicated = platformerrors.New("migration version appears twice in the sequence")

// ErrVersionOutOfOrder indicates a migration whose version is lower than the one
// before it. The sequence is the order the migrations run in, so a sequence that
// does not ascend does not say what a consumer at version N still owes.
var ErrVersionOutOfOrder = platformerrors.New("migration versions must ascend")

// Migration is one versioned step of a package's schema: the version it brings a
// database to, and the DDL that gets it there in each dialect.
//
// A shipped migration is never edited. Once a version has been rendered into a
// consumer's migration sequence and run somewhere, its DDL is what that database
// has; editing it changes only what a fresh install gets, and the two silently
// diverge. A change to the schema is a new Migration at a new version — an ALTER
// in the dialects that need one — appended to the sequence.
//
// Version 1 holds the whole schema as a package first shipped it, so a fresh
// install is the sequence rendered from the start. Every version after it holds
// only its own change, which is why Migrations.Tables reads the whole sequence
// rather than its last element.
type Migration struct {
	// Schema is the DDL this version applies, in each dialect it supports. Its
	// Component names the owning package, and appears in the errors a sequence
	// raises so a failure says which package's migrations were rejected.
	Schema Schema

	// Version is what a database has run once this migration has been applied.
	// It is monotonic within a package's sequence and starts at 1; it is not the
	// version the migration occupies in a consumer's own sequence, which stays
	// the consumer's to choose — see database/migrate's WithGeneratedMigration.
	Version uint64
}

// Migrations is a package's schema over time: its migrations in the order they
// run, ascending by version.
//
// It renders the same two ways a Schema does — Statements and SQL — over the
// whole sequence, so a consumer installing from nothing splices what the
// sequence renders, and one that already ran version N splices what Since(N)
// renders. Both refuse a sequence whose versions duplicate or descend rather
// than emitting DDL in an order nobody meant.
//
// A consumer that wants one migration of their own per version of this one
// renders the elements individually instead, through each Migration's Schema.
// That is the shape database/migrate's WithGeneratedMigration takes, and the
// reason Schema's own surface is unchanged: it renders a schema as it stands,
// which is what every generator over a single version still wants.
type Migrations []Migration

// Validate reports whether the sequence is one that can be run: every version
// positive, and each greater than the one before it.
//
// The rendering methods call it, so a malformed sequence fails where it is
// rendered rather than where it is run. It is exported for the package that
// ships the sequence, which can pin the property in its own test and fail at
// build time instead of at a consumer's.
func (m Migrations) Validate() error {
	var previous uint64

	// Indexed rather than ranged by value: a Migration carries its whole Schema,
	// and copying four DDL bodies per element to read a uint64 off them is what
	// the linter is right about.
	for i := range m {
		version, component := m[i].Version, m[i].Schema.Component

		switch {
		case version == 0:
			return platformerrors.Wrapf(ErrVersionZero, "%s migration", component)
		case version == previous:
			return platformerrors.Wrapf(ErrVersionDuplicated, "%s migration version %d", component, version)
		case version < previous:
			return platformerrors.Wrapf(ErrVersionOutOfOrder,
				"%s migration version %d follows %d", component, version, previous)
		}

		previous = version
	}

	return nil
}

// Latest is the version a database that has run the whole sequence is at, and
// zero for a sequence with no migrations in it.
//
// It is the highest version rather than the last element's, so it answers the
// same thing for a sequence Validate would reject.
func (m Migrations) Latest() uint64 {
	var latest uint64

	for i := range m {
		if m[i].Version > latest {
			latest = m[i].Version
		}
	}

	return latest
}

// Since returns the migrations a database at version still owes: those with a
// version greater than it, in order.
//
// Zero is the version of a database that has run nothing, so Since(0) is the
// whole sequence and a fresh install needs no separate spelling. A database
// already at Latest owes nothing, and the empty result — not an error — is how
// a consumer learns there is nothing to splice this time.
//
// The result renders through the same Statements and SQL as the sequence it came
// from. It shares no capacity with that sequence, so appending to it cannot
// reach the original's elements.
func (m Migrations) Since(version uint64) (Migrations, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	for i := range m {
		if m[i].Version > version {
			return m[i:len(m):len(m)], nil
		}
	}

	return nil, nil
}

// Statements renders the whole sequence against the dialect and the prefix, in
// version order, as individually executable statements.
//
// A sequence with no migrations in it renders none, which is what Since returns
// for a database already at Latest.
func (m Migrations) Statements(d dialect.Dialect, prefix string) ([]string, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	var out []string

	for i := range m {
		stmts, err := m[i].Schema.Statements(d, prefix)
		if err != nil {
			return nil, err
		}

		out = append(out, stmts...)
	}

	return out, nil
}

// SQL renders the same statements as Statements, joined back into one migration
// body — what a caller hands to database/migrate's WithGeneratedMigration.
//
// One body, not one per version: a consumer splicing several versions of this
// sequence into several migrations of their own renders each Migration's Schema
// instead, and chooses a version of their own for each.
func (m Migrations) SQL(d dialect.Dialect, prefix string) (string, error) {
	stmts, err := m.Statements(d, prefix)
	if err != nil {
		return "", err
	}

	return joinStatements(stmts), nil
}

// Tables returns every table the sequence creates under prefix, sorted and
// deduplicated, which is Schema.Tables' answer for a package whose schema has a
// history.
//
// It reads the whole sequence because a later migration alters what an earlier
// one created: the tables are not in any single version, and the last one holds
// least of all. A table created and later dropped is still reported, which is
// the one thing this cannot see — the reader knows CREATE TABLE and nothing
// else, exactly as Schema.Tables does.
func (m Migrations) Tables(namespace string) []string {
	return m.collect(Schema.Tables, namespace)
}

// Identifiers returns every identifier the sequence would create under prefix,
// sorted and deduplicated. It is Schema.Identifiers over every version, and what
// ValidatePrefix vets.
func (m Migrations) Identifiers(namespace string) []string {
	return m.collect(Schema.Identifiers, namespace)
}

// collect unions one of the Schema readers over the sequence. The two readers
// differ only in which names they find, and a second copy of the union could
// drift from the first in the part that matters — whether a name appearing in
// two versions appears twice.
func (m Migrations) collect(read func(Schema, string) []string, namespace string) []string {
	seen := map[string]struct{}{}

	for i := range m {
		for _, name := range read(m[i].Schema, namespace) {
			seen[name] = struct{}{}
		}
	}

	return sorted(seen)
}

// ValidatePrefix reports whether prefix renders a legal identifier for every
// name any version of the schema creates.
//
// Every version is vetted, not just the latest: a consumer runs all of them, and
// a name that was only ever created by version 1 is still a name that reached
// statement text.
func (m Migrations) ValidatePrefix(namespace string) error {
	for i := range m {
		if err := m[i].Schema.ValidatePrefix(namespace); err != nil {
			return err
		}
	}

	return nil
}

// joinStatements renders split statements back into one migration body: one
// terminator per statement, and a blank line between them.
//
// Schema.SQL and Migrations.SQL both need it, and the convention is the kind
// that can be wrong twice — a body that ends without its terminator, or a
// statement count that does not match the semicolon count, is a body some tool
// downstream splits differently. No statements is no body rather than a bare
// terminator, which is an empty statement to whatever runs it.
func joinStatements(stmts []string) string {
	if len(stmts) == 0 {
		return ""
	}

	return strings.Join(stmts, ";\n\n") + ";\n"
}
