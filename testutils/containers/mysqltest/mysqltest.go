// Package mysqltest provides the MySQL testcontainer setup that every
// MySQL-backed suite in this repo would otherwise hand-roll: start the
// container with the shared retry policy and wait strategy, open a
// go-sql-driver pool against it, ping it, and tear all of it down afterwards.
// It is pgtest's MySQL counterpart, and exists because three suites (outbox,
// authorization/database, database/mysql/tableaccess) had each grown their own
// copy of exactly this.
//
// Callers describe the shape they want with Options and receive a live Instance
// inside a closure, so a test body says what it does with MySQL and nothing
// about how MySQL is stood up or torn down.
//
// # The resolution ladder
//
// Run decides where the MySQL comes from, in this order:
//
//  1. the DSN in the environment variable named by WithDSNFromEnv, if that
//     option was given and the variable is set. No container is started.
//  2. -short, which skips.
//  3. a container, behind the RUN_CONTAINER_TESTS gate — the test skips when
//     it is closed.
//
// The first rung is how a suite runs against a server CI already provides, so
// that a binary exercising postgres and MySQL side by side can be pointed at
// both and need no Docker daemon at all. It is the same rung pgtest has, and it
// exists here because an escape hatch for one dialect of a multi-dialect suite
// removes nothing: the binary still needs the daemon for the other.
//
// # What may be on the other end
//
// The container defaults to stock MySQL (DefaultImage), but the MySQL-dialect
// suites in the consuming repository run against MariaDB, and a CI job
// providing a server through WithDSNFromEnv provides MariaDB. The two are close
// enough that one dialect of SQL covers both, and far enough apart that a
// developer pointing the variable at MySQL 8 and a CI job pointing it at
// MariaDB are not running the same tests. When a result differs between the
// two, the MariaDB one is the one CI will report.
package mysqltest

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/testutils/containers"

	// Importing the driver by name also registers it, so callers get a working
	// "mysql" driver from importing mysqltest alone.
	"github.com/go-sql-driver/mysql"
	"github.com/shoenig/test/must"
	"github.com/testcontainers/testcontainers-go"
	mysqlcontainer "github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// DefaultImage is the image Run launches when no override is given. Use
	// WithImage for derivatives the rest of this setup still applies to, e.g.
	// "mariadb:11".
	DefaultImage = "mysql:8.0"

	// DriverName is the database/sql driver Instance.DB and Instance.Open use.
	DriverName = "mysql"

	defaultCredential = "platformtest"

	// A cold start on a busy CI host has to cover an image pull plus the init
	// run.
	startupDeadline = 2 * time.Minute

	// readyLog identifies the real server by the port it announces rather than
	// by counting readiness lines, because the count is not what it looks like:
	// the bootstrap server that runs the init scripts and then shuts down logs
	// "ready for connections" twice on its own — once from the X plugin, once
	// from mysqld on port 0 — so waiting for the second occurrence releases the
	// test against a server that is about to be shut down and restarted.
	//
	// The trailing space is load-bearing. Without it this also matches the X
	// plugin's "port: 33060", which the real server logs a line before it is
	// listening for clients. Matching the port keeps this image-agnostic:
	// MariaDB logs the same field, where the upstream module's
	// "port: 3306  MySQL Community Server" would never match.
	readyLog = "port: 3306 "
)

// defaultParams are the DSN parameters Instance.ConnectionString carries when
// no override is given. parseTime keeps DATETIME(6) round-tripping as
// time.Time rather than []byte; multiStatements lets a test feed rendered DDL
// in one Exec.
var defaultParams = []string{"parseTime=true", "multiStatements=true"}

// Option configures Run.
type Option func(*options)

type options struct {
	image        string
	database     string
	username     string
	password     string
	dsnEnvVar    string
	params       []string
	customizers  []testcontainers.ContainerCustomizer
	maxOpenConns int
}

// WithImage overrides DefaultImage. Use it for MySQL derivatives that the rest
// of this setup still applies to, e.g. "mariadb:11".
func WithImage(image string) Option {
	return func(o *options) { o.image = image }
}

// WithCredentials overrides the database name, user and password the container
// is provisioned with. Tests that create or drop users want distinct
// credentials so they cannot collide with the identifiers under test.
func WithCredentials(database, username, password string) Option {
	return func(o *options) {
		o.database, o.username, o.password = database, username, password
	}
}

// WithConnectionParams replaces the DSN parameters Instance.ConnectionString is
// built with. The defaults are parseTime=true and multiStatements=true. They
// apply to a container's DSN only: one read through WithDSNFromEnv is used
// verbatim, parameters and all.
func WithConnectionParams(params ...string) Option {
	return func(o *options) { o.params = params }
}

