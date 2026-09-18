package ddl

import (
	"strings"
	"testing"

	"github.com/primandproper/primitives-go/v2/database/dialect"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// upgradeSchema is the shape a change to a shipped schema takes: an ALTER in
// every dialect, and — because there is no ADD COLUMN IF NOT EXISTS on MySQL or
// SQLite — nothing idempotent about it. Which is the reason the version exists.
var upgradeSchema = Schema{
	Component: "widget",
	Postgres: `ALTER TABLE {{PREFIX}}widget_items ADD COLUMN owner TEXT;
CREATE INDEX IF NOT EXISTS {{PREFIX}}widget_items_owner_idx ON {{PREFIX}}widget_items (owner);`,
	MySQL:  `ALTER TABLE {{PREFIX}}widget_items ADD COLUMN owner VARCHAR(64);`,
	SQLite: `ALTER TABLE {{PREFIX}}widget_items ADD COLUMN owner TEXT;`,
}

// lateTableSchema is a version that creates a table the first one did not, which
// is what makes Tables a question about the sequence rather than about its first
// element.
var lateTableSchema = Schema{
	Component: "widget",
	Postgres:  `CREATE TABLE IF NOT EXISTS {{PREFIX}}widget_audits (id TEXT PRIMARY KEY);`,
	MySQL:     `CREATE TABLE IF NOT EXISTS {{PREFIX}}widget_audits (id VARCHAR(64) PRIMARY KEY);`,
	SQLite:    `CREATE TABLE IF NOT EXISTS {{PREFIX}}widget_audits (id TEXT PRIMARY KEY);`,
}

// testMigrations is the sequence those three make: the schema as it first
// shipped, a change to it, and a table added later.
var testMigrations = Migrations{
	{Version: 1, Schema: testSchema},
	{Version: 2, Schema: upgradeSchema},
	{Version: 3, Schema: lateTableSchema},
}

func TestMigrations_Validate(T *testing.T) {
	T.Parallel()

	T.Run("accepts an ascending sequence", func(t *testing.T) {
		t.Parallel()

		test.NoError(t, testMigrations.Validate())
	})

	T.Run("accepts a sequence with nothing in it", func(t *testing.T) {
		t.Parallel()

		// Since returns one for a database already at Latest, so an empty
		// sequence is a value rather than a missing one.
		test.NoError(t, Migrations(nil).Validate())
	})

	T.Run("accepts a sequence with gaps in it", func(t *testing.T) {
		t.Parallel()

		// The versions are monotonic, not contiguous: a package that renumbers
		// nothing still owes nobody a version 2.
		test.NoError(t, Migrations{
			{Version: 1, Schema: testSchema},
			{Version: 7, Schema: upgradeSchema},
		}.Validate())
	})

	T.Run("rejects version zero", func(t *testing.T) {
		t.Parallel()

		// Zero is the version of a database that has run nothing, which is what
		// makes Since(0) the fresh install.
		err := Migrations{{Version: 0, Schema: testSchema}}.Validate()
		must.Error(t, err)
		test.ErrorIs(t, err, ErrVersionZero)
		test.StrContains(t, err.Error(), "widget")
	})

	T.Run("rejects a duplicated version", func(t *testing.T) {
		t.Parallel()

		// The copy-paste that adds a version and forgets to change the number,
		// which would otherwise render both bodies for a consumer at 1.
		err := Migrations{
			{Version: 1, Schema: testSchema},
			{Version: 1, Schema: upgradeSchema},
		}.Validate()
		must.Error(t, err)
		test.ErrorIs(t, err, ErrVersionDuplicated)
		test.StrContains(t, err.Error(), "widget")
	})

	T.Run("rejects a descending version", func(t *testing.T) {
		t.Parallel()

		err := Migrations{
			{Version: 2, Schema: upgradeSchema},
			{Version: 1, Schema: testSchema},
		}.Validate()
		must.Error(t, err)
		test.ErrorIs(t, err, ErrVersionOutOfOrder)
		test.StrContains(t, err.Error(), "1 follows 2")
	})
}

func TestMigrations_Latest(T *testing.T) {
	T.Parallel()

	T.Run("is the version the whole sequence brings a database to", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, uint64(3), testMigrations.Latest())
	})

	T.Run("is zero for a sequence with nothing in it", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, uint64(0), Migrations(nil).Latest())
	})

	T.Run("answers for a sequence Validate would reject", func(t *testing.T) {
		t.Parallel()

		// The highest version, not the last element's: an accessor that reported
		// 1 here would say a sequence holding a version 4 was fully applied.
		test.EqOp(t, uint64(4), Migrations{
			{Version: 4, Schema: testSchema},
			{Version: 1, Schema: upgradeSchema},
		}.Latest())
	})
}

