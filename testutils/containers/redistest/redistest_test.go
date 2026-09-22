package redistest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/primandproper/primitives-go/v2/testutils/containers"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestNewOptions(T *testing.T) {
	T.Parallel()

	T.Run("defaults", func(t *testing.T) {
		t.Parallel()

		cfg := newOptions(nil)
		test.EqOp(t, DefaultImage, cfg.image)
		test.EqOp(t, "", cfg.dsnEnvVar)
		test.False(t, cfg.clusterEnabled)
	})

	T.Run("ladder options", func(t *testing.T) {
		t.Parallel()

		cfg := newOptions([]Option{WithDSNFromEnv("SOME_TEST_REDIS_URL")})
		test.EqOp(t, "SOME_TEST_REDIS_URL", cfg.dsnEnvVar)
	})

	T.Run("options override defaults", func(t *testing.T) {
		t.Parallel()

		cfg := newOptions([]Option{WithImage("docker.io/redis:8"), WithClusterEnabled()})
		test.EqOp(t, "docker.io/redis:8", cfg.image)
		test.True(t, cfg.clusterEnabled)
	})
}

// TestStart_Gate pins the guarantee Start inherits from containers.Run: callers
// no longer have to remember containers.SkipIfNotRunning themselves.
//
//nolint:paralleltest // mutates the package-level RunningTests gate; must run serially
func TestStart_Gate(T *testing.T) {
	T.Run("skips instead of demanding a Docker daemon", func(t *testing.T) { //nolint:paralleltest // mutates the package-level RunningTests gate; must run serially
		original := containers.RunningTests
		t.Cleanup(func() { containers.RunningTests = original })
		containers.RunningTests = false

		t.Cleanup(func() { test.True(t, t.Skipped()) })

		Start(t)

		t.Error("Start returned instead of skipping the test")
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
		test.EqOp(t, "", newOptions([]Option{WithDSNFromEnv("REDISTEST_URL_DEFINITELY_UNSET")}).dsnFromEnv())
	})

	T.Run("reads and trims the named variable", func(t *testing.T) { //nolint:paralleltest // t.Setenv forbids it
		const name = "REDISTEST_URL_FOR_TEST"
		t.Setenv(name, "  redis://localhost:6379  ")

		test.EqOp(t, "redis://localhost:6379", newOptions([]Option{WithDSNFromEnv(name)}).dsnFromEnv())
	})

	T.Run("whitespace only reads as unset", func(t *testing.T) { //nolint:paralleltest // t.Setenv forbids it
		const name = "REDISTEST_URL_BLANK_FOR_TEST"
		t.Setenv(name, "   ")

		test.EqOp(t, "", newOptions([]Option{WithDSNFromEnv(name)}).dsnFromEnv())
	})
}

func TestParseDSN(T *testing.T) {
	T.Parallel()

	T.Run("address and password come out of the URL", func(t *testing.T) {
		t.Parallel()

		parsed, err := parseDSN("SOME_VAR", "redis://:s3cret@cache.internal:6380/2")
		must.NoError(t, err)

		test.EqOp(t, "cache.internal:6380", parsed.Addr)
		test.EqOp(t, "s3cret", parsed.Password)
		test.EqOp(t, 2, parsed.DB)
	})

	T.Run("a failure names the variable it came from", func(t *testing.T) {
		t.Parallel()

		_, err := parseDSN("SOME_REDIS_URL_VAR", "http://localhost:6379")

		must.Error(t, err)
		test.StrContains(t, err.Error(), "SOME_REDIS_URL_VAR")
	})
}

func TestOptions_instanceForDSN(T *testing.T) {
	T.Parallel()

	T.Run("an unreachable server is an error rather than an Instance", func(t *testing.T) {
		t.Parallel()

		// Already done, so the ping fails on the spot rather than spending the
		// readiness policy on a port nothing listens on.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		cfg := newOptions([]Option{WithDSNFromEnv("SOME_REDIS_URL_VAR")})
		instance, err := cfg.instanceForDSN(ctx, "redis://127.0.0.1:1")

		must.Error(t, err)
		test.Nil(t, instance)
		test.StrContains(t, err.Error(), "SOME_REDIS_URL_VAR")
	})
}

func TestTry(T *testing.T) {
	T.Parallel()

	T.Run("refuses WithDSNFromEnv rather than starting a container", func(t *testing.T) {
		t.Parallel()

		container, shutdown, err := Try(t.Context(), WithDSNFromEnv("SOME_REDIS_URL_VAR"))

		must.True(t, errors.Is(err, errDSNNeedsRun))
		test.Nil(t, container)
		test.NoError(t, shutdown(t.Context()))
	})
}

func TestRun_Container(T *testing.T) {
	T.Parallel()

	T.Run("hands the closure a reachable container", func(t *testing.T) {
		t.Parallel()

		Run(t, func(_ context.Context, rd *Instance) {
			must.NotNil(t, rd.Container)
			test.StrHasPrefix(t, "redis://", rd.ConnectionString)
			test.False(t, strings.HasPrefix(rd.Address, "redis://"))
			test.EqOp(t, Address(t, rd.Container), rd.Address)
		})
	})
}

// TestRun_DSNFromEnv_Container stands a container up the ordinary way and then
// points a second Run at it through the environment, which is the shape a CI
// job providing its own server has. It is not parallel because t.Setenv
// refuses to be.
func TestRun_DSNFromEnv_Container(t *testing.T) {
	const name = "REDISTEST_RUN_URL_FROM_ENV"

	Run(t, func(_ context.Context, provided *Instance) {
		t.Setenv(name, provided.ConnectionString)

		Run(t, func(_ context.Context, rd *Instance) {
			test.Nil(t, rd.Container)
			test.EqOp(t, provided.ConnectionString, rd.ConnectionString)
			test.EqOp(t, provided.Address, rd.Address)
		}, WithDSNFromEnv(name))
	})
}

func TestStart_Container(T *testing.T) {
	T.Parallel()

	T.Run("returns a reachable container", func(t *testing.T) {
		t.Parallel()

		container := Start(t)
		must.NotNil(t, container)

		addr := Address(t, container)
		test.False(t, strings.HasPrefix(addr, "redis://"))
		test.StrContains(t, addr, ":")
	})
}
