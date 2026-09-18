package sse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/primandproper/primitives-go/v2/eventstream"
	"github.com/primandproper/primitives-go/v2/observability"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestNewUpgrader(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		u := NewUpgrader()
		test.NotNil(t, u)
		test.SliceEmpty(t, u.retryFrame)
	})

	T.Run("with a reconnect delay", func(t *testing.T) {
		t.Parallel()

		u := NewUpgrader(WithReconnectDelay(mustReconnectDelay(t, 15*time.Second)))
		must.NotNil(t, u)
		test.EqOp(t, "retry: 15000\n\n", string(u.retryFrame))
	})

	// The whole reason the delay is a type: every value NewUpgrader could refuse
	// has already been refused by the time it could be named here, so the zero
	// one is the only one that reaches it and it means "absent".
	T.Run("the zero reconnect delay emits no frame", func(t *testing.T) {
		t.Parallel()

		u := NewUpgrader(WithReconnectDelay(ReconnectDelay{}))
		must.NotNil(t, u)
		test.SliceEmpty(t, u.retryFrame)
	})
}

// connect stands up a server that upgrades every request with u, and returns the
// server-side stream and the client's response. Both ends and the server are
// closed for the caller, in that order, so the handler unblocks before the
// server is torn down.
func connect(t *testing.T, u *Upgrader) (eventstream.EventStream, *http.Response) {
	t.Helper()

	streamReady := make(chan eventstream.EventStream, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stream, upgradeErr := u.UpgradeToEventStream(w, r)
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

	return stream, resp
}

// readFlush reads the bytes of one server-side flush off the response body.
func readFlush(t *testing.T, resp *http.Response) string {
	t.Helper()

	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	must.NoError(t, err)

	return string(buf[:n])
}

func TestUpgrader_UpgradeToEventStream(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		streamReady := make(chan eventstream.EventStream, 1)
		u := NewUpgrader()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, err := u.UpgradeToEventStream(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		defer server.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		defer resp.Body.Close()

		stream := <-streamReady
		must.NotNil(t, stream)
		defer stream.Close()

		test.EqOp(t, "text/event-stream", resp.Header.Get("Content-Type"))
		test.EqOp(t, "no-cache", resp.Header.Get("Cache-Control"))
		test.EqOp(t, "keep-alive", resp.Header.Get("Connection"))
	})

	T.Run("response writer does not support flushing", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		u := NewUpgrader()
		w := &nonFlushableResponseWriter{header: http.Header{}}
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)

		stream, err := u.UpgradeToEventStream(w, r)
		test.Nil(t, stream)
		test.Error(t, err)
		test.StrContains(t, err.Error(), "streaming not supported")
	})

	// The ordering is the point: the Flusher check is the only failure that
	// leaves a handler a status code to answer with, so it has to come before
	// the first byte reaches the wire.
	T.Run("the flusher check precedes the retry write", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		u := NewUpgrader(WithReconnectDelay(mustReconnectDelay(t, time.Second)))
		w := &nonFlushableResponseWriter{header: http.Header{}}
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)

		stream, err := u.UpgradeToEventStream(w, r)
		test.Nil(t, stream)
		test.Error(t, err)
		test.StrContains(t, err.Error(), "streaming not supported")
		test.EqOp(t, 0, w.body.Len())
	})

	T.Run("writes the retry field before any event", func(t *testing.T) {
		t.Parallel()

		u := NewUpgrader(WithReconnectDelay(mustReconnectDelay(t, 15*time.Second)))

		stream, resp := connect(t, u)

		// Read before sending anything: the hint rides out with the headers, which
		// is what makes it reach a stream that never sends an event.
		test.EqOp(t, "retry: 15000\n\n", readFlush(t, resp))

		must.NoError(t, stream.Send(t.Context(), &eventstream.Event{
			Type:    "update",
			Payload: json.RawMessage(`{}`),
		}))

		test.EqOp(t, "event: update\ndata: {}\n\n", readFlush(t, resp))
	})

	T.Run("emits no retry field when no delay is named", func(t *testing.T) {
		t.Parallel()

		u := NewUpgrader()

		stream, resp := connect(t, u)

		must.NoError(t, stream.Send(t.Context(), &eventstream.Event{
			Type:    "update",
			Payload: json.RawMessage(`{}`),
		}))

		// The first thing off the wire is the event, so a caller that named no
		// delay leaves the client on its own default.
		test.EqOp(t, "event: update\ndata: {}\n\n", readFlush(t, resp))
	})

	T.Run("truncates the delay to whole milliseconds", func(t *testing.T) {
		t.Parallel()

		u := NewUpgrader(WithReconnectDelay(mustReconnectDelay(t, 1500*time.Microsecond)))

		_, resp := connect(t, u)

		test.EqOp(t, "retry: 1\n\n", readFlush(t, resp))
	})

	// Per stream, not once per upgrader: the second connection has to get it too,
	// or a fleet reconnecting would be told once and never again.
	T.Run("every stream an upgrader produces carries the field", func(t *testing.T) {
		t.Parallel()

		u := NewUpgrader(WithReconnectDelay(mustReconnectDelay(t, 2*time.Second)))

		_, first := connect(t, u)
		test.EqOp(t, "retry: 2000\n\n", readFlush(t, first))

		_, second := connect(t, u)
		test.EqOp(t, "retry: 2000\n\n", readFlush(t, second))
	})

	// Flushable, so the Flusher check passes and the retry write is what fails.
	T.Run("reports a failure writing the retry field", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		u := NewUpgrader(WithReconnectDelay(mustReconnectDelay(t, time.Second)))
		w := &failingResponseWriter{header: http.Header{}}
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)

		stream, err := u.UpgradeToEventStream(w, r)
		test.Nil(t, stream)
		test.Error(t, err)
		test.StrContains(t, err.Error(), "writing reconnection time")
	})
}

