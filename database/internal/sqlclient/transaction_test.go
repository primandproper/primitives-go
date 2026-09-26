package sqlclient

import (
	"context"
	"database/sql"
	stderrors "errors"
	"testing"
	"time"

	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/retry"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// stubConfig is the slice of database.ClientConfig the readiness probe reads.
// The connection strings are the fields IsReady must not touch, and are spelled
// as credentials so a leak would be visible.
type stubConfig struct {
	maxPingAttempts uint64
	pingWaitPeriod  time.Duration
}

func (c stubConfig) GetReadConnectionString() string  { return dsn }
func (c stubConfig) GetWriteConnectionString() string { return dsn }
func (c stubConfig) GetMaxPingAttempts() uint64       { return c.maxPingAttempts }
func (c stubConfig) GetPingWaitPeriod() time.Duration { return c.pingWaitPeriod }
func (c stubConfig) GetMaxIdleConns() int             { return 1 }
func (c stubConfig) GetMaxOpenConns() int             { return 1 }
func (c stubConfig) GetConnMaxLifetime() time.Duration {
	return time.Minute
}

var _ database.ClientConfig = stubConfig{}

// pingableDB builds a mock handle whose pings are monitored, so a test can say
// whether the handle answers.
func pingableDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	must.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db, mock
}

