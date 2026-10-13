package grpc

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"

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
