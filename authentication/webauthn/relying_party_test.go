package webauthn_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/primandproper/primitives-go/v2/authentication/webauthn"
	"github.com/primandproper/primitives-go/v2/authentication/webauthn/webauthntest"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"

	"github.com/go-webauthn/webauthn/protocol"
	gowebauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestNewRelyingParty(T *testing.T) {
	T.Parallel()

	T.Run("builds a relying party from a valid config", func(t *testing.T) {
		t.Parallel()

		rp, err := webauthn.NewRelyingParty(t.Context(), &webauthn.Config{
			RPID:          testRPID,
			RPDisplayName: "Example",
			RPOrigins:     []string{testOrigin},
		}, newMemoryStore())
		must.NoError(t, err)
		must.NotNil(t, rp)

		// The default is applied by the constructor rather than left for a
		// ceremony to discover, so a relying party built from an empty timeout
		// asks the browser for one.
		creation, err := rp.BeginRegistration(t.Context(), newTestUser("user-one"))
		must.NoError(t, err)
		test.EqOp(t, int(webauthn.DefaultCeremonyTimeout.Milliseconds()), creation.Response.Timeout)
	})

	// Absent means noop: a relying party given no observability at all still
	// runs a whole ceremony.
	T.Run("runs a ceremony with no options at all", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		user := newTestUser("user-one")
		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)

		rp, err := webauthn.NewRelyingParty(ctx, &webauthn.Config{
			RPID:          testRPID,
			RPDisplayName: "Example",
			RPOrigins:     []string{testOrigin},
		}, newMemoryStore())
		must.NoError(t, err)

		creation, err := rp.BeginRegistration(ctx, user)
		must.NoError(t, err)

		_, err = rp.FinishRegistration(ctx, user,
			post(t, authenticator.Register(t, creation.Response.Challenge.String())))
		must.NoError(t, err)
	})

	T.Run("refuses a nil config", func(t *testing.T) {
		t.Parallel()

		rp, err := webauthn.NewRelyingParty(t.Context(), nil, newMemoryStore())
		test.ErrorIs(t, err, platformerrors.ErrNilInputParameter)
		test.Nil(t, rp)
	})

	T.Run("refuses a nil store", func(t *testing.T) {
		t.Parallel()

		// There is no default. An implicit in-memory store would pass every
		// test here and fail a fraction of logins the moment there were two
		// replicas.
		rp, err := webauthn.NewRelyingParty(t.Context(), &webauthn.Config{
			RPID:          testRPID,
			RPDisplayName: "Example",
			RPOrigins:     []string{testOrigin},
		}, nil)
		test.ErrorIs(t, err, webauthn.ErrNilStore)
		test.Nil(t, rp)
	})

	T.Run("refuses a config no ceremony could run under", func(t *testing.T) {
		t.Parallel()

		for name, cfg := range map[string]*webauthn.Config{
			"no relying party id": {RPDisplayName: "Example", RPOrigins: []string{testOrigin}},
			"no display name":     {RPID: testRPID, RPOrigins: []string{testOrigin}},
			"no origins":          {RPID: testRPID, RPDisplayName: "Example"},
			"id that is not a domain": {
				RPID: "https://example.com", RPDisplayName: "Example", RPOrigins: []string{testOrigin},
			},
		} {
			rp, err := webauthn.NewRelyingParty(t.Context(), cfg, newMemoryStore())
			test.Error(t, err, test.Sprintf("config %q", name))
			test.Nil(t, rp, test.Sprintf("config %q", name))
		}
	})
}

