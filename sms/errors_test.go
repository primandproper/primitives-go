package sms

import (
	"testing"

	"github.com/primandproper/primitives-go/v2/errors"

	"github.com/shoenig/test"
)

func TestSentinels(T *testing.T) {
	T.Parallel()

	// Three sentinels declared side by side are three chances to alias one to
	// another, and an aliased pair would be caught by nothing else: a consumer
	// recording an opt-out against a person whose number was merely typo'd is a
	// legal record made about somebody who never asked for it.
	T.Run("are distinguishable from one another", func(t *testing.T) {
		t.Parallel()

		sentinels := map[string]error{
			"opted out":  ErrRecipientOptedOut,
			"invalid":    ErrInvalidRecipient,
			"unverified": ErrUnverifiedRecipient,
		}

		for name, sentinel := range sentinels {
			for otherName, other := range sentinels {
				if name == otherName {
					continue
				}

				test.False(t, errors.Is(sentinel, other), test.Sprintf("%s matched %s", name, otherName))
			}
		}
	})

	// The adapters wrap with the provider's own code and prose, so a mapping or
	// a consumer branch that only worked on a bare sentinel would work nowhere
	// real.
	T.Run("survive wrapping", func(t *testing.T) {
		t.Parallel()

		for _, sentinel := range []error{ErrRecipientOptedOut, ErrInvalidRecipient, ErrUnverifiedRecipient} {
			test.ErrorIs(t, errors.Wrap(sentinel, "sending sms"), sentinel)
		}
	})

	// The messages reach clients through errors/http and errors/grpc, so none of
	// them names a phone number — and none can, since a sentinel is a constant.
	T.Run("say nothing about a recipient", func(t *testing.T) {
		t.Parallel()

		for _, sentinel := range []error{ErrRecipientOptedOut, ErrInvalidRecipient, ErrUnverifiedRecipient} {
			test.StrNotContains(t, sentinel.Error(), "+1")
		}
	})
}
