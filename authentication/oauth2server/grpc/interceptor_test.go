package grpc

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/primandproper/primitives-go/v2/authentication/oauth2server"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	testResource  = "https://api.example/"
	otherResource = "https://mcp.example/"
	testIssuer    = "https://auth.example"
	testBearer    = "a-live-token"
	testMethod    = "/test.Recipes/Get"
)

var errStoreDown = platformerrors.New("store is down")

// tokens is a TokenAuthenticator with one answer: the token it holds for
// testBearer, or err for everything.
type tokens struct {
	token *oauth2server.AccessToken
	err   error
}

func (a tokens) Authenticate(_ context.Context, bearer string) (*oauth2server.AccessToken, error) {
	if a.err != nil {
		return nil, a.err
	}

	if bearer != testBearer {
		return nil, oauth2server.ErrNotFound
	}

	return a.token, nil
}

func liveToken(audience ...string) *oauth2server.AccessToken {
	return &oauth2server.AccessToken{
		ClientID: "client",
		Subject:  oauth2server.Subject{ID: "subject"},
		Scopes:   []string{"read"},
		Audience: audience,
	}
}

func newVerifier(t *testing.T, authenticator oauth2server.TokenAuthenticator) *oauth2server.Verifier {
	t.Helper()

	meta, err := oauth2server.NewResourceMetadata(testResource, []string{testIssuer})
	must.NoError(t, err)

	verifier, err := oauth2server.NewVerifier(meta, authenticator)
	must.NoError(t, err)

	return verifier
}

func withBearer(ctx context.Context, bearer string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(AuthorizationMetadataKey, "Bearer "+bearer))
}

// callUnary runs one unary call through an interceptor built over authenticator
// and reports what the handler saw, if it ran.
func callUnary(t *testing.T, ctx context.Context, authenticator oauth2server.TokenAuthenticator, scopes ...string) (*oauth2server.AccessToken, error) {
	t.Helper()

	interceptor, err := NewUnaryServerInterceptor(newVerifier(t, authenticator), scopes...)
	must.NoError(t, err)

	var seen *oauth2server.AccessToken

	_, err = interceptor(ctx, "request", &grpc.UnaryServerInfo{FullMethod: testMethod},
		func(ctx context.Context, _ any) (any, error) {
			token, ok := oauth2server.TokenFromContext(ctx)
			must.True(t, ok)

			seen = token

			return "reply", nil
		})

	return seen, err
}

func TestNewUnaryServerInterceptor(T *testing.T) {
	T.Parallel()

	T.Run("admits a token whose audience names this resource", func(t *testing.T) {
		t.Parallel()

		token, err := callUnary(t, withBearer(t.Context(), testBearer), tokens{token: liveToken(testResource)}, "read")
		must.NoError(t, err)
		must.NotNil(t, token)
		test.EqOp(t, "subject", token.Subject.ID)
	})

	T.Run("admits a token naming this resource among others", func(t *testing.T) {
		t.Parallel()

		token, err := callUnary(t, withBearer(t.Context(), testBearer), tokens{token: liveToken(otherResource, testResource)})
		must.NoError(t, err)
		test.NotNil(t, token)
	})

	T.Run("refuses a token carrying no audience", func(t *testing.T) {
		t.Parallel()

		token, err := callUnary(t, withBearer(t.Context(), testBearer), tokens{token: liveToken()})
		test.Nil(t, token)
		test.EqOp(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrTokenAudienceMismatch)
	})

	T.Run("refuses a token minted for another resource", func(t *testing.T) {
		t.Parallel()

		token, err := callUnary(t, withBearer(t.Context(), testBearer), tokens{token: liveToken(otherResource)})
		test.Nil(t, token)
		test.EqOp(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrTokenAudienceMismatch)

		st, ok := status.FromError(err)
		must.True(t, ok)
		test.EqOp(t, "the token was not issued for this resource", st.Message())
	})

	T.Run("refuses a token lacking a required scope", func(t *testing.T) {
		t.Parallel()

		token, err := callUnary(t, withBearer(t.Context(), testBearer), tokens{token: liveToken(testResource)}, "write")
		test.Nil(t, token)
		test.EqOp(t, codes.PermissionDenied, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrInsufficientScope)
	})

	T.Run("refuses a call carrying no credential", func(t *testing.T) {
		t.Parallel()

		token, err := callUnary(t, t.Context(), tokens{token: liveToken(testResource)})
		test.Nil(t, token)
		test.EqOp(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrNoBearerToken)
	})

	T.Run("refuses a credential with another scheme", func(t *testing.T) {
		t.Parallel()

		ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs(AuthorizationMetadataKey, "Basic "+testBearer))

		_, err := callUnary(t, ctx, tokens{token: liveToken(testResource)})
		test.EqOp(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrNoBearerToken)
	})

	T.Run("refuses a token nobody issued", func(t *testing.T) {
		t.Parallel()

		_, err := callUnary(t, withBearer(t.Context(), "not-a-token"), tokens{token: liveToken(testResource)})
		test.EqOp(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrNotFound)
	})

	T.Run("refuses an expired token", func(t *testing.T) {
		t.Parallel()

		_, err := callUnary(t, withBearer(t.Context(), testBearer), tokens{err: oauth2server.ErrExpired})
		test.EqOp(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrExpired)
	})

	T.Run("answers a broken store as Internal", func(t *testing.T) {
		t.Parallel()

		_, err := callUnary(t, withBearer(t.Context(), testBearer), tokens{err: errStoreDown})
		test.EqOp(t, codes.Internal, status.Code(err))
		test.ErrorIs(t, err, errStoreDown)

		// The message names nothing: the store's error is not the caller's to
		// read.
		st, ok := status.FromError(err)
		must.True(t, ok)
		test.EqOp(t, codes.Internal.String(), st.Message())
	})

	T.Run("refuses to build without a verifier", func(t *testing.T) {
		t.Parallel()

		interceptor, err := NewUnaryServerInterceptor(nil)
		test.ErrorIs(t, err, ErrNilVerifier)
		test.Nil(t, interceptor)
	})

	T.Run("checks the scopes it was built with", func(t *testing.T) {
		t.Parallel()

		scopes := []string{"read"}

		interceptor, err := NewUnaryServerInterceptor(newVerifier(t, tokens{token: liveToken(testResource)}), scopes...)
		must.NoError(t, err)

		// A caller reusing its slice does not change what later calls need.
		scopes[0] = "write"

		_, err = interceptor(withBearer(t.Context(), testBearer), "request", &grpc.UnaryServerInfo{FullMethod: testMethod},
			func(context.Context, any) (any, error) { return "reply", nil })
		test.NoError(t, err)
	})
}

