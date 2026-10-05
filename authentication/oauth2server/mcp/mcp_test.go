package mcp_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/primandproper/primitives-go/v2/authentication/oauth2server"
	"github.com/primandproper/primitives-go/v2/authentication/oauth2server/mcp"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

const (
	testResource  = "https://api.example/mcp"
	otherResource = "https://other.example/"
	testIssuer    = "https://auth.example"
	testBearer    = "a-live-token"
	testMetadata  = "/mcp" + oauth2server.PathProtectedResourceMetadata
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
		ClientID:  "client",
		Subject:   oauth2server.Subject{ID: "subject"},
		Scopes:    []string{"read"},
		Audience:  audience,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
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

// serve sends one request through h and returns the recorded response.
func serve(h http.Handler, method, path, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	return res
}

func TestTokenVerifier(T *testing.T) {
	T.Parallel()

	T.Run("a live token for this resource", func(t *testing.T) {
		t.Parallel()

		token := liveToken(testResource)

		verify, err := mcp.TokenVerifier(newVerifier(t, tokens{token: token}))
		must.NoError(t, err)

		info, err := verify(t.Context(), testBearer, nil)
		must.NoError(t, err)

		test.Eq(t, []string{"read"}, info.Scopes)
		test.EqOp(t, "subject", info.UserID)
		test.True(t, info.Expiration.Equal(token.ExpiresAt))

		got, ok := mcp.AccessTokenFrom(info)
		test.True(t, ok)
		test.EqOp(t, token, got)
	})

	T.Run("refusals are the SDK's invalid token, sentinel kept", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			token  *oauth2server.AccessToken
			want   error
			bearer string
		}{
			"unknown":            {token: liveToken(testResource), bearer: "nope", want: oauth2server.ErrNotFound},
			"no bearer":          {token: liveToken(testResource), bearer: " ", want: oauth2server.ErrNoBearerToken},
			"another resource":   {token: liveToken(otherResource), bearer: testBearer, want: oauth2server.ErrTokenAudienceMismatch},
			"no audience at all": {token: liveToken(), bearer: testBearer, want: oauth2server.ErrTokenAudienceMismatch},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				verify, err := mcp.TokenVerifier(newVerifier(t, tokens{token: tc.token}))
				must.NoError(t, err)

				info, err := verify(t.Context(), tc.bearer, nil)
				test.Nil(t, info)
				test.ErrorIs(t, err, auth.ErrInvalidToken)
				test.ErrorIs(t, err, tc.want)
			})
		}
	})

	T.Run("a broken store is not the credential's fault", func(t *testing.T) {
		t.Parallel()

		verify, err := mcp.TokenVerifier(newVerifier(t, tokens{err: errStoreDown}))
		must.NoError(t, err)

		_, err = verify(t.Context(), testBearer, nil)
		test.ErrorIs(t, err, errStoreDown)
		test.False(t, stderrors.Is(err, auth.ErrInvalidToken))
	})

	T.Run("nil verifier", func(t *testing.T) {
		t.Parallel()

		verify, err := mcp.TokenVerifier(nil)
		test.ErrorIs(t, err, mcp.ErrNilVerifier)
		test.Nil(t, verify)
	})
}

// The SDK's own middleware, composed with TokenVerifier and BearerTokenOptions,
// keeps the 401/403 split and points its challenge at the Verifier's document.
func TestTokenVerifier_RequireBearerToken(T *testing.T) {
	T.Parallel()

	compose := func(t *testing.T, token *oauth2server.AccessToken, scopes ...string) http.Handler {
		t.Helper()

		v := newVerifier(t, tokens{token: token})

		verify, err := mcp.TokenVerifier(v)
		must.NoError(t, err)

		opts, err := mcp.BearerTokenOptions(v, scopes...)
		must.NoError(t, err)
		test.EqOp(t, v.Metadata().URL(), opts.ResourceMetadataURL)

		return auth.RequireBearerToken(verify, opts)(http.HandlerFunc(func(res http.ResponseWriter, _ *http.Request) {
			res.WriteHeader(http.StatusTeapot)
		}))
	}

	T.Run("admits", func(t *testing.T) {
		t.Parallel()

		res := serve(compose(t, liveToken(testResource), "read"), http.MethodPost, "/mcp", testBearer)
		test.EqOp(t, http.StatusTeapot, res.Code)
	})

	T.Run("another resource's token is a 401 naming the document", func(t *testing.T) {
		t.Parallel()

		res := serve(compose(t, liveToken(otherResource)), http.MethodPost, "/mcp", testBearer)
		test.EqOp(t, http.StatusUnauthorized, res.Code)
		test.StrContains(t, res.Header().Get("WWW-Authenticate"), `resource_metadata="https://api.example/mcp/.well-known/oauth-protected-resource"`)
	})

	T.Run("a missing scope is a 403", func(t *testing.T) {
		t.Parallel()

		res := serve(compose(t, liveToken(testResource), "write"), http.MethodPost, "/mcp", testBearer)
		test.EqOp(t, http.StatusForbidden, res.Code)
	})

	T.Run("nil verifier", func(t *testing.T) {
		t.Parallel()

		opts, err := mcp.BearerTokenOptions(nil)
		test.ErrorIs(t, err, mcp.ErrNilVerifier)
		test.Nil(t, opts)
	})
}

