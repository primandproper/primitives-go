package http

import (
	stderrors "errors"
	"net/http"
	"testing"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/sms"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestSMSMappings(T *testing.T) {
	T.Parallel()

	T.Run("maps an opted-out recipient to a 409 with its own code", func(t *testing.T) {
		t.Parallel()

		// Its own code, because the consumer's obligation is specific: record the
		// opt-out against the person. A general conflict would be retried
		// tomorrow.
		code, msg := ToAPIError(sms.ErrRecipientOptedOut)
		test.EqOp(t, ErrRecipientOptedOut, code)
		test.EqOp(t, "recipient has opted out of messages from this sender", msg)
		test.EqOp(t, http.StatusConflict, HTTPStatusForCode(code))
	})

	T.Run("maps an invalid recipient to bad input", func(t *testing.T) {
		t.Parallel()

		code, msg := ToAPIError(sms.ErrInvalidRecipient)
		test.EqOp(t, ErrValidatingRequestInput, code)
		test.EqOp(t, "invalid recipient phone number", msg)
		test.EqOp(t, http.StatusBadRequest, HTTPStatusForCode(code))
	})

	T.Run("maps an unverified recipient to a 500", func(t *testing.T) {
		t.Parallel()

		// A trial account is the deployment being unfinished, not the request
		// being wrong, and the remedy is on the billing page.
		code, _ := ToAPIError(sms.ErrUnverifiedRecipient)
		test.EqOp(t, ErrRecipientUnverified, code)
		test.EqOp(t, http.StatusInternalServerError, HTTPStatusForCode(code))
	})

	T.Run("maps wrapped sentinels too", func(t *testing.T) {
		t.Parallel()

		// The adapter wraps with Twilio's own code and prose, so a mapping that
		// only worked on a bare sentinel would work nowhere real.
		code, _ := ToAPIError(platformerrors.Wrapf(sms.ErrRecipientOptedOut, "twilio error %d: %s", 21610, "Attempt to send to unsubscribed recipient"))
		test.EqOp(t, ErrRecipientOptedOut, code)
	})

	T.Run("says nothing about the number", func(t *testing.T) {
		t.Parallel()

		// The message reaches the client verbatim. A phone number in it is a
		// phone number in whatever aggregates the client's logs.
		_, msg := ToAPIError(platformerrors.Wrap(sms.ErrRecipientOptedOut, "sending to +15558675309"))
		test.StrNotContains(t, msg, "5558675309")
	})

	T.Run("round-trips back to the sentinel a typed client reads", func(t *testing.T) {
		t.Parallel()

		optedOut := ErrorForCode(ErrRecipientOptedOut)
		must.NotNil(t, optedOut)
		test.True(t, stderrors.Is(optedOut, sms.ErrRecipientOptedOut))

		unverified := ErrorForCode(ErrRecipientUnverified)
		must.NotNil(t, unverified)
		test.True(t, stderrors.Is(unverified, sms.ErrUnverifiedRecipient))
	})
}
