package twilio

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	loggingnoop "github.com/primandproper/primitives-go/v2/observability/logging/noop"
	metricsnoop "github.com/primandproper/primitives-go/v2/observability/metrics/noop"
	tracingnoop "github.com/primandproper/primitives-go/v2/observability/tracing/noop"
	"github.com/primandproper/primitives-go/v2/sms"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

const (
	testAccountSID = "ACtestaccountsid"
	testAuthToken  = "testauthtoken"
)

// newTestSender stands a fake Twilio up in front of handler and returns a
// Sender pointed at it, plus the recorded form of the last request it saw.
//
// The fake is the whole point of these tests: every one of them is about what
// this package does with an answer Twilio gave, and the answers that matter
// most — an opted-out recipient, a trial account — cannot be provoked against
// the real API without a real phone number that has texted STOP.
func newTestSender(t *testing.T, handler http.HandlerFunc) (*Sender, *url.Values) {
	t.Helper()

	received := &url.Values{}

	srv := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		// Non-fatal assertions, because this runs on the server's goroutine and
		// FailNow from there does not stop the test it belongs to.
		body, err := io.ReadAll(req.Body)
		test.NoError(t, err)

		parsed, err := url.ParseQuery(string(body))
		test.NoError(t, err)

		*received = parsed

		handler(res, req)
	}))
	t.Cleanup(srv.Close)

	sender, err := NewSender(
		&Config{AccountSID: testAccountSID, AuthToken: testAuthToken, BaseURL: srv.URL},
		srv.Client(),
		WithLogger(loggingnoop.NewLogger()),
		WithTracerProvider(tracingnoop.NewTracerProvider()),
		WithMetricsProvider(metricsnoop.NewMetricsProvider()),
	)
	must.NoError(t, err)

	return sender, received
}

// twilioError renders one of Twilio's error objects, which every non-2xx answer
// carries.
func twilioError(code int, message string) string {
	return fmt.Sprintf(`{"code": %d, "message": %q, "more_info": "https://www.twilio.com/docs/errors/%d", "status": 400}`, code, message, code)
}

func TestNewSender(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(&Config{AccountSID: testAccountSID, AuthToken: testAuthToken}, &http.Client{})
		must.NoError(t, err)
		must.NotNil(t, sender)

		// The default host, since no BaseURL was given.
		test.StrHasPrefix(t, defaultBaseURL, sender.messagesURL)
		test.StrHasSuffix(t, "/Messages.json", sender.messagesURL)
	})

	T.Run("with a nil config", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(nil, &http.Client{})
		test.ErrorIs(t, err, ErrNilConfig)
		test.Nil(t, sender)
	})

	T.Run("with an empty account SID", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(&Config{AuthToken: testAuthToken}, &http.Client{})
		test.ErrorIs(t, err, ErrEmptyAccountSID)
		test.Nil(t, sender)
	})

	T.Run("with an empty auth token", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(&Config{AccountSID: testAccountSID}, &http.Client{})
		test.ErrorIs(t, err, ErrEmptyAuthToken)
		test.Nil(t, sender)
	})

	T.Run("with a nil HTTP client", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(&Config{AccountSID: testAccountSID, AuthToken: testAuthToken}, nil)
		test.ErrorIs(t, err, ErrNilHTTPClient)
		test.Nil(t, sender)
	})

	T.Run("with a base URL override", func(t *testing.T) {
		t.Parallel()

		// The trailing slash is trimmed rather than doubled into the path.
		sender, err := NewSender(&Config{AccountSID: testAccountSID, AuthToken: testAuthToken, BaseURL: "https://example.com/"}, &http.Client{})
		must.NoError(t, err)
		test.EqOp(t, "https://example.com/2010-04-01/Accounts/"+testAccountSID+"/Messages.json", sender.messagesURL)
	})
}

