// Package redistest provides a single source of truth for the redis
// testcontainer setup that the redis-backed test suites in this repo all
// duplicate. It owns the RUN_CONTAINER_TESTS feature flag and the
// retry/wait-strategy choices, so each caller only has to express what shape
// it wants the cluster in.
//
// # Run, and the resolution ladder
//
// Run is the entry point that decides where the redis comes from, in this
// order:
//
//  1. the URL in the environment variable named by WithDSNFromEnv, if that
//     option was given and the variable is set. No container is started.
//  2. -short, which skips.
//  3. a container, behind the RUN_CONTAINER_TESTS gate — the test skips when
//     it is closed.
//
// The first rung is the one pgtest and mysqltest have, and it is here for the
// same reason: a suite pointed at servers CI already provides should be able
// to run with no Docker daemon at all, and one backend still demanding a
// container puts the daemon back.
//
// Start and Try predate Run and return the container itself, so they have no
// way to hand back a server that is not one. They stay container-only, and
// refuse WithDSNFromEnv rather than silently starting a container the caller
// asked not to have. A suite that wants the ladder uses Run.
package redistest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/testutils/containers"

	"github.com/redis/go-redis/v9"
	"github.com/shoenig/test/must"
	"github.com/testcontainers/testcontainers-go"
	rediscontainers "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// DefaultImage is the redis image Start launches when no override is provided.
const DefaultImage = "docker.io/redis:7-bullseye"

// errDSNNeedsRun is what Start and Try report when WithDSNFromEnv reaches them.
var errDSNNeedsRun = platformerrors.New("redistest: WithDSNFromEnv resolves through Run; Start and Try only ever return a container")

// Option configures Run, Start and Try.
type Option func(*options)

type options struct {
	image          string
	dsnEnvVar      string
	clusterEnabled bool
}

// WithImage overrides DefaultImage.
func WithImage(image string) Option {
	return func(o *options) { o.image = image }
}

// WithClusterEnabled passes --cluster-enabled yes to redis-server. The node
// still has no slots assigned, but CLUSTER subcommands like CLUSTER KEYSLOT
// become available — useful for tests that want Redis as a hash oracle
// without orchestrating a full multi-node cluster.
func WithClusterEnabled() Option {
	return func(o *options) { o.clusterEnabled = true }
}

// WithDSNFromEnv names an environment variable holding a redis URL
// (redis://[user:password@]host:port[/db], or rediss:// for TLS). When it is
// set and non-empty, Run connects to that server and starts no container at
// all — the first rung of the resolution ladder, ahead of -short and ahead of
// starting anything.
//
// It is how a suite runs against a redis that CI already provides, and how a
// developer points the whole binary at a local server. Container is nil on
// this path, and Address and Password are read out of the URL. Options that
// shape a container — WithImage, WithClusterEnabled — do not reach a server
// somebody else started, so a suite that relies on cluster mode has to be
// given a server that has it.
//
// Only Run honors it; Start and Try fail if it is given. See the package
// documentation.
func WithDSNFromEnv(name string) Option {
	return func(o *options) { o.dsnEnvVar = name }
}