func TestRelyingParty_registration(T *testing.T) {
	T.Parallel()

	T.Run("registers a passkey across two requests", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		store := newMemoryStore()
		rp := newTestRelyingParty(t, store)
		user := newTestUser("user-one")
		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)

		creation, err := rp.BeginRegistration(ctx, user)
		must.NoError(t, err)
		must.NotNil(t, creation)

		// The ceremony's state stayed here rather than going to the client,
		// which is the whole reason the two requests can land on different
		// replicas.
		test.EqOp(t, 1, store.count())
		test.EqOp(t, testRPID, creation.Response.RelyingParty.ID)

		credential, err := rp.FinishRegistration(ctx, user,
			post(t, authenticator.Register(t, creation.Response.Challenge.String())))
		must.NoError(t, err)
		must.NotNil(t, credential)
		test.Eq(t, authenticator.CredentialID(), credential.ID)

		// And the ceremony is over: nothing is left for a replay to find.
		test.EqOp(t, 0, store.count())
	})

	T.Run("finishes from a body for a caller that is not serving HTTP", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user := newTestUser("user-one")
		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)

		creation, err := rp.BeginRegistration(ctx, user)
		must.NoError(t, err)

		credential, err := rp.FinishRegistrationBody(ctx, user,
			bytes.NewReader(authenticator.Register(t, creation.Response.Challenge.String())))
		must.NoError(t, err)
		test.Eq(t, authenticator.CredentialID(), credential.ID)
	})

	// A challenge answered twice is the replay this package's store exists to
	// stop, and it is refused on the challenge rather than on the signature —
	// the response is byte-identical and perfectly valid the second time.
	T.Run("refuses an attestation replayed inside the ceremony window", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user := newTestUser("user-one")
		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)

		creation, err := rp.BeginRegistration(ctx, user)
		must.NoError(t, err)

		response := authenticator.Register(t, creation.Response.Challenge.String())

		_, err = rp.FinishRegistration(ctx, user, post(t, response))
		must.NoError(t, err)

		_, err = rp.FinishRegistration(ctx, user, post(t, response))
		test.ErrorIs(t, err, webauthn.ErrSessionNotFound)
	})

	T.Run("refuses an attestation for a challenge nobody issued", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user := newTestUser("user-one")
		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)

		_, err := rp.FinishRegistration(ctx, user,
			post(t, authenticator.Register(t, "a-challenge-this-server-never-minted")))
		test.ErrorIs(t, err, webauthn.ErrSessionNotFound)
	})

	T.Run("refuses an attestation from an origin that is not configured", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user := newTestUser("user-one")
		elsewhere := webauthntest.NewAuthenticator(t, testRPID, "https://phishing.example.net")

		creation, err := rp.BeginRegistration(ctx, user)
		must.NoError(t, err)

		_, err = rp.FinishRegistration(ctx, user,
			post(t, elsewhere.Register(t, creation.Response.Challenge.String())))
		test.Error(t, err)
	})

	T.Run("refuses a ceremony without a user", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		store := newMemoryStore()
		rp := newTestRelyingParty(t, store)

		_, err := rp.BeginRegistration(ctx, nil)
		test.ErrorIs(t, err, webauthn.ErrNilUser)
		test.EqOp(t, 0, store.count())

		// A well-formed response, so that the missing user is what refuses it:
		// the user is checked before the ceremony is consumed, so a caller who
		// passed nothing has not also burned a challenge.
		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)

		_, err = rp.FinishRegistration(ctx, nil, post(t, authenticator.Register(t, "some-challenge")))
		test.ErrorIs(t, err, webauthn.ErrNilUser)
	})

	T.Run("refuses a response it cannot parse", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user := newTestUser("user-one")

		_, err := rp.FinishRegistration(ctx, user, post(t, []byte("{not json")))
		test.Error(t, err)

		_, err = rp.FinishRegistration(ctx, user, nil)
		test.Error(t, err)

		_, err = rp.FinishRegistrationBody(ctx, user, nil)
		test.ErrorIs(t, err, webauthn.ErrNilResponse)
		test.ErrorIs(t, err, platformerrors.ErrNilInputParameter)
	})

	T.Run("reports a store that cannot hold the ceremony", func(t *testing.T) {
		t.Parallel()

		store := newMemoryStore()
		store.saveErr = errStoreDown

		rp := newTestRelyingParty(t, store)

		// Reported rather than swallowed: a ceremony whose state was not stored
		// cannot be finished, so handing the client its options anyway would
		// produce a registration that fails a round trip later for no visible
		// reason.
		_, err := rp.BeginRegistration(t.Context(), newTestUser("user-one"))
		test.ErrorIs(t, err, errStoreDown)
	})
}