func TestProtect(T *testing.T) {
	T.Parallel()

	// protect builds Protect over token, with a handler that records the
	// TokenInfo the SDK middleware put on the context.
	protect := func(t *testing.T, authenticator oauth2server.TokenAuthenticator, scopes ...string) (http.Handler, *oauth2server.Verifier, **auth.TokenInfo) {
		t.Helper()

		v := newVerifier(t, authenticator)

		var seen *auth.TokenInfo

		h, err := mcp.Protect(v, http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			seen = auth.TokenInfoFromContext(req.Context())
			res.WriteHeader(http.StatusTeapot)
		}), scopes...)
		must.NoError(t, err)

		return h, v, &seen
	}

	T.Run("admits a live token for this resource, with the SDK's view of it", func(t *testing.T) {
		t.Parallel()

		token := liveToken(testResource)
		h, _, seen := protect(t, tokens{token: token}, "read")

		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			res := serve(h, method, "/mcp", testBearer)
			test.EqOp(t, http.StatusTeapot, res.Code)
		}

		must.NotNil(t, *seen)
		test.EqOp(t, "subject", (*seen).UserID)

		got, ok := mcp.AccessTokenFrom(*seen)
		test.True(t, ok)
		test.EqOp(t, token, got)
	})

	T.Run("no credential is a bare challenge naming the document", func(t *testing.T) {
		t.Parallel()

		h, v, _ := protect(t, tokens{token: liveToken(testResource)})

		res := serve(h, http.MethodPost, "/mcp", "")
		test.EqOp(t, http.StatusUnauthorized, res.Code)
		test.EqOp(t, `Bearer resource_metadata="`+v.Metadata().URL()+`"`, res.Header().Get("WWW-Authenticate"))
	})

	T.Run("a token for another resource is invalid_token", func(t *testing.T) {
		t.Parallel()

		for name, token := range map[string]*oauth2server.AccessToken{
			"another resource":   liveToken(otherResource),
			"no audience at all": liveToken(),
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				h, _, seen := protect(t, tokens{token: token})

				res := serve(h, http.MethodPost, "/mcp", testBearer)
				test.EqOp(t, http.StatusUnauthorized, res.Code)
				test.StrContains(t, res.Header().Get("WWW-Authenticate"), `error="invalid_token"`)
				test.Nil(t, *seen)
			})
		}
	})

	T.Run("a missing scope is a 403 that names it", func(t *testing.T) {
		t.Parallel()

		h, _, seen := protect(t, tokens{token: liveToken(testResource)}, "write")

		res := serve(h, http.MethodPost, "/mcp", testBearer)
		test.EqOp(t, http.StatusForbidden, res.Code)

		challenge := res.Header().Get("WWW-Authenticate")
		test.StrContains(t, challenge, `error="insufficient_scope"`)
		test.StrContains(t, challenge, `scope="write"`)
		test.StrContains(t, challenge, `resource_metadata="https://api.example/mcp/.well-known/oauth-protected-resource"`)
		test.Nil(t, *seen)
	})

	T.Run("a broken store is a 500", func(t *testing.T) {
		t.Parallel()

		h, _, _ := protect(t, tokens{err: errStoreDown})

		res := serve(h, http.MethodPost, "/mcp", testBearer)
		test.EqOp(t, http.StatusInternalServerError, res.Code)
	})

	T.Run("serves the document the challenge points at, unauthenticated", func(t *testing.T) {
		t.Parallel()

		h, _, _ := protect(t, tokens{token: liveToken(testResource)})

		res := serve(h, http.MethodGet, testMetadata, "")
		test.EqOp(t, http.StatusOK, res.Code)

		var doc oauth2server.ProtectedResourceMetadata
		must.NoError(t, json.Unmarshal(res.Body.Bytes(), &doc))
		test.EqOp(t, testResource, doc.Resource)
		test.Eq(t, []string{testIssuer}, doc.AuthorizationServers)
	})

	T.Run("the document is read-only", func(t *testing.T) {
		t.Parallel()

		h, _, _ := protect(t, tokens{token: liveToken(testResource)})

		res := serve(h, http.MethodPost, testMetadata, testBearer)
		test.EqOp(t, http.StatusMethodNotAllowed, res.Code)
		test.EqOp(t, "GET, HEAD", res.Header().Get("Allow"))
	})

	T.Run("nil arguments", func(t *testing.T) {
		t.Parallel()

		h, err := mcp.Protect(nil, http.NotFoundHandler())
		test.ErrorIs(t, err, mcp.ErrNilVerifier)
		test.Nil(t, h)

		h, err = mcp.Protect(newVerifier(t, tokens{}), nil)
		test.ErrorIs(t, err, mcp.ErrNilHandler)
		test.Nil(t, h)
	})
}

