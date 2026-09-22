package twilio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/metrics"
	"github.com/primandproper/primitives-go/v2/sms"
)

const (
	// name scopes this package's spans, logs, and metrics. It is qualified with
	// the component so that a vendor instrumented by more than one platform
	// package does not end up sharing an instrumentation scope.
	name = "twilio_sms_sender"

	// defaultBaseURL is Twilio's API host. Config.BaseURL overrides it, which is
	// what points this package at an httptest server.
	defaultBaseURL = "https://api.twilio.com"

	// apiVersion is the date-stamped prefix every Twilio REST path carries. It
	// has not moved since 2010 and is not a knob: a deployment that needed a
	// different one would need a different adapter.
	apiVersion = "2010-04-01"

	// maxErrorBodyBytes bounds how much of a non-2xx response body is read
	// before giving up on decoding it. Twilio's error objects are a few hundred
	// bytes; this stops a misrouted request that landed on something else from
	// pulling an arbitrarily large body into memory on the error path.
	maxErrorBodyBytes = 64 << 10 // 64 KiB

	// maxErrorBodySnippetBytes bounds how much of a body that did not decode as
	// Twilio's error object reaches the error message.
	//
	// It is a second, much smaller bound because the two serve different
	// readers: maxErrorBodyBytes is what the JSON decoder is allowed to see, and
	// this is what a person reading a log line is. A proxy's HTML error page
	// quoted in full is not something anyone reads, and io.LimitReader truncates
	// silently where http.MaxBytesReader would have failed the read, so the
	// bound on the read is no longer a bound on the message.
	maxErrorBodySnippetBytes = 512

	// The span and log attributes this package records. They are local constants
	// rather than observability/keys entries because nothing else in this module
	// records them, and a key with one writer is not a convention anybody can
	// drift from.
	toAttrKey        = "sms.to"
	fromAttrKey      = "sms.from"
	messageIDAttrKey = "sms.message_id"
	errorCodeAttrKey = "sms.provider_error_code"
)

// The Twilio error codes this adapter translates into sms sentinels. Every
// other code comes back as a wrapped provider error carrying the status and
// Twilio's own message, which is what a reader of the log needs and what a
// consumer has nothing specific to do about.
const (
	// codeInvalidRecipient is "Invalid 'To' Phone Number".
	codeInvalidRecipient = 21211
	// codeUnverifiedRecipient is Twilio's "the number is unverified; trial
	// accounts may only send messages to verified numbers".
	codeUnverifiedRecipient = 21608
	// codeRecipientOptedOut is "Attempt to send to unsubscribed recipient" — the
	// number replied STOP and the carrier refuses further sends.
	codeRecipientOptedOut = 21610
)

var (
	_ sms.Sender = (*Sender)(nil)

	// ErrNilConfig indicates a nil config was provided.
	ErrNilConfig = platformerrors.New("twilio config is nil")
	// ErrEmptyAccountSID indicates an empty account SID was provided.
	ErrEmptyAccountSID = platformerrors.New("empty Twilio account SID")
	// ErrEmptyAuthToken indicates an empty auth token was provided.
	ErrEmptyAuthToken = platformerrors.New("empty Twilio auth token")
	// ErrNilHTTPClient indicates a nil HTTP client was provided.
	ErrNilHTTPClient = platformerrors.New("nil twilio HTTP client")
)

type (
	// Sender uses Twilio to send text messages. It is exported, and returned by
	// NewSender, so a caller who has chosen Twilio can depend on that choice
	// rather than on the interface every provider shares.
	Sender struct {
		o11y        observability.Observer
		instruments *metrics.OperationSet
		client      *http.Client
		messagesURL string
		accountSID  string
		authToken   string
	}

	// messageResponse is the part of Twilio's Message resource this adapter
	// reads. Everything else it returns — the price, the segment count, the
	// status at accept time — is deliberately unread: see sms.Receipt on why the
	// message ID is the whole of what a send can honestly report.
	messageResponse struct {
		SID string `json:"sid"`
	}

	// errorResponse is Twilio's error object, which every non-2xx answer carries.
	//
	// Code is the stable, documented identifier — 21610 means the same thing
	// forever — and is what the sentinel translation reads. Message is Twilio's
	// prose, kept for the wrapped error so an operator reading a log gets the
	// vendor's own words rather than this package's guess at them.
	errorResponse struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
		Status  int    `json:"status"`
	}
)

