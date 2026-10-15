package grpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
)

func newHealthServer(t *testing.T) *Server {
	t.Helper()

	srv, err := NewGRPCServer(t.Context(), &Config{}, nil, nil, []RegistrationFunc{
		func(s *grpc.Server) { grpc_health_v1.RegisterHealthServer(s, health.NewServer()) },
	})
	must.NoError(t, err)

	return srv
}

// newHealthCheckRequest builds the HTTP/2 request grpc-go's ServeHTTP requires,
// carrying one length-prefixed HealthCheckRequest.
func newHealthCheckRequest(t *testing.T) *http.Request {
	t.Helper()

	payload, err := proto.Marshal(&grpc_health_v1.HealthCheckRequest{})
	must.NoError(t, err)

	body := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(body[1:], uint32(len(payload)))
	body = append(body, payload...)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/grpc.health.v1.Health/Check", bytes.NewReader(body))
	req.Proto, req.ProtoMajor, req.ProtoMinor = "HTTP/2.0", 2, 0
	req.Header.Set("Content-Type", "application/grpc")

	return req
}

func TestServer_ServeHTTP(T *testing.T) {
	T.Parallel()

	T.Run("serves a gRPC call", func(t *testing.T) {
		t.Parallel()

		srv := newHealthServer(t)

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, newHealthCheckRequest(t))

		res := rec.Result()
		must.NoError(t, res.Body.Close())

		test.EqOp(t, http.StatusOK, res.StatusCode)
		test.EqOp(t, "0", res.Trailer.Get("Grpc-Status"))
	})

	T.Run("answers UNAVAILABLE once Shutdown has begun", func(t *testing.T) {
		t.Parallel()

		srv := newHealthServer(t)
		must.NoError(t, srv.Shutdown(t.Context()))

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, newHealthCheckRequest(t))

		test.EqOp(t, http.StatusOK, rec.Code)
		test.EqOp(t, "application/grpc", rec.Header().Get("Content-Type"))
		test.EqOp(t, "14", rec.Header().Get("Grpc-Status"))
	})

	// Over HTTP/2 the stalled read ends when grpc-go closes the request body, so
	// this holds without httpCalls.abort; it pins that the deadline holds on
	// HTTP/2 too. The HTTP/1.1 case, where only abort ends the read, is in
	// server/grpcweb, which is the path HTTP/1.1 calls take.
	T.Run("a call stalled mid-body does not hold Shutdown past its deadline", func(t *testing.T) {
		t.Parallel()

		srv := newHealthServer(t)

		var h2c http.Protocols
		h2c.SetUnencryptedHTTP2(true)

		hs := httptest.NewUnstartedServer(srv)
		hs.Config.Protocols = &h2c
		hs.Start()
		t.Cleanup(hs.Close)

		transport := &http.Transport{Protocols: &h2c}
		t.Cleanup(transport.CloseIdleConnections)

		// The body promises 100 bytes and delivers three, then the client goes
		// quiet with the stream open.
		body, stall := io.Pipe()
		t.Cleanup(func() { _ = stall.Close() })

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, hs.URL+"/grpc.health.v1.Health/Check", body)
		must.NoError(t, err)
		req.ContentLength = 100
		req.Header.Set("Content-Type", "application/grpc")

		go func() {
			if resp, doErr := transport.RoundTrip(req); doErr == nil {
				_ = resp.Body.Close()
			}
		}()

		_, err = stall.Write([]byte{0, 0, 0})
		must.NoError(t, err)

		// The pipe's write returning means the transport has the bytes; this
		// gives the server time to admit the call and block reading the rest.
		time.Sleep(100 * time.Millisecond)

		const deadline = 200 * time.Millisecond
		ctx, cancel := context.WithTimeout(t.Context(), deadline)
		defer cancel()

		start := time.Now()
		stopped := make(chan error, 1)
		go func() { stopped <- srv.Shutdown(ctx) }()

		select {
		case err = <-stopped:
		case <-time.After(deadline + 2*time.Second):
			t.Fatal("Shutdown was still blocked well past its deadline")
		}

		test.ErrorIs(t, err, context.DeadlineExceeded)
		test.Less(t, deadline+500*time.Millisecond, time.Since(start))
	})
}

func TestServer_GetServiceInfo(T *testing.T) {
	T.Parallel()

	T.Run("reports the registered services", func(t *testing.T) {
		t.Parallel()

		info := newHealthServer(t).GetServiceInfo()

		must.MapContainsKey(t, info, "grpc.health.v1.Health")

		streaming := map[string]bool{}
		for _, m := range info["grpc.health.v1.Health"].Methods {
			streaming[m.Name] = m.IsServerStream
		}
		test.False(t, streaming["Check"])
		test.True(t, streaming["Watch"])
	})
}
