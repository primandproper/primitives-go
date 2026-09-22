package inbound

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"
	"time"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// The worked example from Twilio's own security documentation. It pins this
// implementation to a published vector rather than to whatever it happened to
// produce first, which is the only way to know the canonicalization is right
// rather than merely self-consistent: a wrong one is a receiver that accepts
// forged replies or rejects every real one, and both look like a happy path in
// a test that signs with the code under test.
const (
	twilioDocToken     = "12345"
	twilioDocURL       = "https://example.com/myapp.php?foo=1&bar=2"
	twilioDocSignature = "L/OH5YylLD5NRKLltdqwSvS0BnU="
)

// twilioDocBody is the documented example's POST parameters, form-encoded. The
// order here is deliberately not the sorted order the MAC is computed in, so
// that a verifier which signed the body's wire order would fail the vector.
const twilioDocBody = "To=%2B18005551212&From=%2B14158675310&Digits=1234&CallSid=CA1234567890ABCDE&Caller=%2B14158675310"

// signTwilio computes a signature the way Twilio does, from the primitives
// rather than through twilioSignedString, so the tests that need a signature
// for a case the docs do not cover are not checking the implementation against
// itself.
func signTwilio(t *testing.T, token, publicURL string, params url.Values) string {
	t.Helper()

	signed := publicURL

	for _, key := range sortedKeys(params) {
		for _, value := range params[key] {
			signed += key + value
		}
	}

	mac := hmac.New(sha1.New, []byte(token))
	mac.Write([]byte(signed))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// sortedKeys is an insertion sort over the keys, written out so that the
// expectation is spelled independently of the sort the implementation uses.
func sortedKeys(params url.Values) []string {
	keys := make([]string, 0, len(params))

	for key := range params {
		i := 0
		for i < len(keys) && keys[i] < key {
			i++
		}

		keys = append(keys, "")
		copy(keys[i+1:], keys[i:])
		keys[i] = key
	}

	return keys
}

func TestNewTwilioVerifier(T *testing.T) {
	T.Parallel()

	T.Run("matches Twilio's published example", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier(twilioDocToken, twilioDocURL)
		must.NoError(t, err)

		test.EqOp(t, providerTwilio, verifier.Provider())
		test.NoError(t, verifier.Verify(
			t.Context(),
			signedHeaders(TwilioSignatureHeader, twilioDocSignature),
			[]byte(twilioDocBody),
		))
	})

	// The whole point of the scheme: the signed material is not the bytes. A
	// verifier that HMAC'd the body would pass every test that signs with
	// itself and fail every real delivery.
	T.Run("does not verify the raw body", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier(twilioDocToken, twilioDocURL)
		must.NoError(t, err)

		mac := hmac.New(sha1.New, []byte(twilioDocToken))
		mac.Write([]byte(twilioDocBody))

		overBody := base64.StdEncoding.EncodeToString(mac.Sum(nil))

		test.ErrorIs(t,
			verifier.Verify(t.Context(), signedHeaders(TwilioSignatureHeader, overBody), []byte(twilioDocBody)),
			ErrInvalidSignature,
		)
	})

	// A form's wire order is not the signing order, so two encodings of the
	// same parameters have to produce the same verdict.
	T.Run("is indifferent to the body's parameter order", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier(twilioDocToken, twilioDocURL)
		must.NoError(t, err)

		reordered := "CallSid=CA1234567890ABCDE&Caller=%2B14158675310&Digits=1234&From=%2B14158675310&To=%2B18005551212"

		test.NoError(t, verifier.Verify(
			t.Context(),
			signedHeaders(TwilioSignatureHeader, twilioDocSignature),
			[]byte(reordered),
		))
	})

	// The query string is part of what Twilio signs, so a verifier configured
	// with the bare path cannot verify a delivery to a URL that carries one.
	T.Run("signs the URL's query string", func(t *testing.T) {
		t.Parallel()

		params := url.Values{"Body": {"hello"}}
		body := []byte(params.Encode())

		withQuery := "https://example.com/hooks/twilio?tenant=acme"
		signature := signTwilio(t, twilioDocToken, withQuery, params)

		matching, err := NewTwilioVerifier(twilioDocToken, withQuery)
		must.NoError(t, err)
		test.NoError(t, matching.Verify(t.Context(), signedHeaders(TwilioSignatureHeader, signature), body))

		bare, err := NewTwilioVerifier(twilioDocToken, "https://example.com/hooks/twilio")
		must.NoError(t, err)
		test.ErrorIs(t,
			bare.Verify(t.Context(), signedHeaders(TwilioSignatureHeader, signature), body),
			ErrInvalidSignature,
		)
	})

	// Twilio's media parameters repeat a key. Each value contributes its own
	// key+value pair, and the values sort among themselves so that the wire
	// order of a repeated key cannot change the MAC either.
	T.Run("appends every value of a repeated key, sorted", func(t *testing.T) {
		t.Parallel()

		params := url.Values{
			"MediaUrl":   {"https://api.twilio.com/a", "https://api.twilio.com/b"},
			"MessageSid": {"SM0123456789abcdef"},
		}

		signature := signTwilio(t, twilioDocToken, twilioDocURL, params)

		verifier, err := NewTwilioVerifier(twilioDocToken, twilioDocURL)
		must.NoError(t, err)

		// Encoded with the repeated key's values the other way round: sorting
		// them is what makes the two encodings agree.
		body := []byte("MediaUrl=https%3A%2F%2Fapi.twilio.com%2Fb&MediaUrl=https%3A%2F%2Fapi.twilio.com%2Fa&MessageSid=SM0123456789abcdef")

		test.NoError(t, verifier.Verify(t.Context(), signedHeaders(TwilioSignatureHeader, signature), body))
	})

	// A GET-configured callback has no parameters to append, so the URL alone
	// is the signed string.
	T.Run("signs the URL alone for an empty body", func(t *testing.T) {
		t.Parallel()

		signature := signTwilio(t, twilioDocToken, twilioDocURL, nil)

		verifier, err := NewTwilioVerifier(twilioDocToken, twilioDocURL)
		must.NoError(t, err)

		test.NoError(t, verifier.Verify(t.Context(), signedHeaders(TwilioSignatureHeader, signature), nil))
	})

	// Twilio's JSON scheme signs a bodySHA256 query parameter instead, and is
	// not implemented. A JSON delivery must fail closed rather than land in
	// some other error a receiver would answer differently.
	T.Run("refuses a JSON body", func(t *testing.T) {
		t.Parallel()

		body := []byte(`{"MessageSid":"SM0123456789abcdef","Body":"hello"}`)

		signature := signTwilio(t, twilioDocToken, twilioDocURL, url.Values{})

		verifier, err := NewTwilioVerifier(twilioDocToken, twilioDocURL)
		must.NoError(t, err)

		test.ErrorIs(t,
			verifier.Verify(t.Context(), signedHeaders(TwilioSignatureHeader, signature), body),
			ErrInvalidSignature,
		)
	})

	// A body url.ParseQuery genuinely rejects, rather than one it merely reads
	// oddly: the parse failure has to become ErrInvalidSignature, not escape as
	// a parse error.
	T.Run("refuses an unparseable body", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier(twilioDocToken, twilioDocURL)
		must.NoError(t, err)

		test.ErrorIs(t,
			verifier.Verify(t.Context(), signedHeaders(TwilioSignatureHeader, twilioDocSignature), []byte("Body=%zz")),
			ErrInvalidSignature,
		)
	})

	T.Run("rejects a wrong auth token", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier("not-the-token", twilioDocURL)
		must.NoError(t, err)

		test.ErrorIs(t,
			verifier.Verify(t.Context(), signedHeaders(TwilioSignatureHeader, twilioDocSignature), []byte(twilioDocBody)),
			ErrInvalidSignature,
		)
	})

	T.Run("rejects a missing header", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier(twilioDocToken, twilioDocURL)
		must.NoError(t, err)

		test.ErrorIs(t, verifier.Verify(t.Context(), http.Header{}, []byte(twilioDocBody)), ErrInvalidSignature)
		test.ErrorIs(t, verifier.Verify(t.Context(), nil, []byte(twilioDocBody)), ErrInvalidSignature)
	})

	T.Run("rejects a header that is not base64", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier(twilioDocToken, twilioDocURL)
		must.NoError(t, err)

		test.ErrorIs(t,
			verifier.Verify(t.Context(), signedHeaders(TwilioSignatureHeader, "not base64!"), []byte(twilioDocBody)),
			ErrInvalidSignature,
		)
	})

	// Twilio issues a secondary auth token for exactly this, and a delivery may
	// be signed under either one while the promotion happens.
	T.Run("verifies under a secondary auth token", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier("the-outgoing-token", twilioDocURL, WithAdditionalSecrets(twilioDocToken))
		must.NoError(t, err)

		test.NoError(t, verifier.Verify(
			t.Context(),
			signedHeaders(TwilioSignatureHeader, twilioDocSignature),
			[]byte(twilioDocBody),
		))
	})

	T.Run("builds on the secondary token alone", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier("", twilioDocURL, WithAdditionalSecrets(twilioDocToken))
		must.NoError(t, err)

		test.NoError(t, verifier.Verify(
			t.Context(),
			signedHeaders(TwilioSignatureHeader, twilioDocSignature),
			[]byte(twilioDocBody),
		))
	})

	T.Run("refuses to build without a token", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier("", twilioDocURL)

		test.ErrorIs(t, err, ErrNoSecret)
		test.Nil(t, verifier)
	})

	T.Run("refuses to build without a URL", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier(twilioDocToken, "")

		test.ErrorIs(t, err, platformerrors.ErrEmptyInputParameter)
		test.Nil(t, verifier)
	})

	// The misconfiguration worth catching at construction: a path where the
	// full public URL belongs fails every delivery, with the same error a wrong
	// token gives.
	T.Run("refuses a URL that is not absolute", func(t *testing.T) {
		t.Parallel()

		for _, notAbsolute := range []string{"/webhooks/twilio", "example.com/webhooks", "https://", "://nope"} {
			verifier, err := NewTwilioVerifier(twilioDocToken, notAbsolute)

			test.ErrorIs(t, err, platformerrors.ErrUnrecognizedInputValue)
			test.Nil(t, verifier)
		}
	})

	// The timestamp options belong to the timestamped schemes; naming one here
	// must not change a verdict, rather than half-applying a freshness check
	// this scheme cannot make.
	T.Run("ignores the timestamp options", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier(twilioDocToken, twilioDocURL, WithVerificationTime(time.Unix(1, 0)))
		must.NoError(t, err)

		test.NoError(t, verifier.Verify(
			t.Context(),
			signedHeaders(TwilioSignatureHeader, twilioDocSignature),
			[]byte(twilioDocBody),
		))
	})
}

