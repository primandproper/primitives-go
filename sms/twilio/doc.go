/*
Package twilio sends text messages through Twilio's Messages API.

Choosing it commits a deployment to a Twilio account SID and auth token, which
together are both the credential and part of the request path: messages are
billed to the account in the URL. Config.BaseURL overrides the API host, which
is what points the package at an httptest server.

# No vendor SDK

The request is built here, against an *http.Client the caller supplies, rather
than through twilio-go. One form-encoded POST and one JSON object back is the
whole of the surface this package needs, and the SDK's is several hundred
generated types wide — every one of which would land in the import graph of any
service that sends a text. The caller's client is also what bounds a send: this
package sets no timeout of its own and does not retry.

# What one send is

SendSMS delivers one message to one recipient and returns when Twilio has
answered, carrying the message SID it assigned. That SID is the whole of the
receipt, and it is what a later status callback, an inbound reply, and a
metering row all key on.

"Answered" means accepted, not delivered. Twilio queues the message and hands it
to a carrier afterwards, so a nil error here says the message was taken, and
whether it arrived is reported minutes later over a webhook this package has
nothing to do with.

# Errors

Three of Twilio's error codes are refusals about the message rather than about
Twilio, and each becomes the sms sentinel that says the same thing, wrapped so
that errors.Is matches while the log keeps Twilio's own code and prose:

	21610   sms.ErrRecipientOptedOut
	21211   sms.ErrInvalidRecipient
	21608   sms.ErrUnverifiedRecipient

Every other code — a body rejected for length, a revoked credential, a 500 —
comes back as a wrapped error naming the HTTP status, Twilio's numeric code, and
its message. Nothing here inspects the body's length or splits it; segment
counting is Twilio's, and a body it rejects is reported rather than pre-empted.
*/
package twilio