func TestSender_SendSMS(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		var sawAuth bool

		sender, received := newTestSender(t, func(res http.ResponseWriter, req *http.Request) {
			user, pass, ok := req.BasicAuth()
			sawAuth = ok && user == testAccountSID && pass == testAuthToken

			res.Header().Set("Content-Type", "application/json")
			res.WriteHeader(http.StatusCreated)
			_, _ = res.Write([]byte(`{"sid": "SM0123456789", "status": "queued"}`))
		})

		receipt, err := sender.SendSMS(t.Context(), &sms.OutboundSMS{
			To:   "+15558675309",
			From: "+15551112222",
			Body: "your code is 123456",
		})
		must.NoError(t, err)
		must.NotNil(t, receipt)

		test.EqOp(t, "SM0123456789", receipt.ProviderMessageID)
		test.True(t, sawAuth)
		test.EqOp(t, "+15558675309", received.Get("To"))
		test.EqOp(t, "+15551112222", received.Get("From"))
		test.EqOp(t, "your code is 123456", received.Get("Body"))
	})

	T.Run("passes a unicode body through untouched", func(t *testing.T) {
		t.Parallel()

		// The first consumer's texts are Spanish. Nothing here transliterates or
		// re-encodes: the segment cost of a UCS-2 body is Twilio's to compute and
		// the consumer's to pay, and a body silently stripped of its accents is a
		// message the recipient did not write.
		const body = "Recordatorio: su cita es mañana a las 10:00. ¿Confirma? Sí/No"

		sender, received := newTestSender(t, func(res http.ResponseWriter, _ *http.Request) {
			res.WriteHeader(http.StatusCreated)
			_, _ = res.Write([]byte(`{"sid": "SMunicode"}`))
		})

		receipt, err := sender.SendSMS(t.Context(), &sms.OutboundSMS{To: "+15558675309", From: "+15551112222", Body: body})
		must.NoError(t, err)
		test.EqOp(t, "SMunicode", receipt.ProviderMessageID)
		test.EqOp(t, body, received.Get("Body"))
	})

	T.Run("does not split a long body", func(t *testing.T) {
		t.Parallel()

		// Segment counting is Twilio's. A body well past one segment is sent as
		// one request with one body, and what it costs comes back on Twilio's bill
		// rather than out of a table this module would have to maintain.
		body := strings.Repeat("a", 1_000)

		sender, received := newTestSender(t, func(res http.ResponseWriter, _ *http.Request) {
			res.WriteHeader(http.StatusCreated)
			_, _ = res.Write([]byte(`{"sid": "SMlong"}`))
		})

		_, err := sender.SendSMS(t.Context(), &sms.OutboundSMS{To: "+15558675309", From: "+15551112222", Body: body})
		must.NoError(t, err)
		test.EqOp(t, body, received.Get("Body"))
	})

	T.Run("with a nil message", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(&Config{AccountSID: testAccountSID, AuthToken: testAuthToken}, &http.Client{})
		must.NoError(t, err)

		receipt, err := sender.SendSMS(t.Context(), nil)
		test.ErrorIs(t, err, platformerrors.ErrNilInputParameter)
		test.Nil(t, receipt)
	})

	for name, message := range map[string]*sms.OutboundSMS{
		"without a recipient": {From: "+15551112222", Body: "hi"},
		"without a sender":    {To: "+15558675309", Body: "hi"},
		"without a body":      {To: "+15558675309", From: "+15551112222"},
	} {
		T.Run(name, func(t *testing.T) {
			t.Parallel()

			sender, err := NewSender(&Config{AccountSID: testAccountSID, AuthToken: testAuthToken}, &http.Client{})
			must.NoError(t, err)

			receipt, err := sender.SendSMS(t.Context(), message)
			test.ErrorIs(t, err, platformerrors.ErrEmptyInputParameter)
			test.Nil(t, receipt)
		})
	}
}

