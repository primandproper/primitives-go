package grpc

import (
	"context"
	"slices"

	"github.com/primandproper/primitives-go/v2/authentication/oauth2server"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

// AuthorizationMetadataKey is the incoming metadata entry the bearer credential
// is read from. gRPC carries HTTP/2 headers as lower-case metadata, so this is
// the Authorization header a client's per-RPC credentials already send.
const AuthorizationMetadataKey = "authorization"

// ErrNilVerifier indicates an interceptor was built without a Verifier. There is
// no default: an interceptor with nothing to verify against would either admit
// everything or refuse everything, and neither is a guard.
var ErrNilVerifier = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil oauth2 verifier")

// NewUnaryServerInterceptor admits only unary calls carrying a live token
// minted for the Verifier's resource and holding every scope in
// requiredScopes. It is Verifier.Middleware for gRPC.
//
// A handler underneath reads what was verified with
// oauth2server.TokenFromContext — the same accessor the HTTP side uses, so a
// handler shared between the two transports reads it one way.
func NewUnaryServerInterceptor(verifier *oauth2server.Verifier, requiredScopes ...string) (grpc.UnaryServerInterceptor, error) {
	if verifier == nil {
		return nil, ErrNilVerifier
	}

	required := slices.Clone(requiredScopes)

	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := verify(ctx, verifier, required)
		if err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}, nil
}

// NewStreamServerInterceptor is NewUnaryServerInterceptor for streams.
//
// A stream is verified once, when it opens. Nothing re-checks mid-stream, so a
// long-lived stream outlives the revocation of the token that opened it — the
// property a unary call has for the length of one call, over a longer window.
func NewStreamServerInterceptor(verifier *oauth2server.Verifier, requiredScopes ...string) (grpc.StreamServerInterceptor, error) {
	if verifier == nil {
		return nil, ErrNilVerifier
	}

	required := slices.Clone(requiredScopes)

	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := verify(ss.Context(), verifier, required)
		if err != nil {
			return err
		}

		return handler(srv, &verifiedStream{ServerStream: ss, ctx: ctx})
	}, nil
}

// verify is the single copy of the check both interceptors make, so the unary
// and stream paths cannot drift into different rules.
func verify(ctx context.Context, verifier *oauth2server.Verifier, required []string) (context.Context, error) {
	token, err := verifier.Verify(ctx, BearerFromIncoming(ctx), required...)
	if err != nil {
		return ctx, Refusal(err)
	}

	return oauth2server.ContextWithToken(ctx, token), nil
}

// Refusal shapes a Verify error as the status a client is answered with: the
// code oauth2server.GRPCMapper gives it, and oauth2server.RefusalDescription's
// sentence as the message.
//
// It is exported because Verify is, for the same reason Verifier.WriteChallenge
// is: a handler that verifies for itself — per-method scopes the interceptor
// was not built with, say — still wants the refusal answered the same way.
//
// The error it returns still is the error it was given. errors.Is matches the
// oauth2server sentinel underneath, and an error-encoding interceptor installed
// outside this one has the chain to encode. A Verify error that is not a
// refusal — a store that broke — is Internal, with a message naming nothing,
// because the caller's credential is not what went wrong.
func Refusal(err error) error {
	if err == nil {
		return nil
	}

	code, ok := oauth2server.GRPCMapper.Map(err)
	if !ok {
		code = codes.Internal
	}

	description, _ := oauth2server.RefusalDescription(err)

	return observability.GRPCStatusError(err, code, description)
}

// BearerFromIncoming reads the bearer credential out of a call's incoming
// metadata, and yields the empty string — which Verify refuses as
// oauth2server.ErrNoBearerToken — when there is none.
//
// The first authorization entry is the one read, as net/http's Header.Get reads
// the first Authorization header; the parse is oauth2server's own, so the two
// transports agree on what a bearer credential looks like.
func BearerFromIncoming(ctx context.Context) string {
	values := metadata.ValueFromIncomingContext(ctx, AuthorizationMetadataKey)
	if len(values) == 0 {
		return ""
	}

	return oauth2server.BearerFromAuthorization(values[0])
}

// verifiedStream is a ServerStream whose context carries the verified token.
// grpc.ServerStream offers no other way to hand a stream handler a context.
type verifiedStream struct {
	grpc.ServerStream

	ctx context.Context
}

func (s *verifiedStream) Context() context.Context { return s.ctx }