func TestClose(T *testing.T) {
	T.Parallel()

	T.Run("closes both handles", func(t *testing.T) {
		t.Parallel()

		readDB, readMock := pingableDB(t)
		writeDB, writeMock := pingableDB(t)
		readMock.ExpectClose()
		writeMock.ExpectClose()

		test.NoError(t, Close(observability.NewObserver("test", nil, nil), readDB, writeDB))

		test.NoError(t, readMock.ExpectationsWereMet())
		test.NoError(t, writeMock.ExpectationsWereMet())
	})

	T.Run("a shared handle is closed once", func(t *testing.T) {
		t.Parallel()

		// One connection string means one handle serving both roles. Closing it
		// twice would report an error for a handle that shut down correctly.
		db, mock := pingableDB(t)
		mock.ExpectClose()

		test.NoError(t, Close(observability.NewObserver("test", nil, nil), db, db))
		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("the write handle is closed even when the read handle failed", func(t *testing.T) {
		t.Parallel()

		// The reason the two closes are not chained: a read-close error that
		// short-circuited would leak the write connection.
		readDB, readMock := pingableDB(t)
		writeDB, writeMock := pingableDB(t)

		readCloseErr := errors.New("read close failed")
		readMock.ExpectClose().WillReturnError(readCloseErr)
		writeMock.ExpectClose()

		test.ErrorIs(t, Close(observability.NewObserver("test", nil, nil), readDB, writeDB), readCloseErr)
		test.NoError(t, writeMock.ExpectationsWereMet())
	})

	T.Run("both failures are joined", func(t *testing.T) {
		t.Parallel()

		readDB, readMock := pingableDB(t)
		writeDB, writeMock := pingableDB(t)

		readCloseErr := errors.New("read close failed")
		writeCloseErr := errors.New("write close failed")
		readMock.ExpectClose().WillReturnError(readCloseErr)
		writeMock.ExpectClose().WillReturnError(writeCloseErr)

		err := Close(observability.NewObserver("test", nil, nil), readDB, writeDB)
		test.ErrorIs(t, err, readCloseErr)
		test.ErrorIs(t, err, writeCloseErr)
	})
}

func TestIsReady(T *testing.T) {
	T.Parallel()

	cfg := stubConfig{maxPingAttempts: 1, pingWaitPeriod: time.Millisecond}

	T.Run("both handles answering is ready", func(t *testing.T) {
		t.Parallel()

		readDB, readMock := pingableDB(t)
		writeDB, writeMock := pingableDB(t)
		readMock.ExpectPing()
		writeMock.ExpectPing()

		o11y := observability.NewObserver("test", nil, nil)
		ctx, op := o11y.Begin(t.Context())
		defer op.End()

		test.True(t, IsReady(ctx, op, cfg, readDB, writeDB))
	})

	T.Run("a shared handle is pinged once", func(t *testing.T) {
		t.Parallel()

		db, mock := pingableDB(t)
		mock.ExpectPing()

		o11y := observability.NewObserver("test", nil, nil)
		ctx, op := o11y.Begin(t.Context())
		defer op.End()

		test.True(t, IsReady(ctx, op, cfg, db, db))
		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("an unreachable read handle is not ready, and the write handle is never asked", func(t *testing.T) {
		t.Parallel()

		readDB, readMock := pingableDB(t)
		writeDB, writeMock := pingableDB(t)
		readMock.ExpectPing().WillReturnError(errors.New("read is down"))

		o11y := observability.NewObserver("test", nil, nil)
		ctx, op := o11y.Begin(t.Context())
		defer op.End()

		test.False(t, IsReady(ctx, op, cfg, readDB, writeDB))

		// No ping was expected of the write handle, so an unmet expectation here
		// would mean the probe kept going after the first role failed.
		test.NoError(t, writeMock.ExpectationsWereMet())
	})

	T.Run("a reachable read handle and an unreachable write handle is not ready", func(t *testing.T) {
		t.Parallel()

		readDB, readMock := pingableDB(t)
		writeDB, writeMock := pingableDB(t)
		readMock.ExpectPing()
		writeMock.ExpectPing().WillReturnError(errors.New("write is down"))

		o11y := observability.NewObserver("test", nil, nil)
		ctx, op := o11y.Begin(t.Context())
		defer op.End()

		test.False(t, IsReady(ctx, op, cfg, readDB, writeDB))
	})

	T.Run("the connection string never reaches the span or the log", func(t *testing.T) {
		t.Parallel()

		// IsReady is handed the whole config, credentials included, and passes
		// WaitForPing a role label instead. The attributes it does set are the
		// ping budget, which is not a secret.
		logger := newValueLogger()
		readDB, readMock := pingableDB(t)
		readMock.ExpectPing().WillReturnError(errors.New("read is down"))

		o11y := observability.NewObserver("test", logger, nil)
		ctx, op := o11y.Begin(t.Context())
		defer op.End()

		test.False(t, IsReady(ctx, op, cfg, readDB, readDB))
		test.SliceNotContains(t, logger.snapshot(), any(dsn))
	})
}

func TestWithTransaction(T *testing.T) {
	T.Parallel()

	noopRollback := func(_ context.Context, tx database.SQLQueryExecutorAndTransactionManager) {
		_ = tx.Rollback()
	}

	T.Run("commits when the callback returns nil", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectCommit()

		var ran bool
		test.NoError(t, WithTransaction(t.Context(), observability.NewObserver("test", nil, nil), db, noopRollback, nil, func(database.Tx) error {
			ran = true

			return nil
		}))

		test.True(t, ran)
		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("rolls back and returns the callback's error", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback()

		cause := errors.New("callback failed")

		test.ErrorIs(t, WithTransaction(t.Context(), observability.NewObserver("test", nil, nil), db, noopRollback, nil, func(database.Tx) error {
			return cause
		}), cause)

		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("reports a failure to begin", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		cause := errors.New("cannot begin")
		mock.ExpectBegin().WillReturnError(cause)

		test.ErrorIs(t, WithTransaction(t.Context(), observability.NewObserver("test", nil, nil), db, noopRollback, nil, func(database.Tx) error {
			t.Error("callback ran despite the transaction never beginning")

			return nil
		}), cause)
	})
}

func TestWithTransaction_RetryOnConflict(T *testing.T) {
	T.Parallel()

	noopRollback := func(_ context.Context, tx database.SQLQueryExecutorAndTransactionManager) {
		_ = tx.Rollback()
	}

	errConflict := errors.New("deadlock found when trying to get lock")
	isConflict := func(err error) bool { return stderrors.Is(err, errConflict) }

	T.Run("re-runs the callback on a conflict and commits the attempt that succeeds", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback()
		mock.ExpectBegin()
		mock.ExpectCommit()

		var runs int

		test.NoError(t, WithTransaction(t.Context(), observability.NewObserver("test", nil, nil), db, noopRollback, isConflict, func(database.Tx) error {
			runs++
			if runs == 1 {
				return errConflict
			}

			return nil
		}, database.RetryOnConflict(3)))

		test.EqOp(t, 2, runs)
		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("wraps the last conflict as exhausted when every attempt conflicts", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback()
		mock.ExpectBegin()
		mock.ExpectRollback()

		var runs int

		err = WithTransaction(t.Context(), observability.NewObserver("test", nil, nil), db, noopRollback, isConflict, func(database.Tx) error {
			runs++

			return errConflict
		}, database.RetryOnConflict(2))

		test.EqOp(t, 2, runs)
		test.ErrorIs(t, err, retry.ErrExhausted)
		test.ErrorIs(t, err, errConflict)

		attempts, ok := retry.Attempts(err)
		test.True(t, ok)
		test.EqOp(t, uint(2), attempts)
		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("runs once without the option, returning the conflict unwrapped", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback()

		var runs int

		err = WithTransaction(t.Context(), observability.NewObserver("test", nil, nil), db, noopRollback, isConflict, func(database.Tx) error {
			runs++

			return errConflict
		})

		test.EqOp(t, 1, runs)
		test.ErrorIs(t, err, errConflict)
		test.False(t, stderrors.Is(err, retry.ErrExhausted))
		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("treats zero attempts as a single attempt", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback()

		var runs int

		err = WithTransaction(t.Context(), observability.NewObserver("test", nil, nil), db, noopRollback, isConflict, func(database.Tx) error {
			runs++

			return errConflict
		}, database.RetryOnConflict(0))

		test.EqOp(t, 1, runs)
		test.ErrorIs(t, err, errConflict)
		test.False(t, stderrors.Is(err, retry.ErrExhausted))
		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("does not retry an error that is not a conflict", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback()

		cause := errors.New("unique constraint violated")

		var runs int

		err = WithTransaction(t.Context(), observability.NewObserver("test", nil, nil), db, noopRollback, isConflict, func(database.Tx) error {
			runs++

			return cause
		}, database.RetryOnConflict(3))

		test.EqOp(t, 1, runs)
		test.ErrorIs(t, err, cause)
		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("retries nothing when the client recognizes no conflicts", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback()

		var runs int

		err = WithTransaction(t.Context(), observability.NewObserver("test", nil, nil), db, noopRollback, nil, func(database.Tx) error {
			runs++

			return errConflict
		}, database.RetryOnConflict(3))

		test.EqOp(t, 1, runs)
		test.ErrorIs(t, err, errConflict)
		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("stops retrying once the context is canceled", func(t *testing.T) {
		t.Parallel()

		// Canceling ctx makes database/sql roll the transaction back itself, on a
		// goroutine of its own, so whether the rollback has reached the mock by
		// the time WithTransaction returns is a race. Expectations are not
		// checked here for that reason.
		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback()

		ctx, cancel := context.WithCancel(t.Context())

		var runs int

		err = WithTransaction(ctx, observability.NewObserver("test", nil, nil), db, noopRollback, isConflict, func(database.Tx) error {
			runs++

			cancel()

			return errConflict
		}, database.RetryOnConflict(3))

		test.EqOp(t, 1, runs)
		test.ErrorIs(t, err, errConflict)
		test.False(t, stderrors.Is(err, retry.ErrExhausted))
	})
}

func TestConflictDelay(T *testing.T) {
	T.Parallel()

	T.Run("doubles from the initial ceiling", func(t *testing.T) {
		t.Parallel()

		cfg := database.NewTxConfig(database.ConflictBackoff(5*time.Millisecond, time.Second))

		test.EqOp(t, 5*time.Millisecond, conflictDelay(cfg, 1))
		test.EqOp(t, 10*time.Millisecond, conflictDelay(cfg, 2))
		test.EqOp(t, 20*time.Millisecond, conflictDelay(cfg, 3))
	})

	T.Run("stops at the cap", func(t *testing.T) {
		t.Parallel()

		cfg := database.NewTxConfig(database.ConflictBackoff(5*time.Millisecond, 30*time.Millisecond))

		test.EqOp(t, 30*time.Millisecond, conflictDelay(cfg, 4))
		test.EqOp(t, 30*time.Millisecond, conflictDelay(cfg, 5))
	})

	T.Run("holds the cap past where a shift would overflow", func(t *testing.T) {
		t.Parallel()

		// 5ms shifted left overflows an int64 of nanoseconds from about the
		// 42nd attempt, which is what the cap exists to rule out.
		cfg := database.NewTxConfig()

		for _, attempt := range []uint{41, 42, 64, 1000} {
			test.EqOp(t, database.DefaultConflictBackoffMax, conflictDelay(cfg, attempt))
		}
	})
}

func TestRollbackTransaction(T *testing.T) {
	T.Parallel()

	T.Run("rolls the transaction back", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback()

		tx, err := db.BeginTx(t.Context(), nil)
		must.NoError(t, err)

		RollbackTransaction(t.Context(), observability.NewObserver("test", nil, nil), tx)

		test.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("a rollback failure is recorded rather than returned", func(t *testing.T) {
		t.Parallel()

		// There is no caller who could act on it: the transaction is already
		// being abandoned, and the connection is poisoned or it is not. So the
		// signature has nowhere to put an error, and this must not panic.
		db, mock, err := sqlmock.New()
		must.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectRollback().WillReturnError(errors.New("rollback failed"))

		tx, err := db.BeginTx(t.Context(), nil)
		must.NoError(t, err)

		RollbackTransaction(t.Context(), observability.NewObserver("test", nil, nil), tx)

		test.NoError(t, mock.ExpectationsWereMet())
	})
}
