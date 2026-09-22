// Package smscfg selects and builds an sms.Sender from configuration — Twilio,
// or the noop sender.
//
// Like emailcfg, it takes an *http.Client as a dependency rather than an
// option: the providers transport over HTTP, and the timeouts and transport a
// deployment wants there are not this package's to choose.
package smscfg

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/sms"
	"github.com/primandproper/primitives-go/v2/sms/noop"
	"github.com/primandproper/primitives-go/v2/sms/twilio"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

const (
	// ProviderTwilio represents Twilio.
	ProviderTwilio = "twilio"
	// ProviderNoop discards every message. It must be selected deliberately —
	// an unset or typo'd provider is an error, because outbound texts that
	// silently go nowhere are discovered by the people who never received their
	// verification code.
	ProviderNoop = "noop"
)

// providers are every provider this package implements. The dispatch switch and
// ValidateWithContext both read it, so they cannot drift apart.
var providers = []string{
	ProviderNoop,
	ProviderTwilio,
}

// knownProvider reports whether p names an implementation, ignoring case and
// surrounding space, exactly as the dispatch switch does.
func knownProvider(p string) bool {
	return slices.Contains(providers, strings.ToLower(strings.TrimSpace(p)))
}

type (
	// Config is the configuration structure.
	Config struct {
		Twilio   *twilio.Config `env:",init"    envPrefix:"TWILIO_"       json:"twilio,omitempty"   yaml:"twilio,omitempty"`
		Provider string         `env:"PROVIDER" json:"provider,omitempty" yaml:"provider,omitempty"`
	}
)

var _ validation.ValidatableWithContext = (*Config)(nil)

// ValidateWithContext validates a Config.
//
// The sub-config for a provider that was not selected is skipped rather than
// merely unguarded, for the reason emailcfg's is: ozzo validates any non-nil
// pointer to a Validatable once a field's rules have run, and `env:",init"`
// leaves every sub-config non-nil, so a validation.When guard alone would
// require Twilio credentials of a deployment that selected the noop.
//
// The selection is read normalized, matching dispatch: a "TWILIO" that
// knownProvider accepts and NewSender dispatches on would otherwise skip the
// very block it is about to use.
func (cfg *Config) ValidateWithContext(ctx context.Context) error {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))

	return validation.ValidateStructWithContext(
		ctx,
		cfg,
		validation.Field(&cfg.Provider, validation.Required, validation.By(func(any) error {
			if !knownProvider(cfg.Provider) {
				return errors.Wrapf(errors.ErrUnknownProvider, "sms provider %q", cfg.Provider)
			}

			return nil
		})),
		validation.Field(&cfg.Twilio, validation.Skip.When(provider != ProviderTwilio), validation.Required),
	)
}

// NewSender provides an sms.Sender from config.
func NewSender(ctx context.Context, cfg *Config, client *http.Client, opts ...Option) (sms.Sender, error) {
	if cfg == nil {
		return nil, errors.ErrNilInputParameter
	}

	o := newOptions(opts)

	// The provider is checked before the rest of the config so an unrecognized
	// one reports ErrUnknownProvider rather than whichever sub-config happened
	// to be missing as a consequence.
	if !knownProvider(cfg.Provider) {
		return nil, errors.Wrapf(errors.ErrUnknownProvider, "sms provider %q", cfg.Provider)
	}

	if err := cfg.ValidateWithContext(ctx); err != nil {
		return nil, errors.Wrap(err, "validating sms config")
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case ProviderTwilio:
		// Built into a variable and returned only once its error is known to be
		// nil. twilio.NewSender returns its own *twilio.Sender, and returning one
		// straight through would convert a nil pointer into a non-nil sms.Sender
		// on the error path — a value that passes a caller's nil check and panics
		// on the first send.
		sender, err := twilio.NewSender(cfg.Twilio, client,
			twilio.WithLogger(o.logger),
			twilio.WithTracerProvider(o.tracerProvider),
			twilio.WithMetricsProvider(o.metricsProvider))
		if err != nil {
			return nil, err
		}

		return sender, nil
	case ProviderNoop:
		return noop.NewSender(), nil
	default:
		return nil, errors.Wrapf(errors.ErrUnknownProvider, "sms provider %q", cfg.Provider)
	}
}