func TestTwilioSignedString(T *testing.T) {
	T.Parallel()

	// The concatenated string from Twilio's documentation, spelled out: no
	// separator between the URL and the first key, between a key and its value,
	// or between one pair and the next.
	T.Run("matches the documented concatenation", func(t *testing.T) {
		t.Parallel()

		signed, err := twilioSignedString(twilioDocURL, []byte(twilioDocBody))
		must.NoError(t, err)

		test.EqOp(t,
			twilioDocURL+
				"CallSidCA1234567890ABCDE"+
				"Caller+14158675310"+
				"Digits1234"+
				"From+14158675310"+
				"To+18005551212",
			signed,
		)
	})

	// The values that enter the MAC are the decoded ones: "+14158675310"
	// arrives on the wire as "%2B14158675310", and a space arrives as "+".
	T.Run("appends decoded values", func(t *testing.T) {
		t.Parallel()

		signed, err := twilioSignedString("https://example.com/", []byte("Body=hello+there%21"))
		must.NoError(t, err)

		test.EqOp(t, "https://example.com/Bodyhello there!", signed)
	})

	T.Run("appends nothing for an empty body", func(t *testing.T) {
		t.Parallel()

		signed, err := twilioSignedString(twilioDocURL, nil)
		must.NoError(t, err)

		test.EqOp(t, twilioDocURL, signed)
	})

	T.Run("reports a body it cannot parse", func(t *testing.T) {
		t.Parallel()

		_, err := twilioSignedString(twilioDocURL, []byte("Body=%zz"))

		test.Error(t, err)
	})
}

