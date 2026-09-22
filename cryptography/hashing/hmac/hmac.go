package hmac

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // G505: SHA-1 is here only because a provider's published scheme specifies it; see NewHMACSHA1Hasher
	"crypto/sha256"
	"crypto/sha512"
	"hash"

	"github.com/primandproper/primitives-go/v2/cryptography/hashing"
)

var _ hashing.Hasher = (*Hasher)(nil)

// Hasher is the HMAC hashing.Hasher implementation: it authenticates rather
// than merely digests, its output depending on a key fixed at construction as
// well as on the content. It is exported, and returned by both constructors, so
// a caller who has chosen a keyed MAC can depend on that choice rather than on
// the interface every digest algorithm shares.
type Hasher struct {
	newHash func() hash.Hash
	key     []byte
}

// NewHMACSHA256Hasher returns a hashing.Hasher computing HMAC-SHA-256 under key.
// The key is copied, so a caller may reuse or zero its buffer afterwards.
//
// An empty key is accepted because crypto/hmac accepts one, and rejecting it
// here would put an error return on a constructor that has nothing else to fail
// on. It is not a meaningful MAC — callers deriving keys from configuration
// should check for emptiness themselves.
func NewHMACSHA256Hasher(key []byte) *Hasher {
	return newHasher(key, sha256.New)
}

// NewHMACSHA512Hasher returns a hashing.Hasher computing HMAC-SHA-512 under key,
// on the same terms as NewHMACSHA256Hasher.
func NewHMACSHA512Hasher(key []byte) *Hasher {
	return newHasher(key, sha512.New)
}

// NewHMACSHA1Hasher returns a hashing.Hasher computing HMAC-SHA-1 under key, on
// the same terms as NewHMACSHA256Hasher.
//
// It exists to verify schemes that already specify SHA-1 and cannot be told
// otherwise — Twilio's X-Twilio-Signature is the one this module reads — not to
// be chosen for anything new. HMAC-SHA-1 is not broken by the collision attacks
// that retired bare SHA-1, because an HMAC does not rest on collision
// resistance; a provider that lets you pick, however, has given you a better
// option and you should take it.
//
// Nothing that signs on this module's behalf reaches it: requestsigning mints
// SHA-256, and webhooks/inbound's configurable HMACScheme deliberately offers no
// Digest that selects it, so a verifier cannot be downgraded to SHA-1 by
// configuration. Reaching it is a deliberate call to this constructor.
func NewHMACSHA1Hasher(key []byte) *Hasher {
	return newHasher(key, sha1.New)
}

func newHasher(key []byte, newHash func() hash.Hash) *Hasher {
	return &Hasher{
		newHash: newHash,
		key:     append([]byte(nil), key...),
	}
}

// Hash returns the MAC of content under the hasher's key.
//
// A fresh hash.Hash is allocated per call rather than reset and reused, which
// is what makes the hasher safe to share across goroutines — a delivery worker
// signs concurrently from one endpoint's hasher, and shared internal state
// would interleave two payloads into one signature.
func (h *Hasher) Hash(content []byte) []byte {
	mac := hmac.New(h.newHash, h.key)

	// hash.Hash documents Write as never returning an error, which is what lets
	// hashing.Hasher have no error return at all.
	mac.Write(content)

	return mac.Sum(nil)
}

// Equal reports whether two MACs are identical, in time independent of how far
// they agree.
//
// It exists because the natural comparison is the wrong one. A digest is
// routinely rendered with hashing.Hex and compared as a string, and == on
// strings returns at the first differing byte — which, repeated against an
// attacker-controlled candidate, leaks the expected MAC a byte at a time. Verify
// paths must compare raw Hash output through this, never hex through ==.
func Equal(a, b []byte) bool {
	return hmac.Equal(a, b)
}

// MatchesAny reports whether any hasher's MAC over content equals any of the
// candidates, in time independent of which one matched.
//
// It is the shape every multi-key verifier in this module needs, because every
// one of them holds more than one key: a receiver carries an outgoing secret
// through a rotation window, and a provider may present several signatures in
// one header for the same reason. Both loops therefore run to completion —
// returning as soon as something matches makes the time taken depend on which
// key and which candidate agreed, which is exactly the distinction Equal's
// constant-time comparison exists to hide, given back one key at a time.
//
// The cost of not short-circuiting is nothing: the lists are a handful of keys
// held during a rotation, not an unbounded set.
//
// No hashers or no candidates is not a match. A verifier holding no keys
// accepts nothing, which is the only safe reading of "we cannot check this".
func MatchesAny(hashers []hashing.Hasher, content []byte, candidates ...[]byte) bool {
	var matched bool

	for _, h := range hashers {
		expected := h.Hash(content)

		for _, candidate := range candidates {
			if Equal(expected, candidate) {
				matched = true
			}
		}
	}

	return matched
}
