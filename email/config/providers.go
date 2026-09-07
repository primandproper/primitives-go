package emailcfg

import (
	"context"
	"net/http"

	circuitbreakingcfg "github.com/primandproper/primitives-go/circuitbreaking/config"
	"github.com/primandproper/primitives-go/email"
	"github.com/primandproper/primitives-go/errors"
)

// NewEmailer provides an email.Emailer from a config.
func NewEmailer(ctx context.Context, cfg *Config, client *http.Client, opts ...Option) (email.Emailer, error) {
	o := newOptions(opts)

	circuitBreaker, err := cfg.CircuitBreaker.NewCircuitBreaker(ctx,
		circuitbreakingcfg.WithLogger(o.logger),
		circuitbreakingcfg.WithMetricsProvider(o.metricsProvider))
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize email circuit breaker")
	}

	return cfg.NewEmailer(ctx, client, circuitBreaker, opts...)
}