func TestMigrations_Since(T *testing.T) {
	T.Parallel()

	T.Run("zero is the whole sequence", func(t *testing.T) {
		t.Parallel()

		// A database that has run nothing owes everything, so a fresh install
		// needs no spelling of its own.
		since, err := testMigrations.Since(0)
		must.NoError(t, err)
		test.Eq(t, testMigrations, since)
	})

	T.Run("returns only what a database at a version still owes", func(t *testing.T) {
		t.Parallel()

		since, err := testMigrations.Since(1)
		must.NoError(t, err)
		must.SliceLen(t, 2, since)
		test.EqOp(t, uint64(2), since[0].Version)
		test.EqOp(t, uint64(3), since[1].Version)
	})

	T.Run("returns nothing for a database already at the latest version", func(t *testing.T) {
		t.Parallel()

		// Empty rather than an error: there being nothing to splice this time is
		// the ordinary outcome, not a failure.
		since, err := testMigrations.Since(testMigrations.Latest())
		must.NoError(t, err)
		test.SliceEmpty(t, since)
	})

	T.Run("returns nothing for a database past the latest version", func(t *testing.T) {
		t.Parallel()

		// A consumer downgraded to an older build of this package reads its own
		// applied version, which is higher than anything the sequence holds.
		since, err := testMigrations.Since(99)
		must.NoError(t, err)
		test.SliceEmpty(t, since)
	})

	T.Run("skips a version the sequence does not hold", func(t *testing.T) {
		t.Parallel()

		// The versions are the package's, and a consumer's recorded version is
		// one of them — but "greater than" is what decides, so a number between
		// two of them still owes the later one.
		since, err := Migrations{
			{Version: 1, Schema: testSchema},
			{Version: 7, Schema: upgradeSchema},
		}.Since(4)
		must.NoError(t, err)
		must.SliceLen(t, 1, since)
		test.EqOp(t, uint64(7), since[0].Version)
	})

	T.Run("refuses a sequence that does not ascend", func(t *testing.T) {
		t.Parallel()

		since, err := Migrations{
			{Version: 2, Schema: upgradeSchema},
			{Version: 1, Schema: testSchema},
		}.Since(0)
		test.ErrorIs(t, err, ErrVersionOutOfOrder)
		test.SliceEmpty(t, since)
	})

	T.Run("shares no spare capacity with the sequence it came from", func(t *testing.T) {
		t.Parallel()

		// The result is a window onto the original's array, so an append to it
		// would otherwise write into a slot the original still owns.
		sequence := make(Migrations, 0, 8)
		sequence = append(sequence,
			Migration{Version: 1, Schema: testSchema},
			Migration{Version: 2, Schema: upgradeSchema},
		)

		since, err := sequence.Since(1)
		must.NoError(t, err)
		test.EqOp(t, len(since), cap(since))
	})
}

