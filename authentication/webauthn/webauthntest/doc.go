// Package webauthntest holds the behavior every webauthn.SessionStore owes its
// callers, written once and run against each implementation, and the virtual
// Authenticator a relying party's ceremonies are tested against.
//
// The store is the piece of a WebAuthn deployment most likely to be written
// again — a consumer with neither a SQL database nor a cache.Cache has to write
// one — and it is the piece whose failures are the least visible. A store that
// hands the same challenge to two callers, or that keeps handing one out after
// its deadline, produces a login that works, which is what makes the gap
// something a suite has to catch rather than a user.
//
// Expiry is the reason it exists at all. The two implementations here express
// it in completely different terms — an expires_at column compared against a
// clock, and a cache entry the provider drops on its own — and the only thing
// that keeps those two answering a caller the same way is a suite that asks
// both.
//
// # Using it
//
//	func TestSessionStore_Conformance(t *testing.T) {
//		t.Parallel()
//
//		webauthntest.Run(t, func(tb testing.TB) webauthn.SessionStore {
//			store, err := NewSessionStore(&Config{}, newTestClient(tb))
//			must.NoError(tb, err)
//
//			return store
//		})
//	}
//
// Each implementation keeps its own test file for what is genuinely its own —
// the sweeper and the dialect rendering for the database store, the cache
// provider's failures for the cache store.
//
// # Declaring a deviation
//
// The Options are how an implementation says where it stops honoring the full
// contract, and every one of them removes cases. They are deliberately shaped
// so that silence means the whole contract: a store that needs one and does not
// declare it fails, rather than skipping something nobody notices. A declared
// deviation still runs as a skipped subtest naming the reason, so `go test -v`
// shows what was not proven instead of hiding it.
//
// # Real clocks, generous windows
//
// The suite uses the wall clock. No implementation's notion of now can be
// replaced from out here — the database store's is a clock it was constructed
// with, the cache store's belongs to the cache provider — so the expiry case
// saves with a short TTL and then waits several times that before asserting the
// state has lapsed. The window is picked so that a loaded CI host cannot land
// inside it, not so that the suite is fast.
//
// # The virtual authenticator
//
// A ceremony cannot be tested without something to answer it, and the thing
// that answers it is a device speaking the WebAuthn protocol: a key that signs
// the challenge, over bytes the specification lays out. [Authenticator] is one,
// real ES256 over P-256 with "none" attestation, producing the JSON a browser
// would POST to finish each ceremony.
//
//	authenticator := webauthntest.NewAuthenticator(t, "example.com", "https://example.com")
//
//	creation, err := rp.BeginRegistration(ctx, user)
//	// ...
//	credential, err := rp.FinishRegistrationBody(ctx, user,
//		bytes.NewReader(authenticator.Register(t, creation.Response.Challenge.String())))
//	// ...
//	assertion, err := rp.BeginLogin(ctx, user)
//	// ...
//	credential, err = rp.FinishLoginBody(ctx, user,
//		bytes.NewReader(authenticator.Assert(t, assertion.Response.Challenge.String(), user.WebAuthnID())))
//
// [Authenticator.Clone] is the sign-count question: a copy of the key whose
// counter falls behind the original's as soon as the original logs in, which is
// what a relying party's clone detection exists to notice.
package webauthntest