func TestRelyingParty_login(T *testing.T) {
	T.Parallel()

	T.Run("logs a registered passkey in across two requests", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		store := newMemoryStore()
		rp := newTestRelyingParty(t, store)
		user, authenticator := registered(t, rp)

		assertion, err := rp.BeginLogin(ctx, user)
		must.NoError(t, err)
		must.NotNil(t, assertion)
		test.EqOp(t, 1, store.count())

		credential, err := rp.FinishLogin(ctx, user,
			post(t, authenticator.Assert(t, assertion.Response.Challenge.String(), user.handle)))
		must.NoError(t, err)
		must.NotNil(t, credential)

		// The sign count the application is expected to write back, and it has
		// moved: this is what makes a cloned authenticator detectable.
		test.EqOp(t, authenticator.SignCount(), credential.Authenticator.SignCount)
		test.EqOp(t, 0, store.count())
	})

	// A user whose passkey was stored before the test began: the credential the
	// authenticator hands over is the one a registration would have returned,
	// so nothing about the login can tell the difference.
	T.Run("logs in a passkey that was stored rather than registered", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)

		user := newTestUser("user-one")
		stored := authenticator.Credential(t)
		user.add(&stored)

		assertion, err := rp.BeginLogin(ctx, user)
		must.NoError(t, err)

		credential, err := rp.FinishLogin(ctx, user,
			post(t, authenticator.Assert(t, assertion.Response.Challenge.String(), user.handle)))
		must.NoError(t, err)
		test.Eq(t, authenticator.CredentialID(), credential.ID)
		test.False(t, credential.Authenticator.CloneWarning)
	})

	// The sign count is the only thing that tells two copies of one key apart,
	// and it only does so once the count the original last signed with has been
	// written back — which is the application's job, done here by hand.
	T.Run("flags an authenticator whose counter has gone stale", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user, authenticator := registered(t, rp)
		clone := authenticator.Clone()

		assertion, err := rp.BeginLogin(ctx, user)
		must.NoError(t, err)

		credential, err := rp.FinishLogin(ctx, user,
			post(t, authenticator.Assert(t, assertion.Response.Challenge.String(), user.handle)))
		must.NoError(t, err)
		test.False(t, credential.Authenticator.CloneWarning)

		user.credentials = []webauthn.Credential{*credential}

		assertion, err = rp.BeginLogin(ctx, user)
		must.NoError(t, err)

		// The clone's signature is perfectly valid — it is the same key — so
		// the login succeeds, and the warning is what the application has to
		// act on.
		cloned, err := rp.FinishLogin(ctx, user,
			post(t, clone.Assert(t, assertion.Response.Challenge.String(), user.handle)))
		must.NoError(t, err)
		test.True(t, cloned.Authenticator.CloneWarning)
		test.EqOp(t, authenticator.SignCount(), clone.SignCount())
	})

	T.Run("finishes from a body for a caller that is not serving HTTP", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user, authenticator := registered(t, rp)

		assertion, err := rp.BeginLogin(ctx, user)
		must.NoError(t, err)

		credential, err := rp.FinishLoginBody(ctx, user,
			bytes.NewReader(authenticator.Assert(t, assertion.Response.Challenge.String(), user.handle)))
		must.NoError(t, err)
		test.Eq(t, authenticator.CredentialID(), credential.ID)
	})

	T.Run("refuses an assertion replayed inside the ceremony window", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user, authenticator := registered(t, rp)

		assertion, err := rp.BeginLogin(ctx, user)
		must.NoError(t, err)

		response := authenticator.Assert(t, assertion.Response.Challenge.String(), user.handle)

		_, err = rp.FinishLogin(ctx, user, post(t, response))
		must.NoError(t, err)

		// The signature is still valid and the sign count is unchanged, so
		// nothing but the consumed challenge refuses this.
		_, err = rp.FinishLogin(ctx, user, post(t, response))
		test.ErrorIs(t, err, webauthn.ErrSessionNotFound)
	})

	T.Run("refuses an assertion from a passkey the user does not own", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user, _ := registered(t, rp)
		stranger := webauthntest.NewAuthenticator(t, testRPID, testOrigin)

		assertion, err := rp.BeginLogin(ctx, user)
		must.NoError(t, err)

		_, err = rp.FinishLogin(ctx, user,
			post(t, stranger.Assert(t, assertion.Response.Challenge.String(), user.handle)))
		test.Error(t, err)
	})

	T.Run("refuses a ceremony without a user", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())

		_, err := rp.BeginLogin(ctx, nil)
		test.ErrorIs(t, err, webauthn.ErrNilUser)

		authenticator := webauthntest.NewAuthenticator(t, testRPID, testOrigin)

		_, err = rp.FinishLoginBody(ctx, nil,
			bytes.NewReader(authenticator.Assert(t, "some-challenge", []byte("user-one"))))
		test.ErrorIs(t, err, webauthn.ErrNilUser)
	})

	T.Run("refuses a response it cannot parse", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user := newTestUser("user-one")

		_, err := rp.FinishLogin(ctx, user, post(t, []byte("{not json")))
		test.Error(t, err)

		_, err = rp.FinishLoginBody(ctx, user, nil)
		test.ErrorIs(t, err, webauthn.ErrNilResponse)
	})

	T.Run("reports a store that cannot hold the ceremony", func(t *testing.T) {
		t.Parallel()

		store := newMemoryStore()
		rp := newTestRelyingParty(t, store)
		user, _ := registered(t, rp)

		store.saveErr = errStoreDown

		_, err := rp.BeginLogin(t.Context(), user)
		test.ErrorIs(t, err, errStoreDown)
	})
}

