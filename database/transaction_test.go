package database_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/primandproper/primitives-go/v2/database"
	databasemock "github.com/primandproper/primitives-go/v2/database/mock"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// rollbackRecorder mimics a Client.RollbackTransaction: it rolls the transaction back
// (satisfying sqlmock's ExpectRollback) and records how many times it was invoked.
type rollbackRecorder struct {
	calls int
}

func (r *rollbackRecorder) rollback(_ context.Context, tx database.SQLQueryExecutorAndTransactionManager) {
	r.calls++
	_ = tx.Rollback()
}

func newRunInTxTestDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New()
	must.NoError(t, err)

	return db, mock
}

func TestRunInTransaction(T *testing.T) {
	T.Parallel()

	T.Run("commits when fn returns nil", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db, mock := newRunInTxTestDB(t)
		rb := &rollbackRecorder{}

		mock.ExpectBegin()
		mock.ExpectExec("UPDATE things").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		var gotQuerier database.SQLQueryExecutor
		err := database.RunInTransaction(ctx, db, rb.rollback, func(querier database.Tx) error {
			gotQuerier = querier
			_, execErr := querier.ExecContext(ctx, "UPDATE things SET x = 1")
			return execErr
		})

		test.NoError(t, err)
		test.NotNil(t, gotQuerier)
		test.EqOp(t, 0, rb.calls)
		must.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("rolls back and returns the error when fn fails", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db, mock := newRunInTxTestDB(t)
		rb := &rollbackRecorder{}

		mock.ExpectBegin()
		mock.ExpectRollback()

		sentinel := errors.New("fn failed")
		err := database.RunInTransaction(ctx, db, rb.rollback, func(_ database.Tx) error {
			return sentinel
		})

		test.ErrorIs(t, err, sentinel)
		test.EqOp(t, 1, rb.calls)
		must.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("wraps begin errors without invoking fn or rollback", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db, mock := newRunInTxTestDB(t)
		rb := &rollbackRecorder{}

		beginErr := errors.New("cannot begin")
		mock.ExpectBegin().WillReturnError(beginErr)

		fnCalled := false
		err := database.RunInTransaction(ctx, db, rb.rollback, func(_ database.Tx) error {
			fnCalled = true
			return nil
		})

		test.ErrorIs(t, err, beginErr)
		test.StrContains(t, err.Error(), "beginning transaction")
		test.False(t, fnCalled)
		test.EqOp(t, 0, rb.calls)
		must.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("wraps commit errors and does not roll back again", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db, mock := newRunInTxTestDB(t)
		rb := &rollbackRecorder{}

		commitErr := errors.New("cannot commit")
		mock.ExpectBegin()
		mock.ExpectCommit().WillReturnError(commitErr)

		err := database.RunInTransaction(ctx, db, rb.rollback, func(_ database.Tx) error {
			return nil
		})

		test.ErrorIs(t, err, commitErr)
		test.StrContains(t, err.Error(), "committing transaction")
		// A failed commit already released the connection, so a second rollback would only
		// surface a spurious ErrTxDone.
		test.EqOp(t, 0, rb.calls)
		must.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("rolls back and re-panics when fn panics", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db, mock := newRunInTxTestDB(t)
		rb := &rollbackRecorder{}

		mock.ExpectBegin()
		mock.ExpectRollback()

		recovered := func() (r any) {
			defer func() { r = recover() }()
			_ = database.RunInTransaction(ctx, db, rb.rollback, func(_ database.Tx) error {
				panic("boom")
			})
			return nil
		}()

		test.EqOp(t, "boom", recovered)
		test.EqOp(t, 1, rb.calls)
		must.NoError(t, mock.ExpectationsWereMet())
	})

	T.Run("returns an error for nil dependencies", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db, _ := newRunInTxTestDB(t)
		rb := &rollbackRecorder{}
		noopFn := func(_ database.Tx) error { return nil }

		test.Error(t, database.RunInTransaction(ctx, nil, rb.rollback, noopFn))
		test.Error(t, database.RunInTransaction(ctx, db, nil, noopFn))
		test.Error(t, database.RunInTransaction(ctx, db, rb.rollback, nil))
	})
}

func TestNewTxConfig(T *testing.T) {
	T.Parallel()

	T.Run("RetryOnConflict sets the attempt budget", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, uint(3), database.NewTxConfig(database.RetryOnConflict(3)).ConflictAttempts)
	})

	T.Run("no options mean a single attempt", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, uint(0), database.NewTxConfig().ConflictAttempts)
	})

	T.Run("later options win and nil options are skipped", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, uint(5), database.NewTxConfig(database.RetryOnConflict(2), nil, database.RetryOnConflict(5)).ConflictAttempts)
	})

	T.Run("the backoff defaults when no option sets it", func(t *testing.T) {
		t.Parallel()

		cfg := database.NewTxConfig(database.RetryOnConflict(3))

		test.EqOp(t, database.DefaultConflictBackoffInitial, cfg.ConflictBackoffInitial)
		test.EqOp(t, database.DefaultConflictBackoffMax, cfg.ConflictBackoffMax)
	})

	T.Run("ConflictBackoff sets both halves", func(t *testing.T) {
		t.Parallel()

		cfg := database.NewTxConfig(database.ConflictBackoff(time.Millisecond, time.Second))

		test.EqOp(t, time.Millisecond, cfg.ConflictBackoffInitial)
		test.EqOp(t, time.Second, cfg.ConflictBackoffMax)
	})

	T.Run("a zero or negative half keeps its default", func(t *testing.T) {
		t.Parallel()

		cfg := database.NewTxConfig(database.ConflictBackoff(0, 2*time.Second))

		test.EqOp(t, database.DefaultConflictBackoffInitial, cfg.ConflictBackoffInitial)
		test.EqOp(t, 2*time.Second, cfg.ConflictBackoffMax)

		cfg = database.NewTxConfig(database.ConflictBackoff(time.Millisecond, -time.Second))

		test.EqOp(t, time.Millisecond, cfg.ConflictBackoffInitial)
		test.EqOp(t, database.DefaultConflictBackoffMax, cfg.ConflictBackoffMax)
	})

	T.Run("a cap below the initial pause lowers the initial pause to it", func(t *testing.T) {
		t.Parallel()

		cfg := database.NewTxConfig(database.ConflictBackoff(time.Second, 10*time.Millisecond))

		test.EqOp(t, 10*time.Millisecond, cfg.ConflictBackoffInitial)
		test.EqOp(t, 10*time.Millisecond, cfg.ConflictBackoffMax)
	})
}

