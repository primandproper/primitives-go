package grpc

import (
	"sync"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// ClientReason is a sentinel and the stable identifier a client branches on when
// it gets that refusal.
//
// It is the third channel, and the one the other two could not be. The status
// message is client-safe prose — a registered sentinel's own words, which are
// written for a person and may be reworded, localized or shortened at any time.
// The status details carry the whole encoded chain, which is for a peer this
// process trusts and which a client-facing edge is supposed to strip. Neither
// gives a client something to switch on: a caller that must show a TOTP prompt
// for one Unauthenticated and a password field for another has, today, only the
// English sentence, so the sentence becomes a public interface that cannot be
// reworded and that every client reinvents its own match against.
//
// Reason is that switch. It is emitted in a google.rpc.ErrorInfo detail, which
// is distinct from the encoded-error detail on purpose: "strip the peer detail,
// keep the client detail" is then something an edge can express, rather than the
// all-or-nothing judgement call stripping is now.
//
// Nothing here widens what a client can learn. Anything that can read a reason
// can already read the message saying the same thing in prose; this only narrows
// what a client depends on.
//
// Reason should be UPPER_SNAKE_CASE, which is google.rpc.ErrorInfo's own
// convention and what tooling in other languages expects. It is a name a client
// compiles against, so it is chosen once and never reworded — reword the
// sentinel's message instead, which is what the message channel is for.
//
// Domain is the logical grouping the reason belongs to, conventionally the
// service that issued it, and it is what makes two services' reasons distinct
// when a gateway merges them. A closed system that only ever talks to one server
// may leave it empty.
//
// There is deliberately no Metadata, which google.rpc.ErrorInfo also carries: a
// value fixed at registration time is the same for every occurrence and says
// nothing a client could not hardcode beside the reason, and a per-occurrence
// one would have to be read off the error, which is a different feature.
type ClientReason struct {
	// Err is the sentinel, matched the way ClientSafeMessage matches: the
	// outermost node of the chain that is this error supplies the reason.
	Err error
	// Reason is the stable identifier, UPPER_SNAKE_CASE.
	Reason string
	// Domain is the logical grouping the reason belongs to. May be empty.
	Domain string
}

// Detail renders the reason as the google.rpc.ErrorInfo the interceptors put on
// the wire.
//
// It is exported for the handler that builds its own status by hand — carrying a
// code the mappers would not pick, or a message this package cannot guess — and
// wants that status to carry the same detail a handler going through the
// interceptors gets for free. Such a handler reaches it through
// ClientSafeReason, which hands back the registered record this is a method on.
func (r ClientReason) Detail() *errdetails.ErrorInfo {
	return &errdetails.ErrorInfo{
		Reason: r.Reason,
		Domain: r.Domain,
	}
}

var (
	registeredReasons   []ClientReason
	registeredReasonsMu sync.RWMutex
)

// RegisterClientSafeReasons records the stable identifiers the interceptors may
// put on the wire for the sentinels they name.
//
// It is the third of the registration calls, beside RegisterGRPCErrorMapper,
// which decides the status code, and RegisterClientSafeSentinels, which decides
// whether the status carries the sentinel's own words. This one decides whether
// the response also carries something a client can switch on — see ClientReason
// for why the other two could not.
//
// Registering here registers the sentinel as client-safe too, so a sentinel with
// a reason does not also need a RegisterClientSafeSentinels call. The two are
// halves of one statement — this refusal is disclosable, here is what to call it
// and here is what to say about it — and keeping them in one call is what stops
// them from drifting apart. The rule RegisterClientSafeSentinels states applies
// unchanged: a sentinel whose text names a table, a key, or a policy must not be
// registered here at all, reason or no reason.
//
// An entry with a nil Err or an empty Reason is ignored, since neither half of
// it can answer anything.
//
// It is additive and safe to call from more than one goroutine, and a sentinel
// registered twice costs a second comparison and nothing else. Where the same
// sentinel is registered with two different reasons the first one registered
// wins, for the same reason the chain walk takes the first match: a later
// registration reaching back and changing what a client already branches on is
// the one thing a stable identifier may not do.
func RegisterClientSafeReasons(reasons ...ClientReason) {
	registeredReasonsMu.Lock()
	registeredReasons = append(registeredReasons, reasons...)
	registeredReasonsMu.Unlock()

	sentinels := make([]error, 0, len(reasons))
	for i := range reasons {
		if reasons[i].Err == nil || reasons[i].Reason == "" {
			continue
		}
		sentinels = append(sentinels, reasons[i].Err)
	}

	if len(sentinels) > 0 {
		RegisterClientSafeSentinels(sentinels...)
	}
}

// ClientSafeReason reports the registered reason for err: the record whose Err
// is the first node of err's chain that has one, and false when no node does.
//
// "First" is the same position ClientSafeMessage means — the chain walked
// outermost-first, depth-first through a Join in the order it was joined — so a
// registered wrapper outranks the registered sentinel it wraps, and a chain that
// resolves to one sentinel for the message resolves to that same sentinel for
// the reason, since registering a reason registers the message too.
//
// The one way the two channels can name different nodes is a message-only
// sentinel wrapping a reason-bearing one, where each answers with the most
// specific node it has. That is the right answer for both — a client reading
// prose and a client reading an identifier each get the nearest thing their own
// channel knows about — but it is worth knowing before wrapping a sentinel that
// has a reason in one that does not.
//
// The interceptors consult it for every error they encode. It stays exported for
// the handler shaping its own status, which wants ClientReason.Detail, and for
// an edge that would rather read the reason off the error it already has than
// off the status it is about to send.
func ClientSafeReason(err error) (ClientReason, bool) {
	if err == nil {
		return ClientReason{}, false
	}

	registeredReasonsMu.RLock()
	registered := registeredReasons
	registeredReasonsMu.RUnlock()

	return firstMatchingNode(err, func(node error) (ClientReason, bool) {
		for i := range registered {
			if registered[i].Err == nil || registered[i].Reason == "" {
				continue
			}
			if nodeIs(node, registered[i].Err) {
				return registered[i], true
			}
		}

		return ClientReason{}, false
	})
}

// clientReasonDetail returns the detail the interceptors attach for err, and nil
// when no node of its chain has a registered reason.
func clientReasonDetail(err error) *errdetails.ErrorInfo {
	reason, ok := ClientSafeReason(err)
	if !ok {
		return nil
	}

	return reason.Detail()
}

// ClientReasonFromStatus reads the google.rpc.ErrorInfo a server attached, and
// false when there is none.
//
// This is the client half, and it works on the status as it arrived: it needs
// neither the encoded detail nor the decoding interceptor, which is the point —
// a reason survives an edge that stripped the encoded chain, and a client in a
// language with no cockroachdb/errors can read the same field out of the same
// detail.
//
// A Go client with UnaryErrorDecodingInterceptor installed can pass what that
// returns straight in: the decoded error still answers as the status it arrived
// as, so the detail is still reachable through it.
func ClientReasonFromStatus(err error) (*errdetails.ErrorInfo, bool) {
	if err == nil {
		return nil, false
	}

	st, ok := status.FromError(err)
	if !ok {
		return nil, false
	}

	for _, detail := range st.Details() {
		if info, isInfo := detail.(*errdetails.ErrorInfo); isInfo && info != nil {
			return info, true
		}
	}

	return nil, false
}

// StripEncodedErrorDetail returns err with the encoded-chain detail removed and
// every other detail left alone.
//
// This is what the third channel bought. The encoded chain is meant for a peer
// this process trusts, so a server reachable by untrusted clients has to strip
// it — and until there was a detail worth keeping, "strip it" and "strip the
// details" were the same sentence, so an edge either dropped everything or read
// the type URLs itself. This drops exactly the one detail that carries internal
// text, so the reason a client branches on goes through.
//
// An error that is not a status, or a status with nothing to strip, comes back
// unchanged, so an edge may call it on everything it forwards. Note that what
// comes back is a plain status error: the chain the detail carried is gone, by
// construction, and errors.Is against a sentinel no longer matches it. Strip at
// the edge, after anything in-process that wanted the sentinel has had it.
func StripEncodedErrorDetail(err error) error {
	if err == nil {
		return nil
	}

	st, ok := status.FromError(err)
	if !ok {
		return err
	}

	// st.Proto is a deep copy, so the details slice is ours to rebuild.
	proto := st.Proto()
	if proto == nil {
		return err
	}

	raw := proto.GetDetails()

	// Details unmarshals each entry in order and one-for-one, which is what
	// makes the two slices index-aligned. Going through it is not a detour: the
	// encoded chain arrives as an Any nested inside the Any the status holds —
	// WithDetails packs whatever it is given, and it was given an Any — so the
	// outer type URL says google.protobuf.Any and only the unmarshaled inner one
	// names cockroach.errorspb.EncodedError. DecodeErrorFromStatus recognizes it
	// the same way, and the two must agree about what "the encoded detail" is or
	// an edge would be stripping something other than what it thinks.
	unmarshaled := st.Details()
	if len(unmarshaled) != len(raw) {
		return err
	}

	kept := make([]*anypb.Any, 0, len(raw))
	for i, detail := range unmarshaled {
		if nested, isAny := detail.(*anypb.Any); isAny && nested != nil && nested.GetTypeUrl() == encodedErrorTypeURL {
			continue
		}
		kept = append(kept, raw[i])
	}

	if len(kept) == len(raw) {
		return err
	}

	proto.Details = kept

	return status.FromProto(proto).Err()
}