// NewSender returns a new Twilio-backed Sender.
//
// The *http.Client is a dependency rather than an option because reaching
// Twilio is the whole of what this does, and the timeout and transport a
// deployment wants there are not this package's to choose. Nothing here retries:
// a send is one request, and a caller that wants backoff wraps this with the
// retry package.
func NewSender(cfg *Config, client *http.Client, opts ...Option) (*Sender, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}

	accountSID := strings.TrimSpace(cfg.AccountSID)
	if accountSID == "" {
		return nil, ErrEmptyAccountSID
	}

	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, ErrEmptyAuthToken
	}

	if client == nil {
		return nil, ErrNilHTTPClient
	}

	o := newOptions(opts)

	instruments, err := metrics.NewOperationSet(o.metricsProvider, name)
	if err != nil {
		return nil, platformerrors.Wrap(err, "creating twilio sms instruments")
	}

	baseURL := defaultBaseURL
	if strings.TrimSpace(cfg.BaseURL) != "" {
		baseURL = strings.TrimSuffix(strings.TrimSpace(cfg.BaseURL), "/")
	}

	// Built once rather than per send. The account SID is path-escaped because
	// it is interpolated into a URL, not because Twilio issues one that needs it:
	// what reaches this is whatever a deployment put in its environment.
	messagesURL := strings.Join([]string{baseURL, apiVersion, "Accounts", url.PathEscape(accountSID), "Messages.json"}, "/")

	return &Sender{
		o11y:        observability.NewObserver(name, o.logger, o.tracerProvider),
		instruments: instruments,
		client:      client,
		messagesURL: messagesURL,
		accountSID:  accountSID,
		authToken:   cfg.AuthToken,
	}, nil
}

// SendSMS sends one text message and returns what Twilio called it.
//
// The body is sent exactly as given. Length, segment count, and the
// GSM-versus-UCS-2 encoding choice are Twilio's — a body it rejects for length
// comes back as a wrapped provider error — and non-ASCII text is passed through
// untouched, which is what makes an accented Spanish reminder arrive as one
// wrote it.
//
// The three refusals Twilio makes about the message rather than about itself
// come back as sms.ErrRecipientOptedOut, sms.ErrInvalidRecipient, and
// sms.ErrUnverifiedRecipient, matchable with errors.Is through the wrapping.
func (s *Sender) SendSMS(ctx context.Context, details *sms.OutboundSMS) (_ *sms.Receipt, err error) {
	ctx, op := s.o11y.Begin(ctx)
	defer op.End()

	if details == nil {
		return nil, op.Error(platformerrors.ErrNilInputParameter, "sending sms")
	}

	s.instruments.Attempt(ctx)
	defer op.Time(ctx, nil, s.instruments.Latency)()
	defer func() {
		if err != nil {
			s.instruments.Failed(ctx)
		}
	}()

	// The numbers go on this service's own span and log line, where email puts
	// its addresses, and nowhere a client can read them: the sentinel messages
	// errors/http renders name no recipient, deliberately. A deployment whose
	// tracing backend must not hold phone numbers configures no tracer provider
	// for this component, which is what "absent means noop" buys.
	op.Set(toAttrKey, details.To).Set(fromAttrKey, details.From)

	// Presence is checked here; validity is not. An empty field is a caller bug
	// this package can name precisely, while whether +15551234567 is a number any
	// carrier serves is a question only Twilio can answer — and does, as
	// sms.ErrInvalidRecipient.
	switch {
	case strings.TrimSpace(details.To) == "":
		return nil, op.Error(platformerrors.ErrEmptyInputParameter, "sending sms without a recipient")
	case strings.TrimSpace(details.From) == "":
		return nil, op.Error(platformerrors.ErrEmptyInputParameter, "sending sms without a sender")
	case details.Body == "":
		return nil, op.Error(platformerrors.ErrEmptyInputParameter, "sending sms without a body")
	}

	form := url.Values{}
	form.Set("To", details.To)
	form.Set("From", details.From)
	form.Set("Body", details.Body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.messagesURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, op.Error(err, "building twilio message request")
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(s.accountSID, s.authToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, op.Error(err, "executing twilio message request")
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			op.Acknowledge(closeErr, "closing response body")
		}
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, op.Error(s.errorForResponse(op, resp), "sending sms")
	}

	var message messageResponse
	if err = json.NewDecoder(resp.Body).Decode(&message); err != nil {
		return nil, op.Error(err, "decoding twilio message response")
	}

	// A 2xx carrying no SID is an answer this package cannot honor its side of
	// the bargain with: the receipt exists so a status callback, a reply, and a
	// metering row have something to join on, and an empty one is what the noop
	// sender returns. Reporting it as a success would put a message beyond
	// reach of everything that comes after it.
	if message.SID == "" {
		return nil, op.Error(platformerrors.New("twilio accepted the message without returning a SID"), "sending sms")
	}

	op.Set(messageIDAttrKey, message.SID)

	return &sms.Receipt{ProviderMessageID: message.SID}, nil
}

