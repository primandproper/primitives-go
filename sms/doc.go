/*
Package sms sends text messages, over one vendor or nowhere at all.

The interface is Sender, and it is deliberately one method: send this
OutboundSMS, or say why not. Templates, scheduling, per-tenant sender numbers,
rate limiting, segment accounting, and the A2P registration a US long code needs
are not here — an application that wants them composes them around this, and an
application that does not is spared them.

# Why this is a primitive

A sender is a provider behind an interface, which is the first kind of thing
this module ships and no service is. An application with no users still needs
one to send a verification code, which is the test. What it deliberately does
not ship is anything that owns the person on the other end: there is no contacts
table here, no consent record, no opt-out list. Those are a product's nouns,
with a lifecycle and a privacy obligation, and they live in the tier that has
tables.

The seam between the two is ErrRecipientOptedOut. The opt-out itself belongs to
the carrier — it is made by a person texting STOP, and it binds every sender on
that number — and what this package owes a consumer is a distinct error saying
so, rather than a generic failure that gets retried. Recording it against the
person is the consumer's, and it is the whole of what a "contacts" primitive
would have been for.

# The providers

Each is a subpackage translating an OutboundSMS onto one vendor's API, and each
constructor returns its own concrete type rather than the interface:

	twilio    Twilio's Messages API
	noop      accepts every message and delivers none

[github.com/primandproper/primitives-go/v2/sms/config] is what selects one from
configuration. Its provider roster is the ground truth for the list above, which
is checked against it rather than maintained beside it.

noop is not a fallback. An unset or misspelled provider is
[github.com/primandproper/primitives-go/v2/errors.ErrUnknownProvider], because
outbound texts that silently go nowhere are discovered by the people who never
received their verification code. Discarding messages has to be asked for by
name.

# The sentinel contract

Three refusals are the provider's answer about the message rather than about
itself, and an adapter must raise them so that errors.Is matches:
ErrRecipientOptedOut, ErrInvalidRecipient, and ErrUnverifiedRecipient. Each is
mapped on both transports by errors/http and errors/grpc, so a consumer gets the
same three answers whether it called in-process or over a wire.

Every other provider failure — a body the vendor rejected for length, a revoked
credential, a 500 — comes back as the adapter's own wrapped error, carrying the
status and the vendor's message. Nothing here inspects a body's length or splits
it; see OutboundSMS on why that is the provider's to decide.
*/
package sms
