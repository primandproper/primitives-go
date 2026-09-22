package noop

import (
	"testing"

	"github.com/primandproper/primitives-go/v2/sms"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestNewSender(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		test.NotNil(t, NewSender())
	})
}

func TestSender_SendSMS(T *testing.T) {
	T.Parallel()

	T.Run("standard", func(t *testing.T) {
		t.Parallel()

		receipt, err := NewSender().SendSMS(t.Context(), &sms.OutboundSMS{
			To:   "+15558675309",
			From: "+15551112222",
			Body: "your code is 123456",
		})
		must.NoError(t, err)
		must.NotNil(t, receipt)

		// Empty rather than a plausible-looking ID. No provider accepted the
		// message, so no provider named it, and a test that keys a status
		// callback on this notices.
		test.EqOp(t, "", receipt.ProviderMessageID)
	})

	T.Run("with a nil message", func(t *testing.T) {
		t.Parallel()

		receipt, err := NewSender().SendSMS(t.Context(), nil)
		must.NoError(t, err)
		test.NotNil(t, receipt)
	})
}
