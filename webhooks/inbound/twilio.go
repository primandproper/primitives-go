package inbound

import (
	"context"
	"encoding/base64"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/primandproper/primitives-go/v2/cryptography/hashing"
	"github.com/primandproper/primitives-go/v2/cryptography/hashing/hmac"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
)

const (
	// TwilioSignatureHeader carries Twilio's base64 HMAC-SHA-1 over the request
	// URL and the form parameters — not over the body. See TwilioVerifier.
	TwilioSignatureHeader = "X-Twilio-Signature"

	// TwilioMessageSidParam names the form field carrying Twilio's per-message
	// identifier. It is the value a consumer keys deduplication on, the way
	// GitHubDeliveryHeader is for GitHub, and unlike that one it is inside the
	// signed material rather than in an unauthenticated header.
	//
	// It is a constant rather than a field on Delivery because this package does
	// not parse payloads: the consumer decodes the form it is handed and reads
	// this key out of it. A voice callback carries CallSid instead, which is why
	// there is no single "event ID" to lift.
	TwilioMessageSidParam = "MessageSid"

	// providerTwilio is the Provider label NewTwilioVerifier reports.
	providerTwilio = "twilio"
)

// TwilioVerifier verifies Twilio's X-Twilio-Signature.
//
// Twilio is the shape the doc warns about: the MAC is not over the body. It is
// over the public URL Twilio requested, with the POST form parameters appended
// — sorted by key, each key immediately followed by its value, with no
// separators anywhere — under HMAC-SHA-1 with the account's auth token,
// base64-encoded. The raw bytes never enter the MAC; their decoded
// parameters do, and the URL does.
//
// That is why the URL is a constructor argument. Verify is handed headers and a
// body, which is everything the other schemes here need and one thing short of
// what this one needs, and the missing piece is fixed configuration rather than
// per-request data. Twilio's own SDKs are shaped the same way.
//
// Only the form-encoded scheme is implemented. Twilio signs a JSON body
// differently — it appends a bodySHA256 query parameter to the URL and signs
// that, leaving the body itself outside the MAC in the same way — which is a
// second scheme, and is out of scope until a consumer needs it. A body that is
// not form-encoded therefore fails verification like any other body that did
// not prove where it came from.
type TwilioVerifier struct {
	// signedURLs are the forms of the configured URL Twilio may have signed,
	// computed once by twilioSignedURLs. There is more than one because the
	// backend is inconsistent about the default port; see that function.
	signedURLs []string
	hashers    []hashing.Hasher
}

var _ Verifier = (*TwilioVerifier)(nil)

// NewTwilioVerifier builds a Verifier for Twilio's X-Twilio-Signature.
//
// authToken is the account's auth token, from the Twilio console — the same
// value that authenticates outbound API calls, which Twilio reuses as the
// webhook signing key.
//
// publicURL is the webhook URL exactly as it is configured on the Twilio
// number, application, or service: scheme, host, any non-default port, path,
// and query string. It is signed verbatim, so it has to be the URL Twilio
// dialed rather than the one that reached the process. A proxy that terminates
// TLS, rewrites a path, or moves the service to a container port leaves the
// request looking nothing like what was signed, and every delivery then fails
// with ErrInvalidSignature — which is the same error a wrong token gives, so
// suspect this first.
//
// The one difference it is forgiving about is the scheme's default port, which
// Twilio's backend is inconsistent about including; writing the URL either way
// verifies. See twilioSignedURLs.
//
// Reads WithAdditionalSecrets, which is how Twilio's secondary auth token is
// supplied: the console issues a second token for exactly this rotation, and
// deliveries may be signed under either one while the promotion happens. The
// timestamp options do nothing, as this scheme signs no timestamp.
func NewTwilioVerifier(authToken, publicURL string, opts ...VerifierOption) (*TwilioVerifier, error) {
	if publicURL == "" {
		return nil, platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "Twilio public webhook URL")
	}

	// Parsed to catch the misconfiguration that produces a uniformly failing
	// endpoint — a path where an absolute URL belongs — but never re-rendered:
	// what gets signed is the caller's string, because a URL that survives a
	// round trip through net/url unchanged is not something to rely on.
	parsed, err := url.Parse(publicURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return nil, platformerrors.Wrapf(platformerrors.ErrUnrecognizedInputValue, "Twilio public webhook URL %q is not absolute", publicURL)
	}

	cfg := newVerifierConfig(opts)

	secrets := cfg.secretsWith(authToken)
	if len(secrets) == 0 {
		return nil, ErrNoSecret
	}

	hashers := make([]hashing.Hasher, 0, len(secrets))
	for _, key := range secrets {
		hashers = append(hashers, hmac.NewHMACSHA1Hasher([]byte(key)))
	}

	return &TwilioVerifier{signedURLs: twilioSignedURLs(publicURL), hashers: hashers}, nil
}

// Provider returns "twilio".
func (v *TwilioVerifier) Provider() string { return providerTwilio }