// WithDSNFromEnv names an environment variable holding a go-sql-driver DSN
// (user:password@tcp(host:port)/dbname?params — not a URL). When it is set and
// non-empty, Run connects to that server and starts no container at all — the
// first rung of the resolution ladder, ahead of -short and ahead of starting
// anything.
//
// It is how a suite runs against a MySQL or MariaDB that CI already provides,
// and how a developer points the whole binary at a local server. The
// container-only fields of Instance are absent on this path: Container is nil,
// and Database, Username and Password are read out of the DSN rather than from
// WithCredentials. The DSN is used as given, so it has to carry the parameters
// the suite relies on — parseTime=true and multiStatements=true, usually —
// because WithConnectionParams does not reach it.
func WithDSNFromEnv(name string) Option {
	return func(o *options) { o.dsnEnvVar = name }
}

// WithMaxOpenConns caps Instance.DB's pool. Set it well above the number of
// concurrent subtests sharing an Instance, otherwise they starve each other.
// Zero (the default) leaves database/sql's unlimited default in place.
func WithMaxOpenConns(n int) Option {
	return func(o *options) { o.maxOpenConns = n }
}

// WithCustomizers appends testcontainers customizers to the ones Run already
// applies. They run after the defaults, so they can override the wait strategy.
func WithCustomizers(customizers ...testcontainers.ContainerCustomizer) Option {
	return func(o *options) { o.customizers = append(o.customizers, customizers...) }
}