func TestMigrations_Statements(T *testing.T) {
	T.Parallel()

	T.Run("renders every version in order", func(t *testing.T) {
		t.Parallel()

		stmts, err := testMigrations.Statements(dialect.Postgres, "ddb")
		must.NoError(t, err)

		// Three from the original schema, two from the change, one from the
		// table added later.
		must.SliceLen(t, 6, stmts)
		test.StrContains(t, stmts[0], "ddb_widget_items")
		test.StrContains(t, stmts[3], "ALTER TABLE ddb_widget_items")
		test.StrContains(t, stmts[5], "ddb_widget_audits")
	})

	T.Run("renders each dialect's own version of the change", func(t *testing.T) {
		t.Parallel()

		// MySQL ships no partial index and no index on the new column, so the
		// count differs by dialect while the ALTER does not.
		stmts, err := testMigrations.Statements(dialect.MySQL, "")
		must.NoError(t, err)
		must.SliceLen(t, 4, stmts)

		joined := strings.Join(stmts, "\n")
		test.StrContains(t, joined, "ALTER TABLE widget_items ADD COLUMN owner VARCHAR(64)")
		test.StrNotContains(t, joined, Placeholder)
	})

	T.Run("renders an upgrade without the schema it upgrades", func(t *testing.T) {
		t.Parallel()

		// The point of the whole exercise: a database at version 1 is given the
		// change and not the CREATE TABLE it already ran.
		since, err := testMigrations.Since(1)
		must.NoError(t, err)

		stmts, err := since.Statements(dialect.SQLite, "ddb")
		must.NoError(t, err)

		joined := strings.Join(stmts, "\n")
		test.StrContains(t, joined, "ALTER TABLE ddb_widget_items")
		test.StrNotContains(t, joined, "CREATE TABLE IF NOT EXISTS ddb_widget_items")
	})

	T.Run("renders nothing for a sequence with nothing in it", func(t *testing.T) {
		t.Parallel()

		stmts, err := Migrations(nil).Statements(dialect.Postgres, "ddb")
		must.NoError(t, err)
		test.SliceEmpty(t, stmts)
	})

	T.Run("refuses a sequence that does not ascend rather than emitting it", func(t *testing.T) {
		t.Parallel()

		stmts, err := Migrations{
			{Version: 1, Schema: testSchema},
			{Version: 1, Schema: upgradeSchema},
		}.Statements(dialect.Postgres, "ddb")
		test.ErrorIs(t, err, ErrVersionDuplicated)
		test.SliceEmpty(t, stmts)
	})

	T.Run("propagates an unsupported dialect", func(t *testing.T) {
		t.Parallel()

		stmts, err := testMigrations.Statements("oracle", "ddb")
		must.Error(t, err)
		test.ErrorIs(t, err, dialect.ErrUnsupported)
		test.SliceEmpty(t, stmts)
	})

	T.Run("propagates a rejected namespace", func(t *testing.T) {
		t.Parallel()

		stmts, err := testMigrations.Statements(dialect.Postgres, "ddb_")
		test.ErrorIs(t, err, ErrPrefixTrailingSeparator)
		test.SliceEmpty(t, stmts)
	})
}

func TestMigrations_SQL(T *testing.T) {
	T.Parallel()

	T.Run("joins the whole sequence into one body", func(t *testing.T) {
		t.Parallel()

		body, err := testMigrations.SQL(dialect.Postgres, "ddb")
		must.NoError(t, err)

		test.StrHasSuffix(t, ";\n", body)
		test.StrContains(t, body, "ddb_widget_items")
		test.StrContains(t, body, "ddb_widget_audits")

		// One terminator per statement, which is what makes the body safe to
		// hand to a tool that splits on semicolons.
		test.EqOp(t, 6, strings.Count(body, ";"))
	})

	T.Run("renders no body for a sequence with nothing in it", func(t *testing.T) {
		t.Parallel()

		// Not a bare terminator: an empty statement is something the tool
		// downstream would try to execute.
		body, err := Migrations(nil).SQL(dialect.Postgres, "ddb")
		must.NoError(t, err)
		test.EqOp(t, "", body)
	})

	T.Run("propagates a rejected sequence and returns no body", func(t *testing.T) {
		t.Parallel()

		body, err := Migrations{{Version: 0, Schema: testSchema}}.SQL(dialect.Postgres, "ddb")
		test.ErrorIs(t, err, ErrVersionZero)
		test.EqOp(t, "", body)
	})

	T.Run("propagates an unsupported dialect and returns no body", func(t *testing.T) {
		t.Parallel()

		body, err := testMigrations.SQL("oracle", "ddb")
		test.ErrorIs(t, err, dialect.ErrUnsupported)
		test.EqOp(t, "", body)
	})
}