func TestRelyingParty_discoverableLogin(T *testing.T) {
	T.Parallel()

	T.Run("logs a passkey in without being told who it belongs to", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		store := newMemoryStore()
		rp := newTestRelyingParty(t, store)
		user, authenticator := registered(t, rp)

		assertion, err := rp.BeginDiscoverableLogin(ctx)
		must.NoError(t, err)
		must.NotNil(t, assertion)
		test.EqOp(t, 1, store.count())

		found, credential, err := rp.FinishDiscoverableLogin(ctx, handlerFor(user),
			post(t, authenticator.Assert(t, assertion.Response.Challenge.String(), user.handle)))
		must.NoError(t, err)
		must.NotNil(t, credential)
		must.NotNil(t, found)

		// The user came back from the handler rather than from the request,
		// which is the point of a usernameless login.
		test.Eq(t, user.handle, found.WebAuthnID())
		test.EqOp(t, 0, store.count())
	})

	T.Run("finishes from a body for a caller that is not serving HTTP", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user, authenticator := registered(t, rp)

		assertion, err := rp.BeginDiscoverableLogin(ctx)
		must.NoError(t, err)

		found, _, err := rp.FinishDiscoverableLoginBody(ctx, handlerFor(user),
			bytes.NewReader(authenticator.Assert(t, assertion.Response.Challenge.String(), user.handle)))
		must.NoError(t, err)
		test.Eq(t, user.handle, found.WebAuthnID())
	})

	// The ceremony a discoverable assertion may be answered against is one that
	// was begun without a user. Answering a named ceremony with a discoverable
	// assertion would let a passkey log in as whoever the ceremony named.
	T.Run("refuses an assertion against a ceremony begun for somebody in particular", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user, authenticator := registered(t, rp)

		assertion, err := rp.BeginLogin(ctx, user)
		must.NoError(t, err)

		_, _, err = rp.FinishDiscoverableLogin(ctx, handlerFor(user),
			post(t, authenticator.Assert(t, assertion.Response.Challenge.String(), user.handle)))
		test.Error(t, err)
	})

	T.Run("refuses an assertion replayed inside the ceremony window", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user, authenticator := registered(t, rp)

		assertion, err := rp.BeginDiscoverableLogin(ctx)
		must.NoError(t, err)

		response := authenticator.Assert(t, assertion.Response.Challenge.String(), user.handle)

		_, _, err = rp.FinishDiscoverableLogin(ctx, handlerFor(user), post(t, response))
		must.NoError(t, err)

		_, _, err = rp.FinishDiscoverableLogin(ctx, handlerFor(user), post(t, response))
		test.ErrorIs(t, err, webauthn.ErrSessionNotFound)
	})

	T.Run("refuses a ceremony without a handler", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())
		user, authenticator := registered(t, rp)

		assertion, err := rp.BeginDiscoverableLogin(ctx)
		must.NoError(t, err)

		// Refused before the ceremony is consumed, so a caller that forgot the
		// handler has not also burned the challenge.
		_, _, err = rp.FinishDiscoverableLogin(ctx, nil,
			post(t, authenticator.Assert(t, assertion.Response.Challenge.String(), user.handle)))
		test.ErrorIs(t, err, webauthn.ErrNilHandler)

		_, _, err = rp.FinishDiscoverableLoginBody(ctx, nil, nil)
		test.ErrorIs(t, err, webauthn.ErrNilResponse)
	})

	T.Run("refuses a response it cannot parse", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		rp := newTestRelyingParty(t, newMemoryStore())

		_, _, err := rp.FinishDiscoverableLogin(ctx, handlerFor(newTestUser("user-one")), post(t, []byte("{not json")))
		test.Error(t, err)
	})

	T.Run("reports a store that cannot hold the ceremony", func(t *testing.T) {
		t.Parallel()

		store := newMemoryStore()
		store.saveErr = errStoreDown

		rp := newTestRelyingParty(t, store)

		_, err := rp.BeginDiscoverableLogin(t.Context())
		test.ErrorIs(t, err, errStoreDown)
	})
}

