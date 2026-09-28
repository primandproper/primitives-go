package grpc

import (
	"context"
	"fmt"
	"runtime/debug"

	perrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability/logging"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// recoveredPanicMessage is the status message a client sees for a panicking
// handler. It is the code's own name, which is what errors/grpc puts on the
// wire for an error it has nothing client-safe to say about; the panic value
// is internal text and stays in the log.
var recoveredPanicMessage = codes.Internal.String()

// recoverRPC turns a recovered panic into the codes.Internal status the RPC
// returns, logging the value and the stack that produced it.
func recoverRPC(l logging.Logger, kind, fullMethod string, recovered any) error {
	l.WithValues(map[string]any{
		"rpc.method": fullMethod,
		"rpc.kind":   kind,
		"panic":      fmt.Sprint(recovered),
		"stack":      string(debug.Stack()),
	}).Error("rpc panicked", perrors.Errorf("panic: %v", recovered))

	return status.Error(codes.Internal, recoveredPanicMessage)
}

// RecoveryInterceptor turns a panicking unary handler into a codes.Internal
// status instead of a dead process.
//
// grpc-go does not recover on a handler's behalf, so without this one nil map
// write in one RPC takes every other in-flight RPC down with it. The panic
// value and its stack are logged; the client is told Internal and nothing
// more, since the value is whatever the handler happened to panic with.
//
// NewGRPCServer installs it outermost by default, so a panic in an interceptor
// is caught as well as one in a handler; WithoutRecovery opts out.
func RecoveryInterceptor(logger logging.Logger) grpc.UnaryServerInterceptor {
	l := logging.EnsureLogger(logger)

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				resp, err = nil, recoverRPC(l, "unary", info.FullMethod, r)
			}
		}()

		return handler(ctx, req)
	}
}

// StreamRecoveryInterceptor is RecoveryInterceptor for streaming RPCs.
func StreamRecoveryInterceptor(logger logging.Logger) grpc.StreamServerInterceptor {
	l := logging.EnsureLogger(logger)

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = recoverRPC(l, "stream", info.FullMethod, r)
			}
		}()

		return handler(srv, ss)
	}
}
