package mcp

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/url"
	"slices"

	"github.com/primandproper/primitives-go/v2/authentication/oauth2server"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// ExtraAccessToken is the auth.TokenInfo Extra entry the verified
// *oauth2server.AccessToken is carried under. Read it with AccessTokenFrom.
const ExtraAccessToken = "oauth2server.access_token" //nolint:gosec // G101: a map key naming where a token is kept, not a credential

var (
	// ErrNilVerifier indicates an adapter was built without a Verifier. There is
	// no default: an MCP endpoint with nothing to verify against would either
	// admit everything or refuse everything, and neither is a guard.
	ErrNilVerifier = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil oauth2 verifier")

	// ErrNilHandler indicates Protect was handed no MCP handler to protect.
	ErrNilHandler = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil mcp handler")
)

// TokenVerifier adapts v to the MCP SDK's verifier, for a deployment that mounts
// the SDK's auth.RequireBearerToken itself rather than using Protect.
//
// A refusal from Verify comes back as auth.ErrInvalidToken with the
// oauth2server sentinel joined to it, so the SDK answers 401 and a caller can
// still tell an expired token from one minted for another resource. A store
// that broke is returned as itself, which the SDK answers 500 — the client's
// credential is not what went wrong.
//
// It takes no scopes, and that is deliberate. The SDK's verifier has one way to
// say no, auth.ErrInvalidToken, and the SDK answers it 401; a scope refusal
// passed through it would tell a client holding a good token to go and get
// another one. The SDK makes the 401/403 split itself, from the Scopes in its
// options and the scopes this hands back, so required scopes go there — see
// BearerTokenOptions.
//
// A token with no audience is refused, as Verify refuses it. There is no
// option to accept one.
func TokenVerifier(v *oauth2server.Verifier) (auth.TokenVerifier, error) {
	if v == nil {
		return nil, ErrNilVerifier
	}

	return func(ctx context.Context, bearer string, _ *http.Request) (*auth.TokenInfo, error) {
		token, err := v.Verify(ctx, bearer)
		if err != nil {
			if refused(err) {
				return nil, stderrors.Join(auth.ErrInvalidToken, err)
			}

			return nil, err
		}

		return tokenInfo(token), nil
	}, nil
}

// BearerTokenOptions builds the options auth.RequireBearerToken takes, with the
// resource metadata URL read off v rather than configured beside it: a 401 that
// points a client at a document naming some other resource is how a client ends
// up holding a token this server refuses.
//
// requiredScopes become the options' Scopes, which is where the SDK makes its
// 403. Protect is the stricter composition — its challenges carry an RFC 6750
// error code the SDK's do not — and is the one to reach for unless something
// else already owns the bearer middleware.
func BearerTokenOptions(v *oauth2server.Verifier, requiredScopes ...string) (*auth.RequireBearerTokenOptions, error) {
	if v == nil {
		return nil, ErrNilVerifier
	}

	return &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: v.Metadata().URL(),
		Scopes:              slices.Clone(requiredScopes),
	}, nil
}

// Protect wraps an MCP HTTP handler with bearer authentication over v, and
// serves v's RFC 9728 document at the URL its challenges point to.
//
// The guard is v.Middleware, so the refusals are Verify's and keep their split:
// 401 with an invalid_token challenge for a token that is unknown, expired or
// minted for another resource, 401 with a bare challenge for a request that
// carried none, 403 with insufficient_scope and the scopes required for a token
// that lacks one. Every challenge names v.Metadata().URL().
//
// Behind the guard the SDK's own bearer middleware runs over the token already
// verified, without a second store lookup, so that auth.TokenInfoFromContext
// works and the streamable transport binds each session to the token's subject;
// a tool reads the token itself with AccessTokenFrom(req.Extra.TokenInfo).
//
// The document is served on GET at the path of v.Metadata().URL(), which for a
// resource with a path — https://api.example/mcp — is under that path. Mount
// the result on a pattern that reaches it ("/mcp/", not only "/mcp"), or mount
// v.Metadata().Handler() there separately.
func Protect(v *oauth2server.Verifier, handler http.Handler, requiredScopes ...string) (http.Handler, error) {
	if v == nil {
		return nil, ErrNilVerifier
	}

	if handler == nil {
		return nil, ErrNilHandler
	}

	metadataURL, err := url.Parse(v.Metadata().URL())
	if err != nil {
		return nil, platformerrors.Wrap(err, "parsing resource metadata URL")
	}

	metadataPath := metadataURL.EscapedPath()
	metadata := v.Metadata().Handler()
	protected := v.Middleware(requiredScopes...)(auth.RequireBearerToken(verified, nil)(handler))

	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		if req.URL.EscapedPath() != metadataPath {
			protected.ServeHTTP(res, req)

			return
		}

		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			res.Header().Set("Allow", "GET, HEAD")
			res.WriteHeader(http.StatusMethodNotAllowed)

			return
		}

		metadata.ServeHTTP(res, req)
	}), nil
}

// AccessTokenFrom reads the verified token out of the TokenInfo the SDK hands a
// tool as req.Extra.TokenInfo, for a per-tool decision — a scope one tool needs
// and the others do not — made on the record v already looked up.
//
// The second return is false for a TokenInfo this package did not build, which
// includes nil: a tool served without Protect or TokenVerifier in front of it.
func AccessTokenFrom(info *auth.TokenInfo) (*oauth2server.AccessToken, bool) {
	if info == nil {
		return nil, false
	}

	token, ok := info.Extra[ExtraAccessToken].(*oauth2server.AccessToken)

	return token, ok && token != nil
}

// verified is the SDK verifier Protect runs behind v.Middleware: the token is
// already checked and on the context, so this only translates it.
func verified(ctx context.Context, _ string, _ *http.Request) (*auth.TokenInfo, error) {
	token, ok := oauth2server.TokenFromContext(ctx)
	if !ok || token == nil {
		// Unreachable behind Middleware. If it is reached, refusing is the only
		// answer that does not admit an unverified request.
		return nil, auth.ErrInvalidToken
	}

	return tokenInfo(token), nil
}

// refused reports whether err is one of Verify's refusals of the credential,
// as opposed to a failure to check it.
func refused(err error) bool {
	return stderrors.Is(err, oauth2server.ErrNoBearerToken) ||
		stderrors.Is(err, oauth2server.ErrNotFound) ||
		stderrors.Is(err, oauth2server.ErrTokenAudienceMismatch) ||
		stderrors.Is(err, oauth2server.ErrInsufficientScope)
}

// tokenInfo is the SDK's view of a verified token.
//
// UserID is the subject, which the streamable transport compares on every
// request of a session: a session opened under one subject cannot be continued
// with another's token.
func tokenInfo(token *oauth2server.AccessToken) *auth.TokenInfo {
	return &auth.TokenInfo{
		Scopes:     slices.Clone(token.Scopes),
		Expiration: token.ExpiresAt,
		UserID:     token.Subject.ID,
		Extra:      map[string]any{ExtraAccessToken: token},
	}
}
