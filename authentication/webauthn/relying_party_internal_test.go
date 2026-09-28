package webauthn

import (
	"context"
	"testing"
	"time"

	"github.com/primandproper/primitives-go/v2/clock"

	"github.com/shoenig/test"
)

// The ceremonies are tested from outside the package, in relying_party_test.go,
// because the virtual authenticator they are run against lives in webauthntest
// and webauthntest imports this package. What is left here is the arithmetic no
// ceremony can stand on precisely enough to observe.

func TestRelyingParty_ttl(T *testing.T) {
	T.Parallel()

	// One number in three places: the deadline the library stamped is what the
	// ceremony's state is stored under, so a per-ceremony option that shortens
	// the ceremony shortens its state's life too.
	T.Run("stores a ceremony for as long as it has left to run", func(t *testing.T) {
		t.Parallel()

		rp := newTTLRelyingParty(clock.NewClock())

		ttl := rp.ttl(&SessionData{Expires: time.Now().Add(30 * time.Second)})
		test.True(t, ttl > 25*time.Second && ttl <= 30*time.Second, test.Sprintf("ttl %v", ttl))
	})

	// The instant the deadline arrives, exactly. A wall clock cannot be stood on
	// that instant, so nothing else here can tell "has a moment left" from "has
	// nothing left" — and the difference is a ceremony stored for zero, which
	// every store refuses, against one stored for the configured timeout.
	T.Run("hands a ceremony whose deadline has just arrived the configured timeout", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
		rp := newTTLRelyingParty(&fixedClock{now: now})

		test.EqOp(t, DefaultCeremonyTimeout, rp.ttl(&SessionData{Expires: now}))
		test.EqOp(t, time.Nanosecond, rp.ttl(&SessionData{Expires: now.Add(time.Nanosecond)}))
	})

	// A session with no deadline is what a caller who built this package's
	// store into their own go-webauthn configuration, with enforcement off,
	// produces. It gets the configured ceremony timeout rather than a TTL of
	// zero, which every store refuses.
	T.Run("falls back to the configured timeout for a ceremony with no deadline", func(t *testing.T) {
		t.Parallel()

		rp := newTTLRelyingParty(clock.NewClock())

		test.EqOp(t, DefaultCeremonyTimeout, rp.ttl(&SessionData{}))
		test.EqOp(t, DefaultCeremonyTimeout, rp.ttl(&SessionData{Expires: time.Now().Add(-time.Minute)}))
	})
}

// newTTLRelyingParty builds just enough of a relying party to ask it for a TTL:
// the clock it reads and the timeout it falls back to, which is what
// NewRelyingParty applies when the config names none.
func newTTLRelyingParty(c clock.Clock) *RelyingParty {
	return &RelyingParty{clock: c, ceremonyTimeout: DefaultCeremonyTimeout}
}

// fixedClock is a Clock stopped at one instant, for the tests that need to
// stand exactly on a ceremony's deadline rather than near it.
type fixedClock struct {
	now time.Time
}

var _ clock.Clock = (*fixedClock)(nil)

func (c *fixedClock) Now() time.Time                                   { return c.now }
func (c *fixedClock) Since(t time.Time) time.Duration                  { return c.now.Sub(t) }
func (c *fixedClock) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }
func (c *fixedClock) NewTicker(_ time.Duration) clock.Ticker           { panic("not used") }