// TestTwilioVerifier_defaultPort pins the forgiveness twilioSignedURLs buys.
// Twilio's backend is inconsistent about spelling out :443 or :80, so a
// verifier that compared only against the configured URL would reject every
// real delivery whenever the backend disagreed with the console — with the same
// error a wrong auth token gives, which is the worst way to learn it.
func TestTwilioVerifier_defaultPort(T *testing.T) {
	T.Parallel()

	params := url.Values{"Body": {"hello"}, "MessageSid": {"SM0123456789abcdef"}}
	body := []byte(params.Encode())

	// Each pair is what the console was given and what Twilio signed. Both
	// directions, because the backend may add the port or drop it.
	for name, urls := range map[string]struct{ configured, signed string }{
		"https configured without the port, signed with it": {"https://example.com/hook", "https://example.com:443/hook"},
		"https configured with the port, signed without it": {"https://example.com:443/hook", "https://example.com/hook"},
		"http configured without the port, signed with it":  {"http://example.com/hook", "http://example.com:80/hook"},
		"http configured with the port, signed without it":  {"http://example.com:80/hook", "http://example.com/hook"},
		"the query string survives the port being added":    {"https://example.com/hook?tenant=1", "https://example.com:443/hook?tenant=1"},
		"the query string survives the port being removed":  {"https://example.com:443/hook?tenant=1", "https://example.com/hook?tenant=1"},
	} {
		T.Run(name, func(t *testing.T) {
			t.Parallel()

			verifier, err := NewTwilioVerifier(twilioDocToken, urls.configured)
			must.NoError(t, err)

			headers := http.Header{TwilioSignatureHeader: {signTwilio(t, twilioDocToken, urls.signed, params)}}
			test.NoError(t, verifier.Verify(t.Context(), headers, body))
		})
	}

	// A non-default port is one Twilio had to dial to reach the service, so it
	// cannot be the thing that differs. Accepting a signature over the portless
	// URL would be accepting one over a URL nothing ever requested.
	T.Run("a non-default port is not dropped", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier(twilioDocToken, "https://example.com:8443/hook")
		must.NoError(t, err)

		headers := http.Header{TwilioSignatureHeader: {signTwilio(t, twilioDocToken, "https://example.com/hook", params)}}
		test.ErrorIs(t, verifier.Verify(t.Context(), headers, body), ErrInvalidSignature)
	})

	// The forgiveness is exactly one byte-for-byte alternative, not a general
	// tolerance for URLs that look similar.
	T.Run("a different host still fails", func(t *testing.T) {
		t.Parallel()

		verifier, err := NewTwilioVerifier(twilioDocToken, "https://example.com/hook")
		must.NoError(t, err)

		headers := http.Header{TwilioSignatureHeader: {signTwilio(t, twilioDocToken, "https://evil.example.com/hook", params)}}
		test.ErrorIs(t, verifier.Verify(t.Context(), headers, body), ErrInvalidSignature)
	})
}