func TestRelyingParty_deadline(T *testing.T) {
	T.Parallel()

	T.Run("bounds a ceremony by the configured timeout", func(t *testing.T) {
		t.Parallel()

		store := newMemoryStore()

		rp, err := webauthn.NewRelyingParty(t.Context(), &webauthn.Config{
			RPID:            testRPID,
			RPDisplayName:   "Example",
			RPOrigins:       []string{testOrigin},
			CeremonyTimeout: 5 * time.Second,
		}, store)
		must.NoError(t, err)

		creation, err := rp.BeginRegistration(t.Context(), newTestUser("user-one"))
		must.NoError(t, err)

		// The browser is asked for the same bound the server will enforce.
		test.EqOp(t, int((5 * time.Second).Milliseconds()), creation.Response.Timeout)
	})

	// Enforcement is not configurable, and this is why: with it off the library
	// stamps no deadline and checks none, so a challenge would be answerable for
	// as long as its row survived.
	T.Run("stamps a deadline the library will check", func(t *testing.T) {
		t.Parallel()

		store := newMemoryStore()
		rp := newTestRelyingParty(t, store)

		_, err := rp.BeginRegistration(t.Context(), newTestUser("user-one"))
		must.NoError(t, err)

		store.mu.Lock()
		defer store.mu.Unlock()

		for _, session := range store.sessions {
			test.False(t, session.Expires.IsZero())
		}
	})
}

func TestRelyingParty_perCeremonyOptions(T *testing.T) {
	T.Parallel()

	// The library's own options, passed through rather than mirrored: a
	// deployment that wants usernameless login registers resident credentials,
	// and that is a per-ceremony decision rather than a configured one.
	T.Run("passes registration options through", func(t *testing.T) {
		t.Parallel()

		rp := newTestRelyingParty(t, newMemoryStore())

		creation, err := rp.BeginRegistration(t.Context(), newTestUser("user-one"),
			gowebauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired))
		must.NoError(t, err)

		test.EqOp(t, protocol.ResidentKeyRequirementRequired, creation.Response.AuthenticatorSelection.ResidentKey)
	})

	T.Run("passes login options through", func(t *testing.T) {
		t.Parallel()

		rp := newTestRelyingParty(t, newMemoryStore())

		assertion, err := rp.BeginDiscoverableLogin(t.Context(),
			gowebauthn.WithUserVerification(protocol.VerificationRequired))
		must.NoError(t, err)

		test.EqOp(t, protocol.VerificationRequired, assertion.Response.UserVerification)
	})
}

// registered runs a whole registration ceremony and hands back the user who now
// owns the passkey, for the login cases that need one.
func registered(tb testing.TB, rp *webauthn.RelyingParty) (*testUser, *webauthntest.Authenticator) {
	tb.Helper()

	user := newTestUser("user-one")
	authenticator := webauthntest.NewAuthenticator(tb, testRPID, testOrigin)

	creation, err := rp.BeginRegistration(tb.Context(), user)
	must.NoError(tb, err)

	credential, err := rp.FinishRegistration(tb.Context(), user,
		post(tb, authenticator.Register(tb, creation.Response.Challenge.String())))
	must.NoError(tb, err)

	user.add(credential)

	return user, authenticator
}

// handlerFor is the application's credential lookup, which for one user is one
// line.
func handlerFor(user *testUser) webauthn.DiscoverableUserHandler {
	return func(_, userHandle []byte) (webauthn.User, error) {
		if !bytes.Equal(userHandle, user.handle) {
			return nil, webauthn.ErrSessionNotFound
		}

		return user, nil
	}
}

// errStoreDown is the failure the store double injects.
var errStoreDown = platformerrors.New("session store is having a day")
