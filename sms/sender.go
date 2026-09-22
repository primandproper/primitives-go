package sms

import (
	"context"
)

type (
	// OutboundSMS is a collection of fields that are useful for sending text
	// messages.
	//
	// It carries only what a provider needs to address and render one message.
	// To and From are E.164 — a leading plus and digits, no separators — because
	// that is the only recipient format every carrier-facing API in this family
	// agrees on. Application identifiers — which client this concerns, which
	// reminder produced it — belong to the application's own envelope, not to
	// this module's transport type.
	//
	// Body is passed through untouched. Length limits, segment counting, and the
	// GSM-versus-UCS-2 encoding choice are the provider's, which is why nothing
	// here splits a long body or transliterates a non-ASCII one: a message that
	// arrives as three segments is a billing fact the provider reports, and one
	// this module cannot compute without duplicating a table that differs per
	// carrier.
	OutboundSMS struct {
		To   string `json:"to"`
		From string `json:"from"`
		Body string `json:"body"`
	}

	// Receipt is what the provider said about a message it accepted.
	//
	// It exists because a text has a delivery lifecycle that an email send, as
	// this module models it, does not: the provider's message ID is what a status
	// callback, an inbound reply thread, and a metering row all key on, and a
	// consumer that never receives it has no way to join any of the three back to
	// the send that caused them.
	//
	// It carries the ID and nothing else, deliberately. Delivery status is not
	// here because it is not known yet — the provider answers "accepted" in
	// milliseconds and "delivered" or "failed" minutes later, over a webhook —
	// and a status field filled in at send time would record the first while
	// looking like the second. Recording the eventual outcome is the consumer's.
	Receipt struct {
		ProviderMessageID string `json:"providerMessageID"`
	}

	// Sender represents a service that can send text messages.
	Sender interface {
		SendSMS(ctx context.Context, details *OutboundSMS) (*Receipt, error)
	}
)
