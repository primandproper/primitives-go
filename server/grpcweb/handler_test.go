package grpcweb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	grpcserver "github.com/primandproper/primitives-go/v2/server/grpc"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	checkMethod = "/grpc.health.v1.Health/Check"
	watchMethod = "/grpc.health.v1.Health/Watch"
)

type healthService struct {
	grpc_health_v1.UnimplementedHealthServer
	check   func(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error)
	watched chan struct{}
}

func (s *healthService) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return s.check(ctx, req)
}

func (s *healthService) Watch(*grpc_health_v1.HealthCheckRequest, grpc.ServerStreamingServer[grpc_health_v1.HealthCheckResponse]) error {
	close(s.watched)
	return nil
}

func newHealthService(check func(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error)) *healthService {
	return &healthService{check: check, watched: make(chan struct{})}
}

func serving(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

// newTestServer serves svc through NewHandler over an httptest server, which
// speaks HTTP/1.1.
func newTestServer(t *testing.T, svc *healthService, interceptors ...grpc.UnaryServerInterceptor) (srv *grpcserver.Server, url string) {
	t.Helper()

	srv, err := grpcserver.NewGRPCServer(t.Context(), &grpcserver.Config{}, interceptors, nil, []grpcserver.RegistrationFunc{
		func(s *grpc.Server) { grpc_health_v1.RegisterHealthServer(s, svc) },
	})
	must.NoError(t, err)

	hs := httptest.NewServer(NewHandler(srv))
	t.Cleanup(hs.Close)

	return srv, hs.URL
}

func send(ctx context.Context, url, method, contentType string, msg proto.Message, header http.Header) (*http.Response, error) {
	payload, err := proto.Marshal(msg)
	if err != nil {
		return nil, err
	}

	body := make([]byte, frameHeaderSize, frameHeaderSize+len(payload))
	binary.BigEndian.PutUint32(body[1:], uint32(len(payload)))
	body = append(body, payload...)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	maps.Copy(req.Header, header)
	req.Header.Set("Content-Type", contentType)

	return http.DefaultClient.Do(req)
}

type webResponse struct {
	header   http.Header
	trailers map[string]string
	messages [][]byte
	proto    int
}

// readResponse splits a gRPC-web body into its message frames and its trailer
// frame, requiring the trailer frame to come last.
func readResponse(t *testing.T, resp *http.Response) *webResponse {
	t.Helper()

	defer func() { must.NoError(t, resp.Body.Close()) }()

	must.EqOp(t, http.StatusOK, resp.StatusCode)

	raw, err := io.ReadAll(resp.Body)
	must.NoError(t, err)

	out := &webResponse{header: resp.Header, proto: resp.ProtoMajor}
	for len(raw) > 0 {
		must.Nil(t, out.trailers, must.Sprint("a frame followed the trailer frame"))
		must.GreaterEq(t, frameHeaderSize, len(raw))

		flag, n := raw[0], int(binary.BigEndian.Uint32(raw[1:frameHeaderSize]))
		must.GreaterEq(t, frameHeaderSize+n, len(raw))

		frame := raw[frameHeaderSize : frameHeaderSize+n]
		raw = raw[frameHeaderSize+n:]

		if flag&trailerFrameFlag == 0 {
			out.messages = append(out.messages, frame)
			continue
		}

		out.trailers = map[string]string{}
		for line := range strings.SplitSeq(string(frame), "\r\n") {
			if line == "" {
				continue
			}

			k, v, ok := strings.Cut(line, ": ")
			must.True(t, ok, must.Sprintf("malformed trailer line %q", line))
			out.trailers[k] = v
		}
	}

	must.NotNil(t, out.trailers, must.Sprint("no trailer frame"))

	return out
}

func call(t *testing.T, url, method string, header http.Header) *webResponse {
	t.Helper()

	resp, err := send(t.Context(), url, method, "application/grpc-web+proto", &grpc_health_v1.HealthCheckRequest{}, header)
	must.NoError(t, err)

	return readResponse(t, resp)
}

func TestHandler_ServeHTTP(T *testing.T) {
	T.Parallel()

	T.Run("an OK call is a message frame then a trailer frame", func(t *testing.T) {
		t.Parallel()

		_, url := newTestServer(t, newHealthService(serving))

		out := call(t, url, checkMethod, nil)

		test.EqOp(t, "application/grpc-web+proto", out.header.Get("Content-Type"))
		must.SliceLen(t, 1, out.messages)

		var msg grpc_health_v1.HealthCheckResponse
		must.NoError(t, proto.Unmarshal(out.messages[0], &msg))
		test.EqOp(t, grpc_health_v1.HealthCheckResponse_SERVING, msg.GetStatus())

		test.EqOp(t, "0", out.trailers["grpc-status"])
		test.EqOp(t, "", out.header.Get("Grpc-Status"))
	})

	T.Run("a request over HTTP/1.1 is served", func(t *testing.T) {
		t.Parallel()

		_, url := newTestServer(t, newHealthService(serving))

		out := call(t, url, checkMethod, nil)

		test.EqOp(t, 1, out.proto)
		test.EqOp(t, "0", out.trailers["grpc-status"])
	})

	T.Run("application/grpc-web is served as well as the +proto form", func(t *testing.T) {
		t.Parallel()

		_, url := newTestServer(t, newHealthService(serving))

		resp, err := send(t.Context(), url, checkMethod, "application/grpc-web", &grpc_health_v1.HealthCheckRequest{}, nil)
		must.NoError(t, err)
		out := readResponse(t, resp)

		test.EqOp(t, "application/grpc-web", out.header.Get("Content-Type"))
		test.EqOp(t, "0", out.trailers["grpc-status"])
	})

	T.Run("a unary interceptor runs and sees the request's metadata", func(t *testing.T) {
		t.Parallel()

		seen := make(chan metadata.MD, 1)
		interceptor := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			seen <- md
			return handler(ctx, req)
		}

		_, url := newTestServer(t, newHealthService(serving), interceptor)

		out := call(t, url, checkMethod, http.Header{"Authorization": {"Bearer token"}})
		test.EqOp(t, "0", out.trailers["grpc-status"])

		md := <-seen
		test.Eq(t, []string{"Bearer token"}, md.Get("authorization"))
	})

	T.Run("header and trailer metadata reach the client", func(t *testing.T) {
		t.Parallel()

		_, url := newTestServer(t, newHealthService(func(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
			if err := grpc.SetHeader(ctx, metadata.Pairs("x-request-id", "abc")); err != nil {
				return nil, err
			}
			if err := grpc.SetTrailer(ctx, metadata.Pairs("x-request-cost", "7")); err != nil {
				return nil, err
			}

			return serving(ctx, req)
		}))

		out := call(t, url, checkMethod, nil)

		test.EqOp(t, "abc", out.header.Get("X-Request-Id"))
		test.EqOp(t, "7", out.trailers["x-request-cost"])
		test.EqOp(t, "", out.header.Get("X-Request-Cost"))
		test.EqOp(t, "0", out.trailers["grpc-status"])
	})

	T.Run("a trailers-only response carries its status as headers too", func(t *testing.T) {
		t.Parallel()

		_, url := newTestServer(t, newHealthService(func(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
			return nil, status.Error(codes.NotFound, "no such service")
		}))

		out := call(t, url, checkMethod, nil)

		test.SliceEmpty(t, out.messages)
		test.EqOp(t, "5", out.trailers["grpc-status"])
		test.EqOp(t, "no such service", out.trailers["grpc-message"])
		test.EqOp(t, "5", out.header.Get("Grpc-Status"))
		test.EqOp(t, "no such service", out.header.Get("Grpc-Message"))
	})

	T.Run("a non-OK status keeps its details byte for byte", func(t *testing.T) {
		t.Parallel()

		st, err := status.New(codes.FailedPrecondition, "100% not ready").WithDetails(&errdetails.ErrorInfo{
			Reason:   "NOT_READY",
			Domain:   "example.test",
			Metadata: map[string]string{"attempt": "3"},
		})
		must.NoError(t, err)

		_, url := newTestServer(t, newHealthService(func(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
			return nil, st.Err()
		}))

		out := call(t, url, checkMethod, nil)

		test.EqOp(t, "9", out.trailers["grpc-status"])
		// grpc-message is percent-encoded on the wire, and passed through so.
		test.EqOp(t, "100%25 not ready", out.trailers["grpc-message"])

		raw, err := base64.RawStdEncoding.DecodeString(out.trailers["grpc-status-details-bin"])
		must.NoError(t, err)

		var got spb.Status
		must.NoError(t, proto.Unmarshal(raw, &got))
		test.True(t, proto.Equal(st.Proto(), &got), test.Sprintf("details changed in transit: %v", &got))
	})

	T.Run("a streaming method is answered UNIMPLEMENTED without reaching the server", func(t *testing.T) {
		t.Parallel()

		svc := newHealthService(serving)
		_, url := newTestServer(t, svc)

		out := call(t, url, watchMethod, nil)

		test.SliceEmpty(t, out.messages)
		test.EqOp(t, "12", out.trailers["grpc-status"])
		test.EqOp(t, "12", out.header.Get("Grpc-Status"))

		select {
		case <-svc.watched:
			t.Fatal("the streaming method was invoked")
		default:
		}
	})

	T.Run("grpc-web-text is answered 415", func(t *testing.T) {
		t.Parallel()

		_, url := newTestServer(t, newHealthService(serving))

		resp, err := send(t.Context(), url, checkMethod, "application/grpc-web-text", &grpc_health_v1.HealthCheckRequest{}, nil)
		must.NoError(t, err)
		must.NoError(t, resp.Body.Close())

		test.EqOp(t, http.StatusUnsupportedMediaType, resp.StatusCode)
	})

	T.Run("a method other than POST is answered 405", func(t *testing.T) {
		t.Parallel()

		_, url := newTestServer(t, newHealthService(serving))

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url+checkMethod, http.NoBody)
		must.NoError(t, err)
		req.Header.Set("Content-Type", "application/grpc-web+proto")

		resp, err := http.DefaultClient.Do(req)
		must.NoError(t, err)
		must.NoError(t, resp.Body.Close())

		test.EqOp(t, http.StatusMethodNotAllowed, resp.StatusCode)
		test.EqOp(t, http.MethodPost, resp.Header.Get("Allow"))
	})

	T.Run("the server's graceful stop waits for an in-flight call", func(t *testing.T) {
		t.Parallel()

		entered, release := make(chan struct{}), make(chan struct{})
		srv, url := newTestServer(t, newHealthService(func(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
			close(entered)
			<-release
			return serving(ctx, req)
		}))

		type result struct {
			resp *http.Response
			err  error
		}
		responded := make(chan result, 1)
		go func() {
			resp, err := send(t.Context(), url, checkMethod, "application/grpc-web+proto", &grpc_health_v1.HealthCheckRequest{}, nil)
			responded <- result{resp: resp, err: err}
		}()

		<-entered

		stopped := make(chan error, 1)
		go func() { stopped <- srv.Shutdown(t.Context()) }()

		select {
		case err := <-stopped:
			t.Fatalf("Shutdown returned with a call in flight: %v", err)
		case <-time.After(50 * time.Millisecond):
		}

		close(release)

		res := <-responded
		must.NoError(t, res.err)
		out := readResponse(t, res.resp)
		must.SliceLen(t, 1, out.messages)
		test.EqOp(t, "0", out.trailers["grpc-status"])

		must.NoError(t, <-stopped)
	})

	T.Run("a call the server is stopped under is answered UNAVAILABLE", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{})
		srv, url := newTestServer(t, newHealthService(func(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
			close(entered)
			<-ctx.Done()
			return nil, status.FromContextError(ctx.Err()).Err()
		}))

		type result struct {
			resp *http.Response
			err  error
		}
		responded := make(chan result, 1)
		go func() {
			resp, err := send(t.Context(), url, checkMethod, "application/grpc-web+proto", &grpc_health_v1.HealthCheckRequest{}, nil)
			responded <- result{resp: resp, err: err}
		}()

		<-entered

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		test.ErrorIs(t, srv.Shutdown(ctx), context.Canceled)

		res := <-responded
		must.NoError(t, res.err)
		out := readResponse(t, res.resp)

		test.SliceEmpty(t, out.messages)
		test.EqOp(t, "14", out.trailers["grpc-status"])
		test.EqOp(t, "14", out.header.Get("Grpc-Status"))
	})
}

func TestEncodeTrailerFrame(T *testing.T) {
	T.Parallel()

	T.Run("lowercases names and writes one line per value", func(t *testing.T) {
		t.Parallel()

		frame := encodeTrailerFrame(http.Header{
			"Grpc-Status": {"0"},
			"X-Multi-Bin": {"YQ", "Yg"},
		})

		block := "grpc-status: 0\r\nx-multi-bin: YQ\r\nx-multi-bin: Yg\r\n"
		test.EqOp(t, trailerFrameFlag, frame[0])
		test.EqOp(t, uint32(len(block)), binary.BigEndian.Uint32(frame[1:frameHeaderSize]))
		test.EqOp(t, block, string(frame[frameHeaderSize:]))
	})
}