func TestMigrations_Tables(T *testing.T) {
	T.Parallel()

	T.Run("collects every table any version creates", func(t *testing.T) {
		t.Parallel()

		// widget_audits arrives at version 3, so the answer is not in the first
		// element — and the last element holds least of all.
		test.Eq(t, []string{"widget_audits", "widget_items", "widget_totals"}, testMigrations.Tables(""))
	})

	T.Run("qualifies every name with the namespace", func(t *testing.T) {
		t.Parallel()

		test.Eq(t, []string{
			"ddb_widget_audits",
			"ddb_widget_items",
			"ddb_widget_totals",
		}, testMigrations.Tables("ddb"))
	})

	T.Run("reports a table named by more than one version once", func(t *testing.T) {
		t.Parallel()

		// The change at version 2 alters widget_items, which the create at
		// version 1 already reported.
		test.Eq(t, []string{"widget_items", "widget_totals"}, Migrations{
			{Version: 1, Schema: testSchema},
			{Version: 2, Schema: upgradeSchema},
		}.Tables(""))
	})

	T.Run("a sequence with nothing in it creates nothing", func(t *testing.T) {
		t.Parallel()

		test.SliceEmpty(t, Migrations(nil).Tables("ddb"))
	})
}

func TestMigrations_Identifiers(T *testing.T) {
	T.Parallel()

	T.Run("collects every name across every version, sorted and deduplicated", func(t *testing.T) {
		t.Parallel()

		test.Eq(t, []string{
			"widget_audits",
			"widget_items",
			"widget_items_claim_idx",
			"widget_items_owner_idx",
			"widget_totals",
		}, testMigrations.Identifiers(""))
	})

	T.Run("is a superset of the tables the same sequence creates", func(t *testing.T) {
		t.Parallel()

		identifiers := testMigrations.Identifiers("ddb")
		for _, table := range testMigrations.Tables("ddb") {
			test.SliceContainsOp(t, identifiers, table)
		}
	})
}

func TestMigrations_ValidatePrefix(T *testing.T) {
	T.Parallel()

	T.Run("accepts a prefix every version renders legally", func(t *testing.T) {
		t.Parallel()

		test.NoError(t, testMigrations.ValidatePrefix("ddb"))
		test.NoError(t, testMigrations.ValidatePrefix(""))
	})

	T.Run("rejects a trailing separator", func(t *testing.T) {
		t.Parallel()

		test.ErrorIs(t, testMigrations.ValidatePrefix("ddb_"), ErrPrefixTrailingSeparator)
	})

	T.Run("vets a name only an earlier version creates", func(t *testing.T) {
		t.Parallel()

		// widget_items_claim_idx is created by version 1 and named by no later
		// one, and a consumer installing from nothing still runs it — so a check
		// that read only the newest version would pass a prefix that cannot be
		// installed.
		longest := len("widget_items_claim_idx")
		namespace := strings.Repeat("n", MaxIdentifierLength-longest)

		err := testMigrations.ValidatePrefix(namespace)
		must.Error(t, err)
		test.ErrorIs(t, err, ErrPrefixTooLong)
		test.StrContains(t, err.Error(), "claim_idx")
	})
}