// optionsClient is a Client with TxOptionsAccess, recording the options each
// transaction was handed.
type optionsClient struct {
	*databasemock.ClientMock

	got []database.TxConfig
}

func (c *optionsClient) WithTransactionOptions(_ context.Context, fn func(database.Tx) error, opts ...database.TxOption) error {
	c.got = append(c.got, database.NewTxConfig(opts...))

	return fn(nil)
}

func TestWithTransaction(T *testing.T) {
	T.Parallel()

	T.Run("hands the options to a client with TxOptionsAccess", func(t *testing.T) {
		t.Parallel()

		client := &optionsClient{ClientMock: &databasemock.ClientMock{}}
		runs := 0

		must.NoError(t, database.WithTransaction(t.Context(), client, func(database.Tx) error {
			runs++

			return nil
		}, database.RetryOnConflict(3)))

		test.EqOp(t, 1, runs)
		must.SliceLen(t, 1, client.got)
		test.EqOp(t, uint(3), client.got[0].ConflictAttempts)
		test.SliceEmpty(t, client.WithTransactionCalls())
	})

	T.Run("runs fn once through a client without it", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("from fn")
		client := &databasemock.ClientMock{
			WithTransactionFunc: func(_ context.Context, fn func(database.Tx) error) error {
				return fn(nil)
			},
		}
		runs := 0

		err := database.WithTransaction(t.Context(), client, func(database.Tx) error {
			runs++

			return sentinel
		}, database.RetryOnConflict(3))

		test.ErrorIs(t, err, sentinel)
		test.EqOp(t, 1, runs)
		test.SliceLen(t, 1, client.WithTransactionCalls())
	})
}