func newOptions(opts []Option) *options {
	cfg := &options{
		image:    DefaultImage,
		database: defaultCredential,
		username: defaultCredential,
		password: defaultCredential,
		params:   defaultParams,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// Instance is the live MySQL handed to a Run closure. DB covers the common
// case; the remaining fields are there for the tests that need a second
// connection, root access, or the container API itself.
type Instance struct {
	// DB is an open, pinged pool against Database as Username.
	DB *sql.DB

	// Container is the underlying testcontainer, for the rare test that needs
	// Host, MappedPort or Exec. Its lifecycle is not yours to manage. It is nil
	// when WithDSNFromEnv resolved the server, since there is no container then.
	Container *mysqlcontainer.MySQLContainer

	// ConnectionString is the DSN DB was opened with.
	ConnectionString string

	// Database, Username and Password are the credentials the server was
	// reached with, exposed so tests can reconnect or grant against them. On the
	// WithDSNFromEnv path they are the DSN's, not WithCredentials'.
	Database string
	Username string
	Password string
}

// RootConnectionString builds a DSN that connects as root, for suites doing
// admin work — CREATE USER, GRANT — that the provisioned user cannot. The
// container provisions MYSQL_ROOT_PASSWORD with the same value as the user
// password, which is what makes this constructible at all. Parameters are the
// caller's verbatim: root work usually wants a different set (e.g.
// allowCleartextPasswords=true) than data-path connections do.
//
// A server named by WithDSNFromEnv was provisioned by somebody else, so its root
// password is not this package's to know, and the test fails rather than
// guessing. A suite that needs root there reads a root DSN from an environment
// variable of its own.
func (i *Instance) RootConnectionString(tb testing.TB, params ...string) string {
	tb.Helper()

	if i.Container == nil {
		tb.Fatal("mysqltest: RootConnectionString needs a container; the root password of a server named by WithDSNFromEnv is not known here")
	}

	cs, err := i.Container.ConnectionString(tb.Context(), params...)
	must.NoError(tb, err)

	_, rest, found := strings.Cut(cs, "@")
	must.True(tb, found, must.Sprintf("DSN %q has no credentials section", cs))

	return "root:" + i.Password + "@" + rest
}

// Open opens and pings an additional pool against this container and closes it
// when the test ends. Use it alongside RootConnectionString to connect as root;
// for the provisioned user, DB is already open.
func (i *Instance) Open(tb testing.TB, connectionString string) *sql.DB {
	tb.Helper()

	db, err := sql.Open(DriverName, connectionString)
	must.NoError(tb, err)
	must.NotNil(tb, db)

	tb.Cleanup(func() { closePool(tb, db) })

	containers.PingUntilReady(tb, tb.Context(), db.PingContext)

	return db
}

// Run resolves a MySQL, opens a pool against it, and hands both to fn as an
// Instance. It is containers.Run with the MySQL-shaped setup — image,
// credentials, readiness wait, sql.Open, ping — already applied, so the closure
// starts from a database it can query.
//
// The server comes from the resolution ladder in the package documentation: a
// DSN named by WithDSNFromEnv if one is set, and a container otherwise, behind
// the RUN_CONTAINER_TESTS gate (the test skips without a Docker daemon).
// Startup failures fail the test, and teardown of the pool and of any container
// is registered with tb.Cleanup — so fn is free to spawn parallel subtests
// against the Instance and return before they run.
func Run(tb testing.TB, fn func(ctx context.Context, my *Instance), opts ...Option) {
	tb.Helper()

	if fn == nil {
		tb.Fatal("mysqltest: Run requires a non-nil fn")
	}

	cfg := newOptions(opts)

	if dsn := cfg.dsnFromEnv(); dsn != "" {
		// A server somebody else is running: nothing to gate on but -short,
		// because a caller asking for a fast answer does not want a database
		// round-trip either.
		if testing.Short() {
			tb.SkipNow()
		}

		ctx := tb.Context()
		fn(ctx, cfg.instanceForDSN(tb, ctx, dsn))

		return
	}

	containers.Run(tb,
		func(ctx context.Context) (*mysqlcontainer.MySQLContainer, error) {
			return mysqlcontainer.Run(ctx, cfg.image, cfg.containerOptions()...)
		},
		func(ctx context.Context, container *mysqlcontainer.MySQLContainer) {
			connectionString, err := container.ConnectionString(ctx, cfg.params...)
			must.NoError(tb, err)

			instance := &Instance{
				DB:               cfg.openPool(tb, ctx, connectionString),
				Container:        container,
				ConnectionString: connectionString,
				Database:         cfg.database,
				Username:         cfg.username,
				Password:         cfg.password,
			}

			fn(ctx, instance)
		},
	)
}

// dsnFromEnv reads the first rung of the resolution ladder, or "" when the
// caller named no variable or the one they named is unset.
func (o *options) dsnFromEnv() string {
	if o.dsnEnvVar == "" {
		return ""
	}

	return strings.TrimSpace(os.Getenv(o.dsnEnvVar))
}

// instanceForDSN is the WithDSNFromEnv path: a server somebody else is running,
// so there is nothing to start and nothing to terminate, and teardown is the
// pool and only the pool. The credentials come out of the DSN through the
// driver's own parser, since a go-sql-driver DSN is not a URL.
func (o *options) instanceForDSN(tb testing.TB, ctx context.Context, dsn string) *Instance {
	tb.Helper()

	parsed, err := parseDSN(o.dsnEnvVar, dsn)
	must.NoError(tb, err)

	return &Instance{
		DB:               o.openPool(tb, ctx, dsn),
		ConnectionString: dsn,
		Database:         parsed.DBName,
		Username:         parsed.User,
		Password:         parsed.Passwd,
	}
}

// parseDSN parses a DSN read from the named environment variable, naming the
// variable on failure so a misconfigured CI job says which knob is wrong.
func parseDSN(envVar, dsn string) (*mysql.Config, error) {
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, platformerrors.Wrapf(err, "mysqltest: parsing DSN from %s", envVar)
	}

	return parsed, nil
}

// openPool opens, sizes and pings a pool, draining it when tb ends.
func (o *options) openPool(tb testing.TB, ctx context.Context, connectionString string) *sql.DB {
	tb.Helper()

	db, err := sql.Open(DriverName, connectionString)
	must.NoError(tb, err)
	must.NotNil(tb, db)

	tb.Cleanup(func() { closePool(tb, db) })

	if o.maxOpenConns > 0 {
		db.SetMaxOpenConns(o.maxOpenConns)
	}

	containers.PingUntilReady(tb, ctx, db.PingContext)

	return db
}

// closePool drains a pool at the end of a test, logging rather than failing if
// it cannot: by then the test's own assertions have already had their say.
func closePool(tb testing.TB, db *sql.DB) {
	tb.Helper()

	if err := db.Close(); err != nil {
		tb.Logf("mysqltest: closing pool: %v", err)
	}
}

// containerOptions renders the resolved options as testcontainers customizers.
// User-supplied customizers come last so they can override the defaults.
func (o *options) containerOptions() []testcontainers.ContainerCustomizer {
	return append([]testcontainers.ContainerCustomizer{
		mysqlcontainer.WithDatabase(o.database),
		mysqlcontainer.WithUsername(o.username),
		mysqlcontainer.WithPassword(o.password),
		testcontainers.WithWaitStrategyAndDeadline(
			startupDeadline,
			wait.ForLog(readyLog),
		),
	}, o.customizers...)
}
