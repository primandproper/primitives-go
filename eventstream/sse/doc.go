/*
Package sse upgrades an HTTP request to a Server-Sent Events stream.

It is the send-only half of eventstream's two transports. Upgrader satisfies
eventstream.EventStreamUpgrader and not the bidirectional one, because SSE has
no client-to-server channel: a client that needs to talk back does so with an
ordinary request, or uses the websocket sibling.

Choosing it commits a caller to plain HTTP with no handshake, no subprotocol
negotiation, and no dependency beyond net/http — a stream survives proxies and
corporate middleboxes that refuse an Upgrade. What it costs is listed below.

# The response is committed at upgrade time

UpgradeToEventStream writes the text/event-stream headers and flushes before it
returns, so by the time a handler has a stream it can no longer choose a status
code. Anything that could fail the request has to fail before the upgrade.

The ResponseWriter must also implement http.Flusher. That check happens first,
before anything is written, and it is the only failure the upgrade reports while
a handler still has a status code to answer with. An Upgrader built with
WithReconnectDelay writes one frame before it flushes, and a write that fails
there is returned too — but by then the response is committed, so a handler that
receives that error has nothing left to say and can only abandon the request.

# Reconnect timing

A client reconnects on its own when the stream drops, after waiting an interval
the HTML specification declines to fix: "an implementation-defined value,
probably in the region of a few seconds", which is three seconds in Chromium and
WebKit and five in Gecko. The server is the only party that knows which way that
number is wrong — too eager for a fleet reconnecting into a proxy that has just
restarted, too slow for a stream a person is watching — and WithReconnectDelay
is how it says so:

	upgrader, err := sse.NewUpgrader(sse.WithReconnectDelay(15 * time.Second))

Every stream that Upgrader produces then opens with a "retry:" field carrying the
delay in milliseconds, which a client adopts as its reconnection time and keeps
until told otherwise. The value belongs to the client's stream object rather than
to the page, so a reload starts over at the implementation default — but each
reconnect is a new request and so a new upgrade, which delivers the field again.

Naming no delay emits no "retry:" at all and leaves the client on its own
default. That is what every caller had before the option existed and is still
what an Upgrader built without it does.

The field carries whole milliseconds, so a delay is truncated toward zero: 1500µs
emits "retry: 1". Below a millisecond the truncation reaches "retry: 0", which is
a well-formed instruction to reconnect immediately and is honored as one, so that
band is refused at construction instead — a delay under a millisecond, zero and
negative included, makes NewUpgrader return an error matching
ErrInvalidReconnectDelay. The floor also catches the unit slip that produces it,
WithReconnectDelay(3000) written for "three seconds", which is three microseconds.

There is no upper bound. A long delay is a coherent instruction to wait a long
time, and which values are unreasonable is a judgment this package has no
information to make. The value is advisory in the other direction too: it is what
a conforming client is told to use, a non-browser client is free to ignore it,
and nothing here observes whether it was honored.

# There is no keepalive, and no comment frame

Nothing is written when there are no events. A client that has gone away is
discovered on the next Send, and an idle connection is at the mercy of whatever
proxy timeout sits in front of it — this package sends no comment frames and no
pings, and Done fires from the request's own context rather than from any
liveness check. The websocket sibling pings on an interval and closes a stream
whose peer stops answering; that is the substantive difference between the two
for a long-lived, low-traffic stream.

Nor will there be a comment frame (": text"), the conventional SSE keepalive.
That is a decision rather than an omission, for two reasons. A stream is handed
back as eventstream.EventStream, an interface the websocket sibling satisfies
too and which has no room for an SSE-only Comment method, so the method would be
reachable only through a type assertion by a caller that had already committed
to this transport. And the one thing a comment is genuinely for is served by
what is already here: an event with an empty payload and a type of its own goes
through Send, keeps the connection warm, and is ignored by a client listening for
no such type. A caller that wants a heartbeat writes one on a ticker and picks
its own type; a caller that wants the transport to run one has described the
websocket sibling.

# Framing

An event is written as an optional "event:" line and one "data:" line per line
of payload, terminated by a blank line. CR and CRLF are normalized to LF and the
event type has its newlines stripped, so neither a multi-line payload nor an
attacker-supplied type can break the framing or inject additional SSE fields.

A configured reconnect delay is written once, ahead of all of that, as a lone
"retry:" field followed by a blank line. A blank line with nothing buffered
before it dispatches no event, so the frame sets the client's reconnection time
and delivers nothing to a handler — it is well-formed on its own and does not
need an event to carry it.

No "id:" field is emitted. A client reconnecting therefore has no Last-Event-ID
to send and no way to ask for what it missed; a stream resumes as a new one from
the present moment.

Close cancels the stream's context so Done fires and the handler unblocks. It
writes nothing to the client, which learns of the close when the response body
ends.
*/
package sse
