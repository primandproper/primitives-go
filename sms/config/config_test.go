package smscfg

import (
	"net/http"
	"testing"

	"github.com/primandproper/primitives-go/v2/errors"
	loggingnoop "github.com/primandproper/primitives-go/v2/observability/logging/noop"
	metricsnoop "github.com/primandproper/primitives-go/v2/observability/metrics/noop"
	tracingnoop "github.com/primandproper/primitives-go/v2/observability/tracing/noop"
	"github.com/primandproper/primitives-go/v2/sms/twilio"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestConfig_ValidateWithContext(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{
			Provider: ProviderTwilio,
			Twilio:   &twilio.Config{AccountSID: t.Name(), AuthToken: t.Name()},
		}
		must.NoError(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("twilio provider requires config", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Provider: ProviderTwilio}
		must.Error(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("the noop provider needs no credentials", func(t *testing.T) {
		t.Parallel()

		// `env:",init"` leaves the Twilio sub-config non-nil, so this is the case
		// a validation.When guard alone would have failed: an unselected
		// provider's credentials must not be required.
		cfg := &Config{Provider: ProviderNoop, Twilio: &twilio.Config{}}
		must.NoError(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("unknown provider fails validation", func(t *testing.T) {
		t.Parallel()

		// ozzo renders the field errors into a validation.Errors map, which does
		// not carry an Unwrap, so the sentinel is readable in the message rather
		// than matchable here. NewSender checks the provider before validating
		// for exactly that reason, and the test below asserts what it returns.
		cfg := &Config{Provider: "twillio"}
		err := cfg.ValidateWithContext(t.Context())
		must.Error(t, err)
		test.StrContains(t, err.Error(), "unknown provider")
	})

	T.Run("empty provider is rejected", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Provider: ""}
		test.Error(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("the selection is read normalized", func(t *testing.T) {
		t.Parallel()

		// knownProvider and dispatch both trim and lowercase, so validation must
		// too, or a "TWILIO " would skip the very block it is about to use.
		cfg := &Config{
			Provider: "  TWILIO ",
			Twilio:   &twilio.Config{AccountSID: t.Name(), AuthToken: t.Name()},
		}
		must.NoError(t, cfg.ValidateWithContext(t.Context()))
	})
}

func TestNewSender(T *testing.T) {
	T.Parallel()

	T.Run("with the twilio provider", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{
			Provider: ProviderTwilio,
			Twilio:   &twilio.Config{AccountSID: t.Name(), AuthToken: t.Name()},
		}

		sender, err := NewSender(t.Context(), cfg, &http.Client{},
			WithLogger(loggingnoop.NewLogger()),
			WithTracerProvider(tracingnoop.NewTracerProvider()),
			WithMetricsProvider(metricsnoop.NewMetricsProvider()))
		must.NoError(t, err)
		test.NotNil(t, sender)
	})

	T.Run("with the noop provider", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(t.Context(), &Config{Provider: ProviderNoop}, &http.Client{})
		must.NoError(t, err)
		test.NotNil(t, sender)
	})

	// Verification codes disappearing because PROVIDER was unset is the failure
	// this guards; noop is still reachable, but only by name.
	T.Run("with an empty provider", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(t.Context(), &Config{Provider: ""}, &http.Client{})
		test.ErrorIs(t, err, errors.ErrUnknownProvider)
		test.Nil(t, sender)
	})

	T.Run("with an unknown provider", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(t.Context(), &Config{Provider: "vonage"}, &http.Client{})
		test.ErrorIs(t, err, errors.ErrUnknownProvider)
		test.Nil(t, sender)
	})

	T.Run("with a nil config", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(t.Context(), nil, &http.Client{})
		test.ErrorIs(t, err, errors.ErrNilInputParameter)
		test.Nil(t, sender)
	})

	// The nil-in-interface trap: twilio.NewSender returns its own concrete type,
	// and handing one straight back from a function returning sms.Sender would
	// turn a nil *twilio.Sender into a non-nil interface that passes a caller's
	// nil check and panics on the first send.
	T.Run("a failed provider build returns a genuinely nil sender", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{
			Provider: ProviderTwilio,
			Twilio:   &twilio.Config{AccountSID: t.Name(), AuthToken: t.Name()},
		}

		// A nil *http.Client is what twilio.NewSender refuses, and it is refused
		// after validation passes, which is the only way to reach the branch's
		// error path.
		sender, err := NewSender(t.Context(), cfg, nil)
		must.Error(t, err)
		test.Nil(t, sender)
		test.True(t, sender == nil)
	})
}

func TestKnownProvider(T *testing.T) {
	T.Parallel()

	T.Run("ignores case and surrounding space", func(t *testing.T) {
		t.Parallel()

		test.True(t, knownProvider(" Twilio "))
		test.True(t, knownProvider("NOOP"))
		test.False(t, knownProvider("twillio"))
		test.False(t, knownProvider(""))
	})
}