// fakeStream is a grpc.ServerStream that carries a context and nothing else.
type fakeStream struct {
	grpc.ServerStream

	ctx context.Context
}

func (s *fakeStream) Context() context.Context { return s.ctx }

// callStream opens one stream through an interceptor built over authenticator
// and reports what the handler saw, if it ran.
func callStream(t *testing.T, ctx context.Context, authenticator oauth2server.TokenAuthenticator, scopes ...string) (*oauth2server.AccessToken, error) {
	t.Helper()

	interceptor, err := NewStreamServerInterceptor(newVerifier(t, authenticator), scopes...)
	must.NoError(t, err)

	var seen *oauth2server.AccessToken

	err = interceptor(nil, &fakeStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: testMethod},
		func(_ any, ss grpc.ServerStream) error {
			token, ok := oauth2server.TokenFromContext(ss.Context())
			must.True(t, ok)

			seen = token

			return nil
		})

	return seen, err
}

func TestNewStreamServerInterceptor(T *testing.T) {
	T.Parallel()

	T.Run("admits a token whose audience names this resource", func(t *testing.T) {
		t.Parallel()

		token, err := callStream(t, withBearer(t.Context(), testBearer), tokens{token: liveToken(testResource)}, "read")
		must.NoError(t, err)
		must.NotNil(t, token)
		test.EqOp(t, "subject", token.Subject.ID)
	})

	T.Run("refuses a token carrying no audience", func(t *testing.T) {
		t.Parallel()

		token, err := callStream(t, withBearer(t.Context(), testBearer), tokens{token: liveToken()})
		test.Nil(t, token)
		test.EqOp(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrTokenAudienceMismatch)
	})

	T.Run("refuses a token minted for another resource", func(t *testing.T) {
		t.Parallel()

		token, err := callStream(t, withBearer(t.Context(), testBearer), tokens{token: liveToken(otherResource)})
		test.Nil(t, token)
		test.EqOp(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrTokenAudienceMismatch)
	})

	T.Run("refuses a token lacking a required scope", func(t *testing.T) {
		t.Parallel()

		token, err := callStream(t, withBearer(t.Context(), testBearer), tokens{token: liveToken(testResource)}, "write")
		test.Nil(t, token)
		test.EqOp(t, codes.PermissionDenied, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrInsufficientScope)
	})

	T.Run("refuses a call carrying no credential", func(t *testing.T) {
		t.Parallel()

		_, err := callStream(t, t.Context(), tokens{token: liveToken(testResource)})
		test.EqOp(t, codes.Unauthenticated, status.Code(err))
		test.ErrorIs(t, err, oauth2server.ErrNoBearerToken)
	})

	T.Run("refuses to build without a verifier", func(t *testing.T) {
		t.Parallel()

		interceptor, err := NewStreamServerInterceptor(nil)
		test.ErrorIs(t, err, ErrNilVerifier)
		test.Nil(t, interceptor)
	})
}

func TestRefusal(T *testing.T) {
	T.Parallel()

	T.Run("nil", func(t *testing.T) {
		t.Parallel()

		test.NoError(t, Refusal(nil))
	})

	T.Run("keeps the chain under the status", func(t *testing.T) {
		t.Parallel()

		err := Refusal(platformerrors.Wrap(oauth2server.ErrTokenAudienceMismatch, "verifying"))
		test.EqOp(t, codes.Unauthenticated, status.Code(err))
		test.True(t, stderrors.Is(err, oauth2server.ErrTokenAudienceMismatch))
	})
}

func TestBearerFromIncoming(T *testing.T) {
	T.Parallel()

	T.Run("reads a bearer credential", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, testBearer, BearerFromIncoming(withBearer(t.Context(), testBearer)))
	})

	T.Run("matches the scheme without regard to case", func(t *testing.T) {
		t.Parallel()

		ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs(AuthorizationMetadataKey, "bEaReR "+testBearer))
		test.EqOp(t, testBearer, BearerFromIncoming(ctx))
	})

	T.Run("reads the first of several", func(t *testing.T) {
		t.Parallel()

		ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs(
			AuthorizationMetadataKey, "Bearer first",
			AuthorizationMetadataKey, "Bearer second",
		))
		test.EqOp(t, "first", BearerFromIncoming(ctx))
	})

	T.Run("yields nothing without metadata", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, "", BearerFromIncoming(t.Context()))
	})
}
