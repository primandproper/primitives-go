package email

import (
	"net/mail"
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestFormatAddress(T *testing.T) {
	T.Parallel()

	T.Run("bare address when there is no name", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, "a@example.com", FormatAddress("", "a@example.com"))
		test.EqOp(t, "a@example.com", FormatAddress("   ", "a@example.com"))
	})

	// The reason this is a function rather than a Sprintf. Several providers
	// accept a comma-separated list wherever they accept an address, so an
	// unescaped comma in a display name adds recipients.
	T.Run("quotes a display name containing a comma", func(t *testing.T) {
		t.Parallel()

		got := FormatAddress(`Spot, attacker@evil.example`, "a@example.com")

		test.StrContains(t, got, `"Spot, attacker@evil.example"`)
		test.StrHasSuffix(t, "<a@example.com>", got)
	})

	T.Run("escapes quotes in a display name", func(t *testing.T) {
		t.Parallel()

		got := FormatAddress(`Spot "the" Dog`, "a@example.com")

		test.StrContains(t, got, `\"the\"`)
	})

	// The property, checked rather than asserted about the rendering: whatever a
	// hostile name contains, the result parses as exactly one mailbox and that
	// mailbox is the address the caller named.
	T.Run("a hostile name still yields one recipient", func(t *testing.T) {
		t.Parallel()

		got := FormatAddress(`x <a@attacker.example>,`, "real@example.com")

		parsed, err := mail.ParseAddress(got)
		must.NoError(t, err)
		test.EqOp(t, "real@example.com", parsed.Address)

		list, err := mail.ParseAddressList(got)
		must.NoError(t, err)
		test.SliceLen(t, 1, list)
	})
}

func TestOutboundEmailMessage_ReplyTo(T *testing.T) {
	T.Parallel()

	T.Run("names none", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, "", (&OutboundEmailMessage{ReplyToName: "Ignored without an address"}).ReplyTo())
		test.EqOp(t, "", (&OutboundEmailMessage{ReplyToAddress: "   "}).ReplyTo())
		test.EqOp(t, "", (*OutboundEmailMessage)(nil).ReplyTo())
	})

	T.Run("a bare address", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, "owner@example.com", (&OutboundEmailMessage{ReplyToAddress: "owner@example.com"}).ReplyTo())
	})

	T.Run("a display name is quoted", func(t *testing.T) {
		t.Parallel()

		got := (&OutboundEmailMessage{ReplyToAddress: "owner@example.com", ReplyToName: "Owner, Studio"}).ReplyTo()
		test.EqOp(t, `"Owner, Studio" <owner@example.com>`, got)
	})
}