func newOptions(opts []Option) *options {
	cfg := &options{image: DefaultImage}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// Instance is the live redis handed to a Run closure.
type Instance struct {
	// Container is the underlying testcontainer, for the rare test that needs
	// Exec or the rest of the container API. Its lifecycle is not yours to
	// manage. It is nil when WithDSNFromEnv resolved the server, since there is
	// no container then; the other fields are populated either way.
	Container *rediscontainers.RedisContainer

	// ConnectionString is the server's redis:// URL.
	ConnectionString string

	// Address is the server's host:port, suitable for dial-style config. Most
	// callers want this over ConnectionString.
	Address string

	// Password is the one in ConnectionString, empty for a server without AUTH
	// (which every container this package starts is).
	Password string
}

// Run resolves a redis and hands it to fn as an Instance. The server comes from
// the resolution ladder in the package documentation: a URL named by
// WithDSNFromEnv if one is set, and a container otherwise, behind the
// RUN_CONTAINER_TESTS gate (the test skips without a Docker daemon).
//
// A server named by WithDSNFromEnv is pinged before fn runs, so an unreachable
// one fails here rather than at the first command of the first test. Startup
// failures fail the test, and termination of any container is registered with
// tb.Cleanup — so fn is free to spawn parallel subtests against the Instance
// and return before they run.
func Run(tb testing.TB, fn func(ctx context.Context, rd *Instance), opts ...Option) {
	tb.Helper()

	if fn == nil {
		tb.Fatal("redistest: Run requires a non-nil fn")
	}

	cfg := newOptions(opts)

	if dsn := cfg.dsnFromEnv(); dsn != "" {
		// A server somebody else is running: nothing to gate on but -short,
		// because a caller asking for a fast answer does not want a network
		// round-trip either.
		if testing.Short() {
			tb.SkipNow()
		}

		ctx := tb.Context()

		instance, err := cfg.instanceForDSN(ctx, dsn)
		must.NoError(tb, err)

		fn(ctx, instance)

		return
	}

	containers.Run(tb,
		func(ctx context.Context) (*rediscontainers.RedisContainer, error) {
			return run(ctx, cfg)
		},
		func(ctx context.Context, container *rediscontainers.RedisContainer) {
			connectionString, err := container.ConnectionString(ctx)
			must.NoError(tb, err)

			fn(ctx, &Instance{
				Container:        container,
				ConnectionString: connectionString,
				Address:          strings.TrimPrefix(connectionString, "redis://"),
			})
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

// parseDSN parses a URL read from the named environment variable through
// go-redis' own parser, which is what a consumer's client will parse it with,
// naming the variable on failure so a misconfigured CI job says which knob is
// wrong.
func parseDSN(envVar, dsn string) (*redis.Options, error) {
	parsed, err := redis.ParseURL(dsn)
	if err != nil {
		return nil, platformerrors.Wrapf(err, "redistest: parsing URL from %s", envVar)
	}

	return parsed, nil
}

// instanceForDSN is the WithDSNFromEnv path: a server somebody else is
// running, so there is nothing to start and nothing to terminate. The ping
// stands in for the readiness wait a container would have had.
func (o *options) instanceForDSN(ctx context.Context, dsn string) (*Instance, error) {
	parsed, err := parseDSN(o.dsnEnvVar, dsn)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(parsed)

	pingErr := containers.PingWithRetry(ctx, func(ctx context.Context) error {
		return client.Ping(ctx).Err()
	})
	if err = platformerrors.Join(pingErr, client.Close()); err != nil {
		return nil, platformerrors.Wrapf(err, "redistest: reaching the server named by %s", o.dsnEnvVar)
	}

	return &Instance{
		ConnectionString: dsn,
		Address:          parsed.Addr,
		Password:         parsed.Password,
	}, nil
}

// Start brings up a redis container and returns it. It is containers.Run with
// the redis-shaped setup already applied, so callers inherit the whole policy:
// the RUN_CONTAINER_TESTS gate (the test skips without a Docker daemon), the
// shared retry policy, and termination registered as a tb.Cleanup. The returned
// container exposes ConnectionString and the rest of the
// rediscontainers.RedisContainer API. It accepts testing.TB so both tests and
// benchmarks can use it.
//
// Because the gate lives here, callers must not also call
// containers.SkipIfNotRunning — Start skips on their behalf.
//
// Failures during startup fail the test. Callers that need to handle startup
// failure differently, or that need redis outside a testing.TB, should use Try.
// Callers that want a server CI already provides should use Run: Start fails
// the test if given WithDSNFromEnv.
func Start(tb testing.TB, opts ...Option) *rediscontainers.RedisContainer {
	tb.Helper()

	cfg := newOptions(opts)
	if cfg.dsnEnvVar != "" {
		tb.Fatal(errDSNNeedsRun)
	}

	// containers.Run is closure-shaped because it owns teardown; Start hands the
	// container back instead, so capture it on the way through.
	var container *rediscontainers.RedisContainer

	containers.Run(tb,
		func(ctx context.Context) (*rediscontainers.RedisContainer, error) {
			return run(ctx, newOptions(opts))
		},
		func(_ context.Context, started *rediscontainers.RedisContainer) {
			container = started
		},
	)

	return container
}

// Try brings up a redis container and returns it along with a shutdown
// closure and any startup error. Prefer Start in tests — Try exists for the
// few callers that want to handle container failures specially (skip vs.
// fail) or that need to bring up Redis outside of a testing.TB context, and so
// it enforces neither the RUN_CONTAINER_TESTS gate nor any cleanup.
//
// The shutdown closure is safe to call even when err is non-nil (it is a
// no-op in that case). Like Start, Try is container-only and returns an error
// if given WithDSNFromEnv.
func Try(ctx context.Context, opts ...Option) (container *rediscontainers.RedisContainer, shutdown func(context.Context) error, err error) {
	cfg := newOptions(opts)
	if cfg.dsnEnvVar != "" {
		return nil, func(context.Context) error { return nil }, errDSNNeedsRun
	}

	container, err = containers.StartWithRetry(ctx, func(c context.Context) (*rediscontainers.RedisContainer, error) {
		return run(c, cfg)
	})
	if err != nil {
		return nil, func(context.Context) error { return nil }, err
	}

	shutdown = func(ctx context.Context) error { return container.Terminate(ctx) }
	return container, shutdown, nil
}

// run is the single definition of what a redis container for this repo looks
// like, shared by Start and Try. It performs one attempt; retry belongs to the
// caller.
func run(ctx context.Context, cfg *options) (*rediscontainers.RedisContainer, error) {
	runOpts := []testcontainers.ContainerCustomizer{
		rediscontainers.WithLogLevel(rediscontainers.LogLevelNotice),
		testcontainers.WithWaitStrategyAndDeadline(2*time.Minute, wait.ForAll(
			wait.ForListeningPort("6379/tcp"),
			wait.ForLog("Ready to accept connections"),
		)),
	}
	if cfg.clusterEnabled {
		runOpts = append(runOpts, testcontainers.WithCmdArgs("--cluster-enabled", "yes"))
	}

	return rediscontainers.Run(ctx, cfg.image, runOpts...)
}

// Address returns the container's host:port string, suitable for
// dial-style config (i.e. ConnectionString with the "redis://" scheme
// trimmed). Most callers want this over ConnectionString.
func Address(tb testing.TB, container *rediscontainers.RedisContainer) string {
	tb.Helper()

	addr, err := container.ConnectionString(tb.Context())
	must.NoError(tb, err)
	return strings.TrimPrefix(addr, "redis://")
}
