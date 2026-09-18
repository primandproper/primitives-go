package eventstreamcfg

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/eventstream"
	"github.com/primandproper/primitives-go/v2/eventstream/sse"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestConfig_ValidateWithContext(T *testing.T) {
	T.Parallel()

	T.Run("SSE provider", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		cfg := &Config{
			Provider: ProviderSSE,
		}

		test.NoError(t, cfg.ValidateWithContext(ctx))
	})

	// Every field of websocket.Config has a default and NewUpgrader documents a
	// nil config as "use them", so naming the provider and nothing else is a
	// configured websocket rather than a missing one.
	T.Run("WebSocket provider without a websocket block", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		cfg := &Config{
			Provider: ProviderWebSocket,
		}

		test.NoError(t, cfg.ValidateWithContext(ctx))
	})

	// An unset provider used to validate clean and then be refused by both
	// constructors, which is the one config validation had nothing to say about.
	T.Run("unset provider", func(t *testing.T) {
		t.Parallel()

		test.Error(t, (&Config{}).ValidateWithContext(t.Context()))
	})

	T.Run("invalid provider", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		cfg := &Config{
			Provider: "invalid",
		}

		test.Error(t, cfg.ValidateWithContext(ctx))
	})
}

func TestNewEventStreamUpgrader(T *testing.T) {
	T.Parallel()

	T.Run("SSE", func(t *testing.T) {
		t.Parallel()

		upgrader, err := NewEventStreamUpgrader(
			t.Context(),
			&Config{
				Provider: ProviderSSE,
			},
			nil,
		)

		must.NoError(t, err)
		test.NotNil(t, upgrader)
	})

	T.Run("WebSocket", func(t *testing.T) {
		t.Parallel()

		upgrader, err := NewEventStreamUpgrader(
			t.Context(),
			&Config{
				Provider: ProviderWebSocket,
			},
			nil,
		)

		must.NoError(t, err)
		test.NotNil(t, upgrader)
	})

	T.Run("invalid provider", func(t *testing.T) {
		t.Parallel()

		_, err := NewEventStreamUpgrader(t.Context(), &Config{}, nil)

		test.ErrorIs(t, err, platformerrors.ErrUnknownProvider)
	})

	// The nil assertion is the load-bearing half. A *sse.Upgrader returned
	// straight into this function's interface result would arrive non-nil on the
	// error path, and a caller checking the upgrader rather than the error would
	// hold something that panics on first use.
	T.Run("SSE with an option the upgrader refuses", func(t *testing.T) {
		t.Parallel()

		upgrader, err := NewEventStreamUpgrader(
			t.Context(),
			&Config{Provider: ProviderSSE},
			WithSSEOptions(sse.WithReconnectDelay(0)),
		)

		test.Nil(t, upgrader)
		test.ErrorIs(t, err, sse.ErrInvalidReconnectDelay)
	})

	// End to end, because what comes back is the interface: the only place the
	// passthrough is observable is the wire.
	T.Run("SSE options reach the upgrader", func(t *testing.T) {
		t.Parallel()

		upgrader, err := NewEventStreamUpgrader(
			t.Context(),
			&Config{Provider: ProviderSSE},
			WithSSEOptions(sse.WithReconnectDelay(time.Second)),
		)
		must.NoError(t, err)
		must.NotNil(t, upgrader)

		streamReady := make(chan eventstream.EventStream, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, upgradeErr := upgrader.UpgradeToEventStream(w, r)
			if upgradeErr != nil {
				http.Error(w, upgradeErr.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		t.Cleanup(server.Close)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		stream := <-streamReady
		must.NotNil(t, stream)
		t.Cleanup(func() { _ = stream.Close() })

		buf := make([]byte, 64)
		n, readErr := resp.Body.Read(buf)
		must.NoError(t, readErr)
		test.EqOp(t, "retry: 1000\n\n", string(buf[:n]))
	})
}

func TestNewBidirectionalEventStreamUpgrader(T *testing.T) {
	T.Parallel()

	T.Run("SSE returns error", func(t *testing.T) {
		t.Parallel()

		_, err := NewBidirectionalEventStreamUpgrader(
			t.Context(),
			&Config{
				Provider: ProviderSSE,
			},
			nil,
		)

		test.Error(t, err)
		test.StrContains(t, err.Error(), "SSE does not support bidirectional")
	})

	T.Run("WebSocket", func(t *testing.T) {
		t.Parallel()

		upgrader, err := NewBidirectionalEventStreamUpgrader(
			t.Context(),
			&Config{
				Provider: ProviderWebSocket,
			},
			nil,
		)

		must.NoError(t, err)
		test.NotNil(t, upgrader)
	})

	T.Run("invalid provider", func(t *testing.T) {
		t.Parallel()

		_, err := NewBidirectionalEventStreamUpgrader(t.Context(), &Config{}, nil)

		test.ErrorIs(t, err, platformerrors.ErrUnknownProvider)
	})

	// Ignored rather than validated: this constructor cannot build SSE at all, so
	// an SSE option is nothing for it to refuse.
	T.Run("SSE options are ignored", func(t *testing.T) {
		t.Parallel()

		_, err := NewBidirectionalEventStreamUpgrader(
			t.Context(),
			&Config{Provider: ProviderWebSocket},
			WithSSEOptions(sse.WithReconnectDelay(0)),
		)

		test.NoError(t, err)
	})
}
