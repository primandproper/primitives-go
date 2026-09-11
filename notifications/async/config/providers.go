package asynccfg

import (
	"context"

	"github.com/primandproper/primitives-go/v2/config/cfgnorm"
	"github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/notifications/async"
	"github.com/primandproper/primitives-go/v2/notifications/async/ably"
	"github.com/primandproper/primitives-go/v2/notifications/async/noop"
	"github.com/primandproper/primitives-go/v2/notifications/async/pusher"
	asyncsse "github.com/primandproper/primitives-go/v2/notifications/async/sse"
	asyncws "github.com/primandproper/primitives-go/v2/notifications/async/websocket"
)

// NewAsyncNotifier provides an AsyncNotifier based on configuration.
//
// One door, and this is it. The package used to export this alongside a
// (*Config).NewAsyncNotifier method holding the same body, which is two
// exported names for one behavior and two places for a caller to read a
// different contract. This is the shape every sibling seam's constructor takes
// — ctx, cfg, then options — so it is the one that survived.
//
// It takes a context so that the whole config goes through
// ValidateWithContext, of which the topology agreement was previously the only
// part this path ran — a pusher deployment with no credentials got as far as
// its first publish.
//
// Every branch assigns into a variable and returns only once its error is
// known to be nil: the provider packages hand back their own *Notifier, and
// returning one straight through would convert a nil pointer into a non-nil
// async.AsyncNotifier on the error path — a value that passes a caller's nil
// check and panics on the first publish.
func NewAsyncNotifier(ctx context.Context, cfg *Config, opts ...Option) (async.AsyncNotifier, error) {
	if cfg == nil {
		return nil, errors.ErrNilInputParameter
	}

	provider, err := cfgnorm.SelectProvider(cfg.Provider, providers, "async notifications provider")
	if err != nil {
		return nil, err
	}

	if err = cfg.ValidateWithContext(ctx); err != nil {
		return nil, errors.Wrap(err, "validating async notifications config")
	}

	o := newOptions(opts)
	logger, tracerProvider, metricsProvider := o.logger, o.tracerProvider, o.metricsProvider

	var notifier async.AsyncNotifier

	switch provider {
	case ProviderPusher:
		notifier, err = pusher.NewNotifier(cfg.Pusher, pusher.WithLogger(logger), pusher.WithTracerProvider(tracerProvider), pusher.WithMetricsProvider(metricsProvider))
	case ProviderAbly:
		notifier, err = ably.NewNotifier(cfg.Ably, ably.WithLogger(logger), ably.WithTracerProvider(tracerProvider), ably.WithMetricsProvider(metricsProvider))
	case ProviderWebSocket:
		notifier, err = asyncws.NewNotifier(cfg.WebSocket, asyncws.WithLogger(logger), asyncws.WithTracerProvider(tracerProvider))
	case ProviderSSE:
		notifier, err = asyncsse.NewNotifier(cfg.SSE, asyncsse.WithLogger(logger), asyncsse.WithTracerProvider(tracerProvider))
	case ProviderNoop:
		// Only by name. An unset provider never reaches here — SelectProvider
		// refuses it, because "notify nobody, forever" is a decision somebody
		// has to make.
		notifier, err = noop.NewAsyncNotifier()
	default:
		return nil, errors.Wrapf(errors.ErrUnknownProvider, "async notifications provider %q", cfg.Provider)
	}

	if err != nil {
		return nil, err
	}

	return notifier, nil
}