// Verify checks X-Twilio-Signature against the verifier's URL and body's form
// parameters.
func (v *TwilioVerifier) Verify(_ context.Context, headers http.Header, body []byte) error {
	// A nil header bag reads as an absent header rather than a special case, which is what
	// http.Header.Get already does.
	presented := headers.Get(TwilioSignatureHeader)
	if presented == "" {
		// The same error a wrong signature gets; see ErrInvalidSignature.
		return platformerrors.Wrapf(ErrInvalidSignature, "no %s header", TwilioSignatureHeader)
	}

	candidate, err := base64.StdEncoding.DecodeString(presented)
	if err != nil {
		return platformerrors.Wrapf(ErrInvalidSignature, "%s is not valid base64", TwilioSignatureHeader)
	}

	// Every URL Twilio may have signed, against every secret, without
	// short-circuiting on the first match; see hmac.MatchesAny and
	// twilioSignedURLs.
	var matched bool

	for _, signedURL := range v.signedURLs {
		signed, signErr := twilioSignedString(signedURL, body)
		if signErr != nil {
			// A body this scheme cannot canonicalize did not prove it came from
			// Twilio, which is the same statement a mismatched MAC makes. Returning
			// a parse error instead would answer a 400 to a forgery and hand a
			// prober a way to tell "malformed" from "unsigned".
			//
			// The body is the same for every candidate URL, so one failing to
			// parse means they all do.
			return platformerrors.Wrap(ErrInvalidSignature, "body is not form-encoded")
		}

		if hmac.MatchesAny(v.hashers, []byte(signed), candidate) {
			matched = true
		}
	}

	if !matched {
		return ErrInvalidSignature
	}

	return nil
}

// twilioSignedURLs returns every form of publicURL that Twilio may have signed:
// the one that was configured, and — where the two differ only by the scheme's
// default port — the other one.
//
// Twilio's backend is inconsistent about whether the URL it signs spells out an
// explicit :443 or :80. Its own helper libraries answer this by computing the
// signature both ways and accepting either, with the comment "sig generation on
// back-end is inconsistent"; a receiver that compared only against what the
// console was given rejects every delivery whenever the backend disagrees with
// it, and does so with the same ErrInvalidSignature a wrong auth token gives.
//
// Only the default port varies. A non-default port is one Twilio must have
// dialed to reach the service at all, so it cannot be the thing that differs,
// and offering a candidate without it would accept a signature over a URL that
// was never requested. That is narrower than Twilio's own libraries, which strip
// any port; the extra candidate they allow is one their backend cannot produce.
//
// The authority is edited in place rather than re-rendered through net/url, for
// the reason NewTwilioVerifier parses without rebuilding: every other byte of
// the URL has to reach the MAC exactly as it was written, and percent-encoding
// in a path or query does not reliably survive the round trip.
func twilioSignedURLs(publicURL string) []string {
	asConfigured := []string{publicURL}

	schemeEnd := strings.Index(publicURL, "://")
	if schemeEnd < 0 {
		return asConfigured
	}

	var defaultPort string

	switch strings.ToLower(publicURL[:schemeEnd]) {
	case "https":
		defaultPort = "443"
	case "http":
		defaultPort = "80"
	default:
		// Some other scheme has no default port to disagree about.
		return asConfigured
	}

	authorityStart := schemeEnd + len("://")

	authorityEnd := strings.IndexAny(publicURL[authorityStart:], "/?#")
	if authorityEnd < 0 {
		authorityEnd = len(publicURL)
	} else {
		authorityEnd += authorityStart
	}

	head, authority, tail := publicURL[:authorityStart], publicURL[authorityStart:authorityEnd], publicURL[authorityEnd:]

	// The port follows the last colon that is outside any userinfo and outside
	// any bracketed IPv6 literal. Neither appears in a Twilio webhook URL, but
	// reading the authority correctly costs two lines.
	host := authority
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		host = authority[at+1:]
	}

	portStart := strings.LastIndex(host, ":")
	if portStart < strings.LastIndex(host, "]") {
		portStart = -1
	}

	if portStart < 0 {
		// No port was configured, so the other candidate spells out the default.
		return append(asConfigured, head+authority+":"+defaultPort+tail)
	}

	if host[portStart+1:] != defaultPort {
		return asConfigured
	}

	// The default port was spelled out, so the other candidate drops it.
	return append(asConfigured, head+authority[:len(authority)-len(host)+portStart]+tail)
}

// twilioSignedString builds the string Twilio signs: the public URL, then every
// form parameter sorted by key, each key immediately followed by its value.
//
// Values are the decoded ones — url.ParseQuery undoes the percent-encoding and
// the plus-for-space substitution — because that is what Twilio hashed before
// it encoded the form. A repeated key contributes one key+value pair per value,
// with the values sorted among themselves, so that a form's wire order cannot
// change the MAC.
//
// An empty body yields the URL alone, which is the correct signed string for a
// Twilio callback configured as a GET: there are no parameters to append, and
// the query string is already part of the URL.
func twilioSignedString(publicURL string, body []byte) (string, error) {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", err
	}

	var signed strings.Builder

	signed.WriteString(publicURL)

	for _, key := range slices.Sorted(maps.Keys(values)) {
		for _, value := range slices.Sorted(slices.Values(values[key])) {
			signed.WriteString(key)
			signed.WriteString(value)
		}
	}

	return signed.String(), nil
}