func TestSender_SendSMS_sentinels(T *testing.T) {
	T.Parallel()

	// The three refusals Twilio makes about the message rather than about
	// itself, each of which a consumer has something specific to do about.
	cases := map[string]struct {
		sentinel error
		message  string
		code     int
	}{
		"opted out": {
			code:     codeRecipientOptedOut,
			message:  "Attempt to send to unsubscribed recipient",
			sentinel: sms.ErrRecipientOptedOut,
		},
		"invalid recipient": {
			code:     codeInvalidRecipient,
			message:  "The 'To' number is not a valid phone number.",
			sentinel: sms.ErrInvalidRecipient,
		},
		"unverified recipient": {
			code:     codeUnverifiedRecipient,
			message:  "The number is unverified. Trial accounts may only send messages to verified numbers.",
			sentinel: sms.ErrUnverifiedRecipient,
		},
	}

	for name, tc := range cases {
		T.Run(name, func(t *testing.T) {
			t.Parallel()

			sender, _ := newTestSender(t, func(res http.ResponseWriter, _ *http.Request) {
				res.Header().Set("Content-Type", "application/json")
				res.WriteHeader(http.StatusBadRequest)
				_, _ = res.Write([]byte(twilioError(tc.code, tc.message)))
			})

			receipt, err := sender.SendSMS(t.Context(), &sms.OutboundSMS{To: "+15558675309", From: "+15551112222", Body: "hi"})
			must.Error(t, err)
			test.Nil(t, receipt)

			test.ErrorIs(t, err, tc.sentinel)

			// Twilio's own code and prose survive the wrapping, which is what an
			// operator reading the log has to go on.
			test.StrContains(t, err.Error(), strconv.Itoa(tc.code))

			// Each sentinel is distinguishable from the other two, which is the
			// whole reason there are three of them.
			for otherName, other := range cases {
				if otherName != name {
					test.False(t, platformerrors.Is(err, other.sentinel), test.Sprintf("%s also matched %s", name, otherName))
				}
			}
		})
	}
}

func TestSender_SendSMS_providerFailures(T *testing.T) {
	T.Parallel()

	// Every sentinel, so each case can assert it claimed none of them.
	sentinels := []error{sms.ErrRecipientOptedOut, sms.ErrInvalidRecipient, sms.ErrUnverifiedRecipient}

	assertNoSentinel := func(t *testing.T, err error) {
		t.Helper()

		for _, sentinel := range sentinels {
			test.False(t, platformerrors.Is(err, sentinel))
		}
	}

	T.Run("with a 5xx", func(t *testing.T) {
		t.Parallel()

		sender, _ := newTestSender(t, func(res http.ResponseWriter, _ *http.Request) {
			res.WriteHeader(http.StatusInternalServerError)
			_, _ = res.Write([]byte(`{"code": 20500, "message": "Internal server error", "status": 500}`))
		})

		receipt, err := sender.SendSMS(t.Context(), &sms.OutboundSMS{To: "+15558675309", From: "+15551112222", Body: "hi"})
		must.Error(t, err)
		test.Nil(t, receipt)
		assertNoSentinel(t, err)
		test.StrContains(t, err.Error(), "500")
	})

	T.Run("with an error code no sentinel covers", func(t *testing.T) {
		t.Parallel()

		// 21617 is "message body is too long". The issue this package was written
		// for is explicit that length is Twilio's to enforce, so a body it rejects
		// arrives here as an ordinary wrapped provider error.
		sender, _ := newTestSender(t, func(res http.ResponseWriter, _ *http.Request) {
			res.WriteHeader(http.StatusBadRequest)
			_, _ = res.Write([]byte(twilioError(21617, "The concatenated message body exceeds the 1600 character limit.")))
		})

		receipt, err := sender.SendSMS(t.Context(), &sms.OutboundSMS{To: "+15558675309", From: "+15551112222", Body: "hi"})
		must.Error(t, err)
		test.Nil(t, receipt)
		assertNoSentinel(t, err)
		test.StrContains(t, err.Error(), "21617")
	})

	T.Run("with an error body that is not Twilio's", func(t *testing.T) {
		t.Parallel()

		// A proxy or a load balancer answering instead of Twilio. It is still a
		// failed send, and reporting it as a success would record a message that
		// was never accepted.
		sender, _ := newTestSender(t, func(res http.ResponseWriter, _ *http.Request) {
			res.WriteHeader(http.StatusBadGateway)
			_, _ = res.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
		})

		receipt, err := sender.SendSMS(t.Context(), &sms.OutboundSMS{To: "+15558675309", From: "+15551112222", Body: "hi"})
		must.Error(t, err)
		test.Nil(t, receipt)
		assertNoSentinel(t, err)
		test.StrContains(t, err.Error(), "502")
	})

	T.Run("with a success status and an undecodable body", func(t *testing.T) {
		t.Parallel()

		sender, _ := newTestSender(t, func(res http.ResponseWriter, _ *http.Request) {
			res.WriteHeader(http.StatusCreated)
			_, _ = res.Write([]byte(`not json`))
		})

		receipt, err := sender.SendSMS(t.Context(), &sms.OutboundSMS{To: "+15558675309", From: "+15551112222", Body: "hi"})
		must.Error(t, err)
		test.Nil(t, receipt)
	})

	T.Run("with an unreachable server", func(t *testing.T) {
		t.Parallel()

		sender, err := NewSender(&Config{
			AccountSID: testAccountSID,
			AuthToken:  testAuthToken,
			// A port nothing is listening on, so the transport fails before any
			// status exists to interpret.
			BaseURL: "http://127.0.0.1:1",
		}, &http.Client{})
		must.NoError(t, err)

		receipt, err := sender.SendSMS(t.Context(), &sms.OutboundSMS{To: "+15558675309", From: "+15551112222", Body: "hi"})
		must.Error(t, err)
		test.Nil(t, receipt)
	})
}

