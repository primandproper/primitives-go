package webauthntest_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/primandproper/primitives-go/v2/authentication/webauthn/webauthntest"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

const (
	testRPID   = "example.com"
	testOrigin = "https://example.com"
)

func TestAuthenticator_Assert(T *testing.T) {
	T.Parallel()

	T.Run("advances the counter with every assertion", func(t *testing.T) {
		t.Parallel()

		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)
		start := authenticator.SignCount()

		first := signedCounter(t, authenticator.Assert(t, "challenge-one", nil))
		second := signedCounter(t, authenticator.Assert(t, "challenge-two", nil))

		test.EqOp(t, start+1, first)
		test.EqOp(t, start+2, second)
		test.EqOp(t, second, authenticator.SignCount())
	})

	T.Run("signs for the relying party it was minted for", func(t *testing.T) {
		t.Parallel()

		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)
		authData := authenticatorData(t, authenticator.Assert(t, "a-challenge", nil))

		rpIDHash := sha256.Sum256([]byte(testRPID))
		test.Eq(t, rpIDHash[:], authData[:sha256.Size])
	})
}

func TestAuthenticator_Clone(T *testing.T) {
	T.Parallel()

	// The case a relying party's clone detection exists for: the original has
	// logged in since the key was copied, so the copy's next counter is one the
	// relying party has already seen.
	T.Run("produces a device whose next assertion carries a stale counter", func(t *testing.T) {
		t.Parallel()

		original := webauthntest.NewAuthenticator(t, testRPID, testOrigin)
		clone := original.Clone()

		stored := signedCounter(t, original.Assert(t, "challenge-one", nil))
		stale := signedCounter(t, clone.Assert(t, "challenge-two", nil))

		test.LessEq(t, stored, stale)
	})

	T.Run("is the same passkey", func(t *testing.T) {
		t.Parallel()

		original := webauthntest.NewAuthenticator(t, testRPID, testOrigin)
		clone := original.Clone()

		test.Eq(t, original.CredentialID(), clone.CredentialID())
		test.Eq(t, original.Credential(t).PublicKey, clone.Credential(t).PublicKey)
		test.EqOp(t, original.SignCount(), clone.SignCount())
	})

	T.Run("counts independently of the original", func(t *testing.T) {
		t.Parallel()

		original := webauthntest.NewAuthenticator(t, testRPID, testOrigin)
		clone := original.Clone()
		start := original.SignCount()

		original.Assert(t, "challenge-one", nil)
		original.Assert(t, "challenge-two", nil)

		test.EqOp(t, start+2, original.SignCount())
		test.EqOp(t, start, clone.SignCount())
	})
}

func TestAuthenticator_Credential(T *testing.T) {
	T.Parallel()

	T.Run("describes the passkey the device registers", func(t *testing.T) {
		t.Parallel()

		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)
		credential := authenticator.Credential(t)

		test.Eq(t, authenticator.CredentialID(), credential.ID)
		test.EqOp(t, authenticator.SignCount(), credential.Authenticator.SignCount)

		// Backup eligibility is never claimed, in the credential or in either
		// ceremony, because a credential whose answer changes between
		// ceremonies is refused by the library.
		test.False(t, credential.Flags.BackupEligible)
		test.False(t, credential.Flags.BackupState)

		// The public key is the one the registration announces, which is what
		// lets a user seeded from this credential log in.
		test.True(t, bytes.Contains(attestationObject(t, authenticator.Register(t, "a-challenge")), credential.PublicKey))
	})

	T.Run("hands out copies", func(t *testing.T) {
		t.Parallel()

		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)
		want := authenticator.CredentialID()

		authenticator.CredentialID()[0] ^= 0xff
		credential := authenticator.Credential(t)
		credential.ID[0] ^= 0xff

		test.Eq(t, want, authenticator.CredentialID())
	})
}

// response decodes the field of a ceremony response a test wants.
func response(tb testing.TB, body []byte, field string) []byte {
	tb.Helper()

	var envelope struct {
		Response map[string]string `json:"response"`
	}
	must.NoError(tb, json.Unmarshal(body, &envelope))

	encoded, ok := envelope.Response[field]
	must.True(tb, ok, must.Sprintf("response has no %q", field))

	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	must.NoError(tb, err)

	return decoded
}

// authenticatorData is the authenticator data an assertion signed.
func authenticatorData(tb testing.TB, body []byte) []byte {
	tb.Helper()

	return response(tb, body, "authenticatorData")
}

// attestationObject is the CBOR attestation a registration carries.
func attestationObject(tb testing.TB, body []byte) []byte {
	tb.Helper()

	return response(tb, body, "attestationObject")
}

// signedCounter reads the sign count out of an assertion's authenticator data,
// where it follows the relying party's hash and the flags byte.
func signedCounter(tb testing.TB, body []byte) uint32 {
	tb.Helper()

	authData := authenticatorData(tb, body)
	must.SliceLen(tb, sha256.Size+1+4, authData)

	return binary.BigEndian.Uint32(authData[sha256.Size+1:])
}
