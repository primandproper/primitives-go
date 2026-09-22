package mysqltest

import (
	"context"
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"github.com/testcontainers/testcontainers-go"
)

func TestNewOptions(T *testing.T) {
	T.Parallel()

	T.Run("defaults", func(t *testing.T) {
		t.Parallel()

		cfg := newOptions(nil)
		test.EqOp(t, DefaultImage, cfg.image)
		test.EqOp(t, defaultCredential, cfg.database)
		test.EqOp(t, defaultCredential, cfg.username)
		test.EqOp(t, defaultCredential, cfg.password)
		test.Eq(t, defaultParams, cfg.params)
		test.EqOp(t, 0, cfg.maxOpenConns)
		test.EqOp(t, "", cfg.dsnEnvVar)
		test.SliceEmpty(t, cfg.customizers)
	})

	T.Run("ladder options", func(t *testing.T) {
		t.Parallel()

		cfg := newOptions([]Option{WithDSNFromEnv("SOME_TEST_MYSQL_DSN")})
		test.EqOp(t, "SOME_TEST_MYSQL_DSN", cfg.dsnEnvVar)
	})

	T.Run("options override defaults", func(t *testing.T) {
		t.Parallel()

		cfg := newOptions([]Option{
			WithImage("mariadb:11"),
			WithCredentials("mariatest", "mariauser", "mariapass"),
			WithConnectionParams("parseTime=true"),
			WithMaxOpenConns(64),
			WithCustomizers(testcontainers.WithEnv(map[string]string{"FOO": "bar"})),
		})
		test.EqOp(t, "mariadb:11", cfg.image)
		test.EqOp(t, "mariatest", cfg.database)
		test.EqOp(t, "mariauser", cfg.username)
		test.EqOp(t, "mariapass", cfg.password)
		test.Eq(t, []string{"parseTime=true"}, cfg.params)
		test.EqOp(t, 64, cfg.maxOpenConns)
		test.SliceLen(t, 1, cfg.customizers)
	})

	T.Run("customizers accumulate in call order", func(t *testing.T) {
		t.Parallel()

		cfg := newOptions([]Option{
			WithCustomizers(testcontainers.WithEnv(map[string]string{"FIRST": "1"})),
			WithCustomizers(testcontainers.WithEnv(map[string]string{"SECOND": "2"})),
		})
		test.SliceLen(t, 2, cfg.customizers)
	})
}

func TestOptions_containerOptions(T *testing.T) {
	T.Parallel()

	T.Run("user customizers come last so they can override the defaults", func(t *testing.T) {
		t.Parallel()

		override := testcontainers.WithEnv(map[string]string{"FOO": "bar"})
		got := newOptions([]Option{WithCustomizers(override)}).containerOptions()

		// database, username, password, wait strategy, then the caller's own.
		test.SliceLen(t, 5, got)
	})
}

// TestOptions_dsnFromEnv is not parallel, and neither are its subtests: t.Setenv
// refuses to run anywhere under a parallel test.
//
//nolint:paralleltest // t.Setenv forbids it, here and in every parent
func TestOptions_dsnFromEnv(T *testing.T) {
	T.Run("empty when no variable was named", func(t *testing.T) { //nolint:paralleltest // t.Setenv forbids it
		test.EqOp(t, "", newOptions(nil).dsnFromEnv())
	})

	T.Run("empty when the named variable is unset", func(t *testing.T) { //nolint:paralleltest // t.Setenv forbids it
		test.EqOp(t, "", newOptions([]Option{WithDSNFromEnv("MYSQLTEST_DSN_DEFINITELY_UNSET")}).dsnFromEnv())
	})

	T.Run("reads and trims the named variable", func(t *testing.T) { //nolint:paralleltest // t.Setenv forbids it
		const name = "MYSQLTEST_DSN_FOR_TEST"
		t.Setenv(name, "  u:p@tcp(localhost:3306)/db  ")

		test.EqOp(t, "u:p@tcp(localhost:3306)/db", newOptions([]Option{WithDSNFromEnv(name)}).dsnFromEnv())
	})

	T.Run("whitespace only reads as unset", func(t *testing.T) { //nolint:paralleltest // t.Setenv forbids it
		const name = "MYSQLTEST_DSN_BLANK_FOR_TEST"
		t.Setenv(name, "   ")

		test.EqOp(t, "", newOptions([]Option{WithDSNFromEnv(name)}).dsnFromEnv())
	})
}

func TestParseDSN(T *testing.T) {
	T.Parallel()

	T.Run("credentials come out of a driver DSN, not a URL", func(t *testing.T) {
		t.Parallel()

		parsed, err := parseDSN("SOME_VAR", "someuser:p@ss/word@tcp(db.internal:3307)/somedb?parseTime=true")
		must.NoError(t, err)

		test.EqOp(t, "somedb", parsed.DBName)
		test.EqOp(t, "someuser", parsed.User)
		test.EqOp(t, "p@ss/word", parsed.Passwd)
		test.EqOp(t, "db.internal:3307", parsed.Addr)
	})

	T.Run("a failure names the variable it came from", func(t *testing.T) {
		t.Parallel()

		// A URL is the likeliest mistake, since pgtest's variable holds one.
		_, err := parseDSN("SOME_MYSQL_DSN_VAR", "mysql://u:p@localhost:3306/db")

		must.Error(t, err)
		test.StrContains(t, err.Error(), "SOME_MYSQL_DSN_VAR")
	})
}

func TestRun_Container(T *testing.T) {
	T.Parallel()

	T.Run("hands the closure a queryable database", func(t *testing.T) {
		t.Parallel()

		Run(t, func(ctx context.Context, my *Instance) {
			must.NotNil(t, my.DB)
			must.NotNil(t, my.Container)
			test.EqOp(t, defaultCredential, my.Database)
			test.StrContains(t, my.ConnectionString, "parseTime=true")

			var current string
			must.NoError(t, my.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&current))
			test.EqOp(t, defaultCredential, current)
		})
	})

	T.Run("root connection can do admin work", func(t *testing.T) {
		t.Parallel()

		Run(t, func(ctx context.Context, my *Instance) {
			root := my.Open(t, my.RootConnectionString(t, "multiStatements=true"))

			var user string
			must.NoError(t, root.QueryRowContext(ctx, "SELECT CURRENT_USER()").Scan(&user))
			test.StrHasPrefix(t, "root@", user)
		})
	})
}

// TestRun_DSNFromEnv_Container stands a container up the ordinary way and then
// points a second Run at it through the environment, which is the shape a CI
// job providing its own server has. It is not parallel because t.Setenv
// refuses to be.
func TestRun_DSNFromEnv_Container(t *testing.T) {
	const name = "MYSQLTEST_RUN_DSN_FROM_ENV"

	Run(t, func(_ context.Context, provided *Instance) {
		t.Setenv(name, provided.ConnectionString)

		Run(t, func(ctx context.Context, my *Instance) {
			test.Nil(t, my.Container)
			test.EqOp(t, provided.ConnectionString, my.ConnectionString)

			// Read out of the DSN, not from WithCredentials, which says otherwise.
			test.EqOp(t, provided.Database, my.Database)
			test.EqOp(t, provided.Username, my.Username)
			test.EqOp(t, provided.Password, my.Password)

			var current string
			must.NoError(t, my.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&current))
			test.EqOp(t, provided.Database, current)
		}, WithDSNFromEnv(name), WithCredentials("ignored", "ignored", "ignored"))
	})
}