// errorForResponse turns a non-2xx answer into the error a caller branches on.
//
// A body that does not decode, or that carries a code this package has no
// sentinel for, still produces an error naming the HTTP status — the request
// failed either way, and a nil error here would report a send that never
// happened as a success.
func (s *Sender) errorForResponse(op observability.Operation, resp *http.Response) error {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if readErr != nil {
		op.Acknowledge(readErr, "reading twilio error response body")

		return platformerrors.Errorf("twilio returned status %d", resp.StatusCode)
	}

	// Through encoding/json rather than this module's encoding package, which
	// rejects unknown fields: Twilio's error object carries more than the three
	// fields read here, and a strict decode would turn every provider error into
	// a decoding error instead.
	var apiErr errorResponse
	if err := json.Unmarshal(body, &apiErr); err != nil || apiErr.Code == 0 {
		return platformerrors.Errorf("twilio returned status %d: %s", resp.StatusCode, errorBodySnippet(body))
	}

	op.Set(errorCodeAttrKey, apiErr.Code)

	// The sentinel is wrapped rather than returned bare, so errors.Is matches it
	// while the log still carries Twilio's own code and prose.
	if sentinel := sentinelForCode(apiErr.Code); sentinel != nil {
		return platformerrors.Wrapf(sentinel, "twilio error %d: %s", apiErr.Code, apiErr.Message)
	}

	return platformerrors.Errorf("twilio returned status %d (error %d): %s", resp.StatusCode, apiErr.Code, apiErr.Message)
}

// errorBodySnippet renders a body that did not decode as Twilio's error object
// for the one error message that quotes it.
//
// The cut is by bytes and can land inside a rune, so what survives it is run
// through ToValidUTF8: a log line carrying half a character is a log line an
// aggregator may refuse. A body already within the bound is returned trimmed
// and otherwise untouched.
func errorBodySnippet(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) <= maxErrorBodySnippetBytes {
		return trimmed
	}

	return strings.ToValidUTF8(trimmed[:maxErrorBodySnippetBytes], "") + "…"
}

// sentinelForCode maps a Twilio error code onto the sms sentinel that says the
// same thing, or nil for a code no sentinel covers.
func sentinelForCode(code int) error {
	switch code {
	case codeRecipientOptedOut:
		return sms.ErrRecipientOptedOut
	case codeInvalidRecipient:
		return sms.ErrInvalidRecipient
	case codeUnverifiedRecipient:
		return sms.ErrUnverifiedRecipient
	default:
		return nil
	}
}
