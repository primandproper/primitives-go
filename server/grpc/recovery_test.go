package grpc

import (
	"context"
	"maps"
	"sync"
	"testing"

	"github.com/primandproper/primitives-go/v2/observability/logging"
	loggingnoop "github.com/primandproper/primitives-go/v2/observability/logging/noop"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// recordingLogger keeps the values of every Error line, which is where a
// recovered panic's value and stack go.
type recordingLogger struct {
	logging.Logger
	values map[string]any
	errors *[]map[string]any
	mu     *sync.Mutex
}

func newRecordingLogger() *recordingLogger {
	return &recordingLogger{Logger: loggingnoop.NewLogger(), errors: &[]map[string]any{}, mu: &sync.Mutex{}}
}

func (r *recordingLogger) WithValues(values map[string]any) logging.Logger {
	merged := map[string]any{}
	maps.Copy(merged, r.values)
	maps.Copy(merged, values)

	return &recordingLogger{Logger: r.Logger, values: merged, errors: r.errors, mu: r.mu}
}

func (r *recordingLogger) Error(string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.errors = append(*r.errors, r.values)
}

func (r *recordingLogger) lines() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), *r.errors...)
}

func TestRecoveryInterceptor(T *testing.T) {
	T.Parallel()

	info := &grpc.UnaryServerInfo{FullMethod: "/test/Method"}

	T.Run("a panic becomes Internal and is logged with its stack", func(t *testing.T) {
		t.Parallel()

		logger := newRecordingLogger()
		interceptor := RecoveryInterceptor(logger)

		result, err := interceptor(t.Context(), "request", info, func(context.Context, any) (any, error) {
			panic("scanning widget_catalog_entries")
		})

		test.Nil(t, result)
		test.EqOp(t, codes.Internal, status.Code(err))
		// The panic value is internal text and does not reach the client.
		test.EqOp(t, codes.Internal.String(), status.Convert(err).Message())

		lines := logger.lines()
		must.SliceLen(t, 1, lines)
		test.EqOp(t, "/test/Method", lines[0]["rpc.method"].(string))
		test.EqOp(t, "scanning widget_catalog_entries", lines[0]["panic"].(string))
		test.StrContains(t, lines[0]["stack"].(string), "recovery_test.go")
	})

	T.Run("a handler that returns is passed through", func(t *testing.T) {
		t.Parallel()

		interceptor := RecoveryInterceptor(nil)

		result, err := interceptor(t.Context(), "request", info, func(context.Context, any) (any, error) {
			return "result", errStub
		})

		test.EqOp(t, "result", result.(string))
		test.ErrorIs(t, err, errStub)
	})
}

func TestStreamRecoveryInterceptor(T *testing.T) {
	T.Parallel()

	info := &grpc.StreamServerInfo{FullMethod: "/test/Stream"}

	T.Run("a panic becomes Internal and is logged with its stack", func(t *testing.T) {
		t.Parallel()

		logger := newRecordingLogger()
		interceptor := StreamRecoveryInterceptor(logger)

		err := interceptor(nil, nil, info, func(any, grpc.ServerStream) error {
			panic(errStub)
		})

		test.EqOp(t, codes.Internal, status.Code(err))
		test.EqOp(t, codes.Internal.String(), status.Convert(err).Message())

		lines := logger.lines()
		must.SliceLen(t, 1, lines)
		test.EqOp(t, "stream", lines[0]["rpc.kind"].(string))
		test.EqOp(t, errStub.Error(), lines[0]["panic"].(string))
		test.StrContains(t, lines[0]["stack"].(string), "recovery_test.go")
	})

	T.Run("a handler that returns is passed through", func(t *testing.T) {
		t.Parallel()

		interceptor := StreamRecoveryInterceptor(nil)

		test.ErrorIs(t, interceptor(nil, nil, info, func(any, grpc.ServerStream) error { return errStub }), errStub)
		test.NoError(t, interceptor(nil, nil, info, func(any, grpc.ServerStream) error { return nil }))
	})
}

const panicMethod = "/platform.test.v1.Panic/Panic"

// panicServiceDesc routes through the interceptor chain, as generated code
// does, and its handler panics.
var panicServiceDesc = grpc.ServiceDesc{
	ServiceName: "platform.test.v1.Panic",
	HandlerType: (*any)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Panic",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				in := new(wrapperspb.BytesValue)
				if err := dec(in); err != nil {
					return nil, err
				}

				handler := func(context.Context, any) (any, error) { panic("handler bug") }
				if interceptor == nil {
					return handler(ctx, in)
				}

				return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: panicMethod}, handler)
			},
		},
	},
}

func TestNewGRPCServer_Recovery(T *testing.T) {
	T.Parallel()

	call := func(t *testing.T, interceptors ...grpc.UnaryServerInterceptor) {
		t.Helper()

		srv, err := NewGRPCServer(t.Context(), &Config{}, interceptors, nil,
			[]RegistrationFunc{func(s *grpc.Server) { s.RegisterService(&panicServiceDesc, struct{}{}) }})
		must.NoError(t, err)

		cc := dialEcho(t, serveEcho(t, srv))

		for range 2 {
			err = cc.Invoke(t.Context(), panicMethod, wrapperspb.Bytes(nil), new(wrapperspb.BytesValue))
			test.EqOp(t, codes.Internal, status.Code(err))
			test.EqOp(t, codes.Internal.String(), status.Convert(err).Message())
		}
	}

	T.Run("is installed by default, and the server outlives a panicking handler", func(t *testing.T) {
		t.Parallel()

		call(t)
	})

	T.Run("is outside the caller's interceptors", func(t *testing.T) {
		t.Parallel()

		// A panic in an interceptor the caller supplied is recovered too, which
		// is what installing recovery outermost buys.
		call(t, func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) {
			panic("interceptor bug")
		})
	})

	T.Run("WithoutRecovery leaves it out", func(t *testing.T) {
		t.Parallel()

		test.False(t, newOptions(nil).withoutRecovery)
		test.True(t, newOptions([]Option{WithoutRecovery()}).withoutRecovery)
	})
}
