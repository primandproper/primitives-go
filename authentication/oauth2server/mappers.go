package oauth2server

import (
	stderrors "errors"

	grpcerrors "github.com/primandproper/primitives-go/v2/errors/grpc"
	httperrors "github.com/primandproper/primitives-go/v2/errors/http"

	"google.golang.org/grpc/codes"
)

// The client-facing sentences for Verify's four refusals. They are written once
// because three things say them — the WWW-Authenticate challenge, HTTPMapper,
// and the gRPC interceptors — and a refusal described one way in a header and
// another way in an envelope is two answers to one question.
const (
	descriptionNoBearerToken    = "the request carries no bearer token"
	descriptionTokenUnusable    = "the token is expired, revoked, or unknown" //nolint:gosec // G101: a sentence about a token, not one
	descriptionAudienceMismatch = "the token was not issued for this resource"
	descriptionInsufficient     = "the token does not carry the required scope"
)

// RefusalDescription returns the sentence a client is told for a Verify
// refusal, and false for an error that is not one — a broken store, most
// likely, which is this server's fault and not something to describe to the
// caller.
//
// It is exported for a transport this package does not ship: authentication/
// oauth2server/grpc puts it in a status message, and a resource server with its
// own envelope puts it wherever that envelope keeps one. None of the sentences
// names the scope, the resource, or the token.
func RefusalDescription(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case stderrors.Is(err, ErrNoBearerToken):
		return descriptionNoBearerToken, true
	case stderrors.Is(err, ErrInsufficientScope):
		return descriptionInsufficient, true
	case stderrors.Is(err, ErrTokenAudienceMismatch):
		return descriptionAudienceMismatch, true
	case stderrors.Is(err, ErrNotFound):
		return descriptionTokenUnusable, true
	default:
		return "", false
	}
}

// HTTPMapper maps this package's resource-server sentinels onto errors/http's
// codes, for a handler that calls Verify itself and returns what it got through
// the ordinary envelope.
//
// It lives here rather than in errors/http's PlatformMapper because it cannot
// live there: this package imports routing, routing imports errors/http, and the
// reverse edge is a cycle. It is the same arrangement the domain tier's mappers
// use, and it is reachable the same way — once a composition root has handed it
// to RegisterHTTPErrorMapper.
//
// Verifier.Middleware does not need it. It writes its refusal with
// WriteChallenge, which knows the RFC 6750 header this envelope has no room for.
var HTTPMapper httperrors.HTTPErrorMapper = httpMapper{}

type httpMapper struct{}

func (httpMapper) Map(err error) (code httperrors.ErrorCode, msg string, ok bool) {
	msg, ok = RefusalDescription(err)
	if !ok {
		return "", "", false
	}

	// The one 403. Every other refusal is answered by presenting a better
	// credential; this one is a good credential that is not allowed to do this.
	if stderrors.Is(err, ErrInsufficientScope) {
		return httperrors.ErrUserIsNotAuthorized, msg, true
	}

	return httperrors.ErrAuthenticationFailed, msg, true
}

// GRPCMapper maps this package's resource-server sentinels onto gRPC codes, and
// covers the same four as HTTPMapper.
//
// Unauthenticated for every refusal but one, which is gRPC's own line between
// "we do not know who you are" and "we know, and you may not": a missing,
// unusable, or foreign token identifies nobody here. ErrInsufficientScope is the
// exception because it does identify somebody — a live token minted for this
// resource — who is not allowed this, and that is PermissionDenied.
//
// The interceptors in authentication/oauth2server/grpc consult it directly, so
// they answer with the right code whether or not it is registered. Registering
// it with RegisterGRPCErrorMapper is for everything else: a handler that calls
// Verify itself, and an error-encoding interceptor re-mapping a chain that
// carries one of these.
var GRPCMapper grpcerrors.GRPCErrorMapper = grpcMapper{}

type grpcMapper struct{}

func (grpcMapper) Map(err error) (code codes.Code, ok bool) {
	if _, ok = RefusalDescription(err); !ok {
		return codes.Unknown, false
	}

	if stderrors.Is(err, ErrInsufficientScope) {
		return codes.PermissionDenied, true
	}

	return codes.Unauthenticated, true
}
