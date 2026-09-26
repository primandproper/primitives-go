package database

import (
	"context"
	"database/sql"
	"time"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
)

// RunInTransaction begins a transaction on writeDB, invokes fn with that transaction as
// the sole query executor, and commits when fn returns nil. It is the shared engine
// behind each Client's WithTransaction method — application code should prefer
// Client.WithTransaction, which wraps this with the implementation's observability.
//
// fn receives the transaction as a Tx, not the transaction handle: it cannot commit or
// roll back, and its statements cannot accidentally target the read replica or another
// connection. Tx is producible only here, so a parameter typed Tx anywhere in this module
// is a compile-time claim that the caller is inside one of these. Lifecycle is managed
// entirely here:
//
//   - rollback is invoked (with the transaction) on any non-nil error from fn, and the
//     error is returned unwrapped.
//   - a panic inside fn triggers rollback and is then re-raised, so no connection leaks
//     and the caller still observes the failure.
//   - a nil return from fn commits; commit errors are wrapped and returned.
//
// A failed commit has already released the connection back to the pool, so no second
// rollback is attempted (it would only surface a spurious ErrTxDone).
//
// The Tx is spent the moment fn returns — before the commit or rollback below — so a Tx
// that escaped into a struct field or a goroutine reports ErrTransactionClosed rather
// than racing the outcome of a transaction it can no longer affect.
func RunInTransaction(
	ctx context.Context,
	writeDB *sql.DB,
	rollback func(ctx context.Context, tx SQLQueryExecutorAndTransactionManager),
	fn func(tx Tx) error,
) error {
	if writeDB == nil || rollback == nil || fn == nil {
		return platformerrors.ErrNilInputParameter
	}

	tx, err := writeDB.BeginTx(ctx, nil)
	if err != nil {
		return platformerrors.Wrap(err, "beginning transaction")
	}

	// Roll back on panic and re-raise so the caller still sees the failure and the
	// pooled connection is not leaked.
	defer func() {
		if r := recover(); r != nil {
			rollback(ctx, tx)
			panic(r)
		}
	}()

	txExec := newTxExecutor(tx)

	fnErr := func() error {
		defer txExec.spend()

		return fn(txExec)
	}()
	if fnErr != nil {
		rollback(ctx, tx)

		return fnErr
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return platformerrors.Wrap(commitErr, "committing transaction")
	}

	return nil
}

type (
	// TxOption configures a single WithTransaction call.
	TxOption func(*TxConfig)

	// TxConfig is what a WithTransaction call's options resolve to. It is exported
	// so a Client implementation outside this module, a test double included, can
	// implement TxOptionsAccess and read the options it was handed.
	TxConfig struct {
		_ struct{} `json:"-"`

		// ConflictAttempts bounds how many times fn runs in total, the first
		// included, while the client reports a retryable conflict. A value below 2
		// runs fn once.
		ConflictAttempts uint

		// ConflictBackoffInitial is the ceiling on the pause before the second
		// attempt. Each later ceiling doubles, up to ConflictBackoffMax.
		ConflictBackoffInitial time.Duration

		// ConflictBackoffMax caps the ceiling on every pause, however many
		// attempts were asked for.
		ConflictBackoffMax time.Duration
	}
)

const (
	// DefaultConflictBackoffInitial is ConflictBackoffInitial when no
	// ConflictBackoff option sets it. A deadlock's losers need only to fall out
	// of step with each other, which takes milliseconds.
	DefaultConflictBackoffInitial = 5 * time.Millisecond

	// DefaultConflictBackoffMax is ConflictBackoffMax when no ConflictBackoff
	// option sets it. A transaction still conflicting after pauses this long is
	// under contention a longer wait will not clear, and its caller is better
	// served by retry.ErrExhausted soon than by a request that hangs.
	DefaultConflictBackoffMax = 250 * time.Millisecond
)

// NewTxConfig resolves opts, in order, into the configuration for one
// transaction, with defaults in place of the backoff an option left unset: the
// config a Client reads is the one that runs.
func NewTxConfig(opts ...TxOption) TxConfig {
	var cfg TxConfig

	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if cfg.ConflictBackoffInitial <= 0 {
		cfg.ConflictBackoffInitial = DefaultConflictBackoffInitial
	}

	if cfg.ConflictBackoffMax <= 0 {
		cfg.ConflictBackoffMax = DefaultConflictBackoffMax
	}

	// A cap below the first pause flattens the schedule at the cap: the cap is
	// the promise, and the pause is what gives.
	cfg.ConflictBackoffInitial = min(cfg.ConflictBackoffInitial, cfg.ConflictBackoffMax)

	return cfg
}

// RetryOnConflict re-runs the whole transaction, up to attempts times in total,
// when the database aborts it with a conflict its engine resolves by asking the
// caller to start over: a deadlock or a serialization failure. Which errors
// qualify is the client's decision, not the caller's; postgres and mysql each
// recognize their own, and sqlite, a single writer, recognizes none. Every other
// error returns on the first attempt, as does a canceled context. When every
// attempt conflicts, the error wraps retry.ErrExhausted around the last conflict.
//
//	err := database.WithTransaction(ctx, client, fn, database.RetryOnConflict(3))
//
// # The contract on fn
//
// Opting in is a claim that fn is safe to run more than once. Each run starts
// from a rolled-back transaction, so anything fn does through tx is undone before
// the next one. Nothing else is:
//
//   - fn must act only through tx. A mail sent, a message published, or a
//     statement run on Client.Writer() from inside fn happens once per attempt.
//     Side effects belong in the outbox, whose writes take the same Tx and roll
//     back with it.
//   - fn must not accumulate into state it captured. Assigning a result to a
//     captured variable is fine, since the final attempt overwrites it; appending
//     to a captured slice or incrementing a captured counter is not.
//
// A caller who cannot make that claim leaves the option off and keeps a single
// attempt, which is the default.
func RetryOnConflict(attempts uint) TxOption {
	return func(cfg *TxConfig) {
		cfg.ConflictAttempts = attempts
	}
}

// ConflictBackoff sets the pause between the attempts RetryOnConflict allows.
// The ceiling on the first pause is initial, each one after it doubles, and
// none exceeds maxDelay; the pause itself is drawn at random below its ceiling,
// so transactions that lost the same deadlock do not start over in lockstep.
//
// A zero or negative argument keeps that half's default,
// DefaultConflictBackoffInitial or DefaultConflictBackoffMax, and a maxDelay
// below initial pauses at most maxDelay every time. Without RetryOnConflict it
// has nothing to pace.
func ConflictBackoff(initial, maxDelay time.Duration) TxOption {
	return func(cfg *TxConfig) {
		cfg.ConflictBackoffInitial = initial
		cfg.ConflictBackoffMax = maxDelay
	}
}

// WithTransaction runs fn in a transaction on client, as Client.WithTransaction
// does, configured by opts. A client with TxOptionsAccess, which every client in
// this module has, receives the options. One without it, a test double that
// predates them for instance, runs fn once through Client.WithTransaction and
// never sees them: every option is a refinement of that single run, never a
// condition of it being correct, so the fallback is the behavior a caller had
// before asking for more.
func WithTransaction(ctx context.Context, client Client, fn func(querier Tx) error, opts ...TxOption) error {
	if withOpts, ok := client.(TxOptionsAccess); ok {
		return withOpts.WithTransactionOptions(ctx, fn, opts...)
	}

	return client.WithTransaction(ctx, fn)
}