func TestSentinelForCode(T *testing.T) {
	T.Parallel()

	T.Run("claims nothing for an unknown code", func(t *testing.T) {
		t.Parallel()

		test.Nil(t, sentinelForCode(0))
		test.Nil(t, sentinelForCode(21617))
	})
}

func TestSender_SendSMS_missingSID(T *testing.T) {
	T.Parallel()

	T.Run("refuses a success that names no message", func(t *testing.T) {
		t.Parallel()

		// The receipt exists so a status callback, a reply, and a metering row
		// have something to join on. An empty one is what the noop sender
		// returns, and reporting this as a success would put the message beyond
		// reach of all three.
		sender, _ := newTestSender(t, func(res http.ResponseWriter, _ *http.Request) {
			res.WriteHeader(http.StatusCreated)
			_, _ = res.Write([]byte(`{"status": "queued"}`))
		})

		receipt, err := sender.SendSMS(t.Context(), &sms.OutboundSMS{To: "+15558675309", From: "+15551112222", Body: "hi"})
		must.Error(t, err)
		test.Nil(t, receipt)
	})
}

func TestErrorBodySnippet(T *testing.T) {
	T.Parallel()

	T.Run("returns a short body trimmed and whole", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, "<html>502</html>", errorBodySnippet([]byte("  <html>502</html>\n")))
	})

	// io.LimitReader truncates silently where http.MaxBytesReader failed the
	// read, so without this bound a misrouted request that landed on something
	// chatty would put 64 KiB of someone else's HTML into an error message.
	T.Run("bounds a body that is too long to quote", func(t *testing.T) {
		t.Parallel()

		snippet := errorBodySnippet([]byte(strings.Repeat("a", maxErrorBodyBytes)))

		test.EqOp(t, maxErrorBodySnippetBytes+len("…"), len(snippet))
		test.StrHasSuffix(t, "…", snippet)
	})

	// The cut is by bytes and lands mid-rune here: "é" is two bytes, so a bound
	// that is odd relative to the run splits the last one.
	T.Run("drops a rune the cut split in half", func(t *testing.T) {
		t.Parallel()

		snippet := errorBodySnippet([]byte("a" + strings.Repeat("é", maxErrorBodySnippetBytes)))

		test.True(t, utf8.ValidString(snippet))
	})
}