func TestSSEStream_Send(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		streamReady := make(chan eventstream.EventStream, 1)
		u := NewUpgrader()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, err := u.UpgradeToEventStream(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		defer server.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		defer resp.Body.Close()

		stream := <-streamReady
		must.NotNil(t, stream)
		defer stream.Close()

		sendErr := stream.Send(t.Context(), &eventstream.Event{
			Type:    "test_event",
			Payload: json.RawMessage(`{"msg":"hello"}`),
		})
		must.NoError(t, sendErr)

		scanner := bufio.NewScanner(resp.Body)

		// Read "event: test_event"
		must.True(t, scanner.Scan())
		test.EqOp(t, "event: test_event", scanner.Text())

		// Read "data: {\"msg\":\"hello\"}"
		must.True(t, scanner.Scan())
		test.EqOp(t, `data: {"msg":"hello"}`, scanner.Text())

		// Read empty line (event separator)
		must.True(t, scanner.Scan())
		test.EqOp(t, "", scanner.Text())
	})

	T.Run("neutralizes newlines in the payload to prevent SSE injection", func(t *testing.T) {
		t.Parallel()

		streamReady := make(chan eventstream.EventStream, 1)
		u := NewUpgrader()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, err := u.UpgradeToEventStream(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		defer server.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		defer resp.Body.Close()

		stream := <-streamReady
		must.NotNil(t, stream)
		defer stream.Close()

		// A payload embedding a newline followed by a would-be control field must be
		// emitted as multiple "data:" lines, not injected as a separate "event:" field.
		sendErr := stream.Send(t.Context(), &eventstream.Event{
			Payload: json.RawMessage("line1\nevent: injected"),
		})
		must.NoError(t, sendErr)

		scanner := bufio.NewScanner(resp.Body)

		must.True(t, scanner.Scan())
		test.EqOp(t, "data: line1", scanner.Text())

		must.True(t, scanner.Scan())
		test.EqOp(t, "data: event: injected", scanner.Text())

		must.True(t, scanner.Scan())
		test.EqOp(t, "", scanner.Text())
	})

	T.Run("event without type", func(t *testing.T) {
		t.Parallel()

		streamReady := make(chan eventstream.EventStream, 1)
		u := NewUpgrader()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, err := u.UpgradeToEventStream(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		defer server.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		defer resp.Body.Close()

		stream := <-streamReady
		must.NotNil(t, stream)
		defer stream.Close()

		sendErr := stream.Send(t.Context(), &eventstream.Event{
			Payload: json.RawMessage(`{"x":1}`),
		})
		must.NoError(t, sendErr)

		scanner := bufio.NewScanner(resp.Body)

		// No "event:" line, just data
		must.True(t, scanner.Scan())
		test.EqOp(t, `data: {"x":1}`, scanner.Text())

		// Empty line (event separator)
		must.True(t, scanner.Scan())
		test.EqOp(t, "", scanner.Text())
	})

	T.Run("multiple events", func(t *testing.T) {
		t.Parallel()

		streamReady := make(chan eventstream.EventStream, 1)
		u := NewUpgrader()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, err := u.UpgradeToEventStream(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		defer server.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		defer resp.Body.Close()

		stream := <-streamReady
		must.NotNil(t, stream)
		defer stream.Close()

		for i, name := range []string{"first", "second", "third"} {
			sendErr := stream.Send(t.Context(), &eventstream.Event{
				Type:    "msg",
				Payload: json.RawMessage(`"` + name + `"`),
			})
			must.NoError(t, sendErr, must.Sprintf("send %d", i))
		}

		scanner := bufio.NewScanner(resp.Body)
		for _, name := range []string{"first", "second", "third"} {
			must.True(t, scanner.Scan())
			test.EqOp(t, "event: msg", scanner.Text())

			must.True(t, scanner.Scan())
			test.EqOp(t, `data: "`+name+`"`, scanner.Text())

			must.True(t, scanner.Scan())
			test.EqOp(t, "", scanner.Text())
		}
	})

	T.Run("send after close returns error", func(t *testing.T) {
		t.Parallel()

		streamReady := make(chan eventstream.EventStream, 1)
		u := NewUpgrader()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, err := u.UpgradeToEventStream(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		defer server.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		defer resp.Body.Close()

		stream := <-streamReady
		must.NotNil(t, stream)

		must.NoError(t, stream.Close())

		sendErr := stream.Send(t.Context(), &eventstream.Event{
			Type:    "test",
			Payload: json.RawMessage(`{}`),
		})
		test.Error(t, sendErr)
		test.StrContains(t, sendErr.Error(), "stream closed")
	})
}

func TestSSEStream_Done(T *testing.T) {
	T.Parallel()

	T.Run("closes on Close", func(t *testing.T) {
		t.Parallel()

		streamReady := make(chan eventstream.EventStream, 1)
		u := NewUpgrader()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, err := u.UpgradeToEventStream(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		defer server.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		defer resp.Body.Close()

		stream := <-streamReady
		must.NotNil(t, stream)

		done := stream.Done()
		must.NoError(t, stream.Close())

		select {
		case <-done:
			// expected: channel closed
		case <-time.After(time.Second):
			t.Fatalf("Done() channel was not closed after Close()")
		}
	})

	T.Run("closes on client disconnect", func(t *testing.T) {
		t.Parallel()

		streamReady := make(chan eventstream.EventStream, 1)
		u := NewUpgrader()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, err := u.UpgradeToEventStream(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		defer server.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)

		stream := <-streamReady
		must.NotNil(t, stream)

		// Close the client connection, which cancels the request context
		resp.Body.Close()

		// The done channel should close because the request context was cancelled
		select {
		case <-stream.Done():
			// expected
		case <-time.After(2 * time.Second):
			t.Fatalf("Done() channel was not closed after client disconnect")
		}
	})
}

func TestSSEStream_Close(T *testing.T) {
	T.Parallel()

	T.Run("idempotent", func(t *testing.T) {
		t.Parallel()

		streamReady := make(chan eventstream.EventStream, 1)
		u := NewUpgrader()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, err := u.UpgradeToEventStream(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		defer server.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		defer resp.Body.Close()

		stream := <-streamReady
		must.NotNil(t, stream)

		// Close should be idempotent (context.CancelFunc is safe to call multiple times)
		test.NoError(t, stream.Close())
		test.NoError(t, stream.Close())
	})
}

// nonFlushableResponseWriter is a minimal ResponseWriter that does NOT implement
// http.Flusher. It records what it was handed, so a test can assert that a
// refused upgrade wrote nothing and left the handler a status code to choose.
type nonFlushableResponseWriter struct {
	header http.Header
	body   bytes.Buffer
}

func (w *nonFlushableResponseWriter) Header() http.Header { return w.header }
func (w *nonFlushableResponseWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}
func (w *nonFlushableResponseWriter) WriteHeader(int) {}

// failingResponseWriter is a flushable ResponseWriter whose Write always fails.
type failingResponseWriter struct {
	header http.Header
}

func (w *failingResponseWriter) Header() http.Header       { return w.header }
func (w *failingResponseWriter) Write([]byte) (int, error) { return 0, errWriteFailed }
func (w *failingResponseWriter) WriteHeader(int)           {}
func (w *failingResponseWriter) Flush()                    {}

var errWriteFailed = errors.New("write failed")

// newFailingStream builds an sseStream whose writes always fail, with a
// RecordingObserver swapped in so a test can assert the failure was observed on
// the operation.
func newFailingStream() (*sseStream, *observability.RecordingObserver) {
	w := &failingResponseWriter{header: http.Header{}}
	obs := observability.NewRecordingObserver()
	return &sseStream{
		w:       w,
		flusher: w,
		done:    make(chan struct{}),
		o11y:    obs,
	}, obs
}

func TestSSEStream_Send_writeErrors(T *testing.T) {
	T.Parallel()

	T.Run("error writing event type", func(t *testing.T) {
		t.Parallel()

		s, obs := newFailingStream()

		err := s.Send(t.Context(), &eventstream.Event{
			Type:    "boom",
			Payload: json.RawMessage(`{}`),
		})
		test.Error(t, err)
		test.StrContains(t, err.Error(), "writing event type")

		op := obs.ObservedOperationWithData(t, map[string]any{})
		must.SliceLen(t, 1, op.Errors)
	})

	T.Run("error writing event data", func(t *testing.T) {
		t.Parallel()

		s, obs := newFailingStream()

		// Empty Type skips the type write and reaches the data write directly.
		err := s.Send(t.Context(), &eventstream.Event{
			Payload: json.RawMessage(`{}`),
		})
		test.Error(t, err)
		test.StrContains(t, err.Error(), "writing event data")

		op := obs.ObservedOperationWithData(t, map[string]any{})
		must.SliceLen(t, 1, op.Errors)
	})
}

func TestSSEStream_Send_verifies_SSE_format(T *testing.T) {
	T.Parallel()

	T.Run("output is valid SSE", func(t *testing.T) {
		t.Parallel()

		streamReady := make(chan eventstream.EventStream, 1)
		u := NewUpgrader()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream, err := u.UpgradeToEventStream(w, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			streamReady <- stream
			<-stream.Done()
		}))
		defer server.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		must.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		defer resp.Body.Close()

		stream := <-streamReady
		must.NotNil(t, stream)
		defer stream.Close()

		sendErr := stream.Send(t.Context(), &eventstream.Event{
			Type:    "update",
			Payload: json.RawMessage(`{"id":"abc","status":"done"}`),
		})
		must.NoError(t, sendErr)

		// Read raw bytes and verify the exact SSE format
		buf := make([]byte, 4096)
		n, readErr := resp.Body.Read(buf)
		must.NoError(t, readErr)

		output := string(buf[:n])
		expected := "event: update\ndata: {\"id\":\"abc\",\"status\":\"done\"}\n\n"
		test.EqOp(t, expected, output)
	})
}