func TestAccessTokenFrom(T *testing.T) {
	T.Parallel()

	T.Run("a TokenInfo this package did not build", func(t *testing.T) {
		t.Parallel()

		for name, info := range map[string]*auth.TokenInfo{
			"nil":        nil,
			"no extra":   {UserID: "subject"},
			"wrong type": {Extra: map[string]any{mcp.ExtraAccessToken: "a string"}},
			"typed nil":  {Extra: map[string]any{mcp.ExtraAccessToken: (*oauth2server.AccessToken)(nil)}},
		} {
			token, ok := mcp.AccessTokenFrom(info)
			test.False(t, ok, test.Sprint(name))
			test.Nil(t, token, test.Sprint(name))
		}
	})
}

// bearerTransport adds a bearer credential to every request, which is what an
// MCP client that has completed discovery does.
type bearerTransport struct {
	bearer string
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.bearer)

	return http.DefaultTransport.RoundTrip(req)
}

type whoamiOutput struct {
	Subject  string `json:"subject"`
	ClientID string `json:"clientID"`
}

// End to end through the SDK: a real client, the streamable transport, and a
// tool reading the verified token off its request.
func TestProtect_StreamableHTTP(T *testing.T) {
	T.Parallel()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "v0"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "whoami"},
		func(_ context.Context, req *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, whoamiOutput, error) {
			token, ok := mcp.AccessTokenFrom(req.Extra.TokenInfo)
			if !ok {
				return nil, whoamiOutput{}, platformerrors.New("no token")
			}

			return nil, whoamiOutput{Subject: token.Subject.ID, ClientID: token.ClientID}, nil
		})

	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)

	protected, err := mcp.Protect(newVerifier(T, tokens{token: liveToken(testResource)}), handler)
	must.NoError(T, err)

	srv := httptest.NewServer(protected)
	T.Cleanup(srv.Close)

	connect := func(t *testing.T, bearer string) (*sdkmcp.ClientSession, error) {
		t.Helper()

		client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "client", Version: "v0"}, nil)

		return client.Connect(t.Context(), &sdkmcp.StreamableClientTransport{
			Endpoint:             srv.URL + "/mcp",
			HTTPClient:           &http.Client{Transport: bearerTransport{bearer: bearer}},
			MaxRetries:           -1,
			DisableStandaloneSSE: true,
		}, nil)
	}

	T.Run("a tool sees the verified token", func(t *testing.T) {
		t.Parallel()

		session, connectErr := connect(t, testBearer)
		must.NoError(t, connectErr)
		t.Cleanup(func() { _ = session.Close() })

		result, callErr := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "whoami"})
		must.NoError(t, callErr)
		must.False(t, result.IsError)

		raw, marshalErr := json.Marshal(result.StructuredContent)
		must.NoError(t, marshalErr)

		var out whoamiOutput
		must.NoError(t, json.Unmarshal(raw, &out))
		test.EqOp(t, "subject", out.Subject)
		test.EqOp(t, "client", out.ClientID)
	})

	T.Run("an unknown token never reaches the server", func(t *testing.T) {
		t.Parallel()

		session, connectErr := connect(t, "nope")
		test.Error(t, connectErr)
		test.Nil(t, session)
	})
}
