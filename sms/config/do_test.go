package smscfg

import (
	"net/http"
	"testing"

	loggingnoop "github.com/primandproper/primitives-go/v2/observability/logging/noop"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
	metricsnoop "github.com/primandproper/primitives-go/v2/observability/metrics/noop"
	tracingnoop "github.com/primandproper/primitives-go/v2/observability/tracing/noop"
	"github.com/primandproper/primitives-go/v2/sms"
	"github.com/primandproper/primitives-go/v2/sms/twilio"

	"github.com/samber/do/v2"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestRegisterSender(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{
			Provider: ProviderTwilio,
			Twilio:   &twilio.Config{AccountSID: t.Name(), AuthToken: t.Name()},
		}

		i := do.New()
		do.ProvideValue(i, t.Context())
		do.ProvideValue(i, loggingnoop.NewLogger())
		do.ProvideValue(i, tracingnoop.NewTracerProvider())
		do.ProvideValue[metrics.Provider](i, metricsnoop.NewMetricsProvider())
		do.ProvideValue(i, &http.Client{})
		do.ProvideValue(i, cfg)

		RegisterSender(i)

		sender, err := do.Invoke[sms.Sender](i)
		must.NoError(t, err)
		test.NotNil(t, sender)
	})

	// A container that registers no observability still wires up: absent means
	// noop, and InvokePillars distinguishes "nobody registered one" from "the
	// registered one failed to build".
	T.Run("without registered observability", func(t *testing.T) {
		t.Parallel()

		i := do.New()
		do.ProvideValue(i, t.Context())
		do.ProvideValue(i, &http.Client{})
		do.ProvideValue(i, &Config{Provider: ProviderNoop})

		RegisterSender(i)

		sender, err := do.Invoke[sms.Sender](i)
		must.NoError(t, err)
		test.NotNil(t, sender)
	})
}
