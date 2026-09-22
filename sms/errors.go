package sms

import (
	"github.com/primandproper/primitives-go/v2/errors"
)

// The refusals a provider makes about the message rather than about itself.
//
// They live here, beside the interface, rather than in the adapter that raises
// them, for the reason search's client-facing sentinels do: errors/http and
// errors/grpc map them, and a mapper that imported an adapter would put that
// vendor's transport into the import graph of the package every handler already
// depends on. A consumer branching on one of these therefore costs the interface
// package and nothing more, and keeps branching correctly after a provider swap.
//
// Everything else a provider can say — a malformed body, a revoked credential,
// an outage — comes back as the adapter's wrapped error. These three are the
// ones a consumer has something specific to do about.
var (
	// ErrRecipientOptedOut indicates the carrier refuses further messages to this
	// number because the person behind it replied STOP. Twilio reports it as
	// error 21610.
	//
	// It is the load-bearing one, and the reason this module ships no "contacts"
	// primitive to go with the sender. The opt-out is the carrier's record, not
	// ours: it was made by a person texting a keyword to a shortcode, it binds
	// every sender on that number, and it is a legal fact — under the TCPA in the
	// United States and its equivalents elsewhere — that the consumer is obliged
	// to record against the person and honor everywhere else it contacts them.
	// A generic send failure would be retried; this one must be written down.
	//
	// It is not recoverable by the sender. Consent is restored by the person
	// texting START, and by nothing a service can do on their behalf.
	ErrRecipientOptedOut = errors.New("recipient has opted out of messages from this sender")

	// ErrInvalidRecipient indicates the provider could not parse or route the
	// destination number — it is not a valid E.164 number, or not one any carrier
	// serves. Twilio reports it as error 21211.
	//
	// It is bad input, and both transports map it as such: the remedy is a
	// corrected number, and no amount of retrying the one that was sent will
	// produce one.
	ErrInvalidRecipient = errors.New("recipient phone number is invalid")

	// ErrUnverifiedRecipient indicates the account may only message numbers
	// verified in the provider's console, and this one is not. Twilio reports it
	// as error 21608, and it means the account is still a trial.
	//
	// It is the deployment's problem rather than the caller's, which is why it is
	// distinct from ErrInvalidRecipient: the number is fine, the account is not
	// upgraded. A service that answered it as bad input would send an operator
	// looking at the recipient's phone number instead of at the billing page.
	ErrUnverifiedRecipient = errors.New("recipient is not verified for this trial account")
)
