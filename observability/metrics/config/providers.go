package metricscfg

import (
	"context"

	"github.com/primandproper/primitives-go/v2/observability/metrics"
)

// NewMetricsProvider provides a metrics.Provider from a config.
func NewMetricsProvider(ctx context.Context, c *Config, opts ...Option) (metrics.Provider, error) {
	return c.NewMetricsProvider(ctx, opts...)
}
