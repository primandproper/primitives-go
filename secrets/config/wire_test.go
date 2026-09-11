package secretscfg

import (
	"context"
	"os"
	"testing"

	"github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/secrets"

	"github.com/shoenig/test/must"
)

func TestNewSecretSourceFromConfig(T *testing.T) {
	T.Parallel()

	T.Run("nil config returns env source", func(t *testing.T) {
		t.Parallel()

		var cfg *Config
		source, err := NewSecretSource(context.Background(), cfg)
		must.NoError(t, err)
		must.NotNil(t, source)

		key := "TEST_WIRE_NIL_" + t.Name()
		value := "from-env"
		must.NoError(t, os.Setenv(key, value))
		t.Cleanup(func() { _ = os.Unsetenv(key) })

		got, err := source.GetSecret(context.Background(), key)
		must.NoError(t, err)
		must.EqOp(t, value, got)
	})

	T.Run("empty provider returns env source", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Provider: ""}
		source, err := NewSecretSource(context.Background(), cfg)
		must.NoError(t, err)
		must.NotNil(t, source)

		key := "TEST_WIRE_EMPTY_" + t.Name()
		value := "from-env"
		must.NoError(t, os.Setenv(key, value))
		t.Cleanup(func() { _ = os.Unsetenv(key) })

		got, err := source.GetSecret(context.Background(), key)
		must.NoError(t, err)
		must.EqOp(t, value, got)
	})

	T.Run("noop provider returns noop source", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Provider: ProviderNoop}
		source, err := NewSecretSource(context.Background(), cfg)
		must.NoError(t, err)
		must.NotNil(t, source)

		got, err := source.GetSecret(context.Background(), "any")
		must.ErrorIs(t, err, secrets.ErrSecretNotFound)
		must.EqOp(t, "", got)
	})

	T.Run("env provider returns env source", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Provider: ProviderEnv}
		source, err := NewSecretSource(context.Background(), cfg)
		must.NoError(t, err)
		must.NotNil(t, source)

		key := "TEST_WIRE_ENV_" + t.Name()
		value := "from-env"
		must.NoError(t, os.Setenv(key, value))
		t.Cleanup(func() { _ = os.Unsetenv(key) })

		got, err := source.GetSecret(context.Background(), key)
		must.NoError(t, err)
		must.EqOp(t, value, got)
	})

	T.Run("an unrecognized provider names itself in the error", func(t *testing.T) {
		t.Parallel()

		// One door, one wrapping. The thin second constructor used to add
		// "provide secret source" on top of this, so the same failure reached a
		// caller with two different messages depending on which door it came
		// through; what survives is the one that says which provider.
		cfg := &Config{Provider: "vault"}
		source, err := NewSecretSource(context.Background(), cfg)
		must.Error(t, err)
		must.Nil(t, source)
		must.ErrorIs(t, err, errors.ErrUnknownProvider)
		must.StrContains(t, err.Error(), `"vault"`)
	})
}