func TestTwilioSignedURLs(T *testing.T) {
	T.Parallel()

	for name, tc := range map[string]struct {
		in   string
		want []string
	}{
		"https without a port":     {"https://example.com/h", []string{"https://example.com/h", "https://example.com:443/h"}},
		"https with the default":   {"https://example.com:443/h", []string{"https://example.com:443/h", "https://example.com/h"}},
		"http without a port":      {"http://example.com/h", []string{"http://example.com/h", "http://example.com:80/h"}},
		"http with the default":    {"http://example.com:80/h", []string{"http://example.com:80/h", "http://example.com/h"}},
		"a non-default port":       {"https://example.com:8443/h", []string{"https://example.com:8443/h"}},
		"http's default under tls": {"https://example.com:80/h", []string{"https://example.com:80/h"}},
		"no path at all":           {"https://example.com", []string{"https://example.com", "https://example.com:443"}},
		"a query and no path":      {"https://example.com?a=1", []string{"https://example.com?a=1", "https://example.com:443?a=1"}},
		// Neither appears in a Twilio webhook URL; the authority is still read
		// correctly, so the colon in userinfo and the ones inside the IPv6
		// literal are not mistaken for a port.
		"userinfo carrying a colon":        {"https://user:pw@example.com/h", []string{"https://user:pw@example.com/h", "https://user:pw@example.com:443/h"}},
		"an IPv6 literal":                  {"https://[::1]/h", []string{"https://[::1]/h", "https://[::1]:443/h"}},
		"an IPv6 literal with the default": {"https://[::1]:443/h", []string{"https://[::1]:443/h", "https://[::1]/h"}},
	} {
		T.Run(name, func(t *testing.T) {
			t.Parallel()

			test.Eq(t, tc.want, twilioSignedURLs(tc.in))
		})
	}
}
