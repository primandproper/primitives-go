/*
Package grpc translates errors into gRPC statuses, and back again on the other
side of the wire.

MapToGRPC resolves a Go error to a codes.Code, consulting PlatformMapper first
and then whatever mappers domains have registered. The interceptors apply that
mapping to whatever a handler returns, and DecodeErrorFromStatus reconstructs the
original error on the client, so errors.Is keeps matching across a service
boundary.

The sentinel set PlatformMapper maps is the same one errors/http's maps,
deliberately. A service exposing both transports would otherwise answer one
failure with a considered status on one and codes.Unknown on the other, and which
the client got would depend on how it happened to connect. Each domain mapper
holds the same property for its own sentinels.

# Which direction the imports run

This package imports the packages whose sentinels PlatformMapper maps —
circuitbreaking, database, idempotency, ratelimiting, requestsigning, and the two
search indexes. Every one of them is a primitive, and that is the whole of the
list on purpose: this package is a primitive too, so nothing built on those may
appear in it.

The tier above maps itself. dataprivacy, identity, links, operations and
sessions each export a GRPCMapper holding the cases for their own sentinels, and the import
runs from them to here. Anything else with a sentinel a client should act on does
the same: declare a mapper beside the sentinel, and register it.

Registration is what makes a mapper reachable. RegisterGRPCErrorMapper appends
one; MapToGRPC consults PlatformMapper first, then registered mappers in
registration order. RegisterClientSafeSentinels is the companion for the second
half of the answer — whether a sentinel's own words reach the client — and
RegisterClientSafeReasons for the third, whether the response also carries a
stable identifier a client can branch on. All three are described below. This
module's four are one call that does them together, errormappers.Register,
which service.Register makes for a service built from a service.Config and a
service assembled by hand makes itself, alongside the mappers it declares for its
own sentinels. There is deliberately no init doing it: a mapper that installs
itself into a process-wide registry by being linked in is a side effect a
consumer cannot opt out of.

# What a handler returns

PrepareAndLogGRPCStatus is the spelling a handler wants. It logs, traces, maps
the code and hands back an error that is still the error it was given: the
sentinel chain intact, with the status alongside it rather than rendered into it.

That last part is the whole point. UnaryErrorEncodingInterceptor can only encode
the chain it is handed, so a handler that returned status.Errorf(code, "%v", err)
— which is what this function itself used to do — hands it a leaf whose only
content is a message, and the sentinel is not merely unmatched on the far side
but gone. See observability.GRPCStatusError, which is the error type underneath
and where the reasoning is written down.

# What reaches the client, and what that assumes

The status message is derived from the code rather than from the error's text,
which is the whole wrapped chain and can name tables, connection strings, and the
permission that was missing. Two things stand in for the code's name. The first
is a list of platform sentinels documented as client-safe, whose own wording
tells a caller what to do differently without describing the policy behind the
refusal, plus whatever a domain has added to it with RegisterClientSafeSentinels.
The second is the description a handler passed PrepareAndLogGRPCStatus — a short
account of what it was doing, written for this reader by the code that knew — and
a client-safe sentinel outranks it, since the sentinel is the more specific of
the two. The interceptors have no description to offer for an error a handler
returned bare, and fall back to the code.

The full error does still cross the wire, encoded in the status details, and that
is what makes the error reconstructable on the far side. It is meant for trusted
service-to-service traffic. A server running these interceptors and reachable by
untrusted clients needs that detail stripped at the edge — otherwise the internal
error text this package took care to keep out of the message is available in the
details of the same response. StripEncodedErrorDetail is that edge's spelling: it
removes exactly the encoded detail and leaves the rest of the response alone.

# The third channel

Neither of those two gives a client something to switch on. The message is prose,
written for a person and rewordable at any time; the details are the peer's and
are supposed to be stripped before a client sees them. A caller that has to
branch on which refusal it got — show a second-factor prompt for one
Unauthenticated, a password field for another — has historically had only the
English sentence, which makes the sentence a public interface nobody may reword
and every client matches differently.

RegisterClientSafeReasons closes that. A registered sentinel carries a stable
UPPER_SNAKE_CASE identifier, which the interceptors emit in a google.rpc.ErrorInfo
detail — distinct from the encoded-error detail, so "strip the peer detail, keep
the client detail" is something an edge can express. Registering a reason also
registers the sentinel as client-safe, since both halves say the same thing about
the same refusal. ClientReasonFromStatus is the client's read, and it needs
neither the encoded detail nor the decoding interceptor.

This discloses nothing new: anything that can read a reason can already read the
message saying the same thing in prose. It only narrows what a client depends on.
*/
package grpc

//platform:transport mapping: a sentinel to a gRPC code, and back
