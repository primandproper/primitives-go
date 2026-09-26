package mysql

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/testutils/containers/mysqltest"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// The claim this file exists to check is one no unit test can make: that the
// deadlock a real InnoDB reports, having come up through otelsql and the
// transaction wrapper, is still the error isRetryableConflict recognizes.

// deadlockPair runs two transactions that lock rows 1 and 2 of table in
// opposite orders, each waiting for the other to take its first lock before
// reaching for its second, which is a lock cycle InnoDB breaks by rolling one
// back with 1213. The choreography runs once: a retried attempt finds both
// signals already sent and takes its locks without waiting.
//
// It returns how many times the two callbacks ran in total, and both
// transactions' errors.
func deadlockPair(t *testing.T, client *Client, table string, opts ...database.TxOption) (runs int64, errA, errB error) {
	t.Helper()

	var (
		runCount         atomic.Int64
		aLocked, bLocked = make(chan struct{}), make(chan struct{})
		signalA, signalB sync.Once
		wg               sync.WaitGroup
		updateStatement  = fmt.Sprintf("UPDATE %s SET v = v + 1 WHERE id = ?", table)
		lockThenReachFor = func(ctx context.Context, tx database.Tx, first, second int, signal *sync.Once, locked, other chan struct{}) error {
			runCount.Add(1)

			if _, err := tx.ExecContext(ctx, updateStatement, first); err != nil {
				return err
			}

			signal.Do(func() { close(locked) })
			<-other

			_, err := tx.ExecContext(ctx, updateStatement, second)

			return err
		}
	)

	ctx := t.Context()

	wg.Go(func() {
		errA = database.WithTransaction(ctx, client, func(tx database.Tx) error {
			return lockThenReachFor(ctx, tx, 1, 2, &signalA, aLocked, bLocked)
		}, opts...)
	})

	wg.Go(func() {
		<-aLocked

		errB = database.WithTransaction(ctx, client, func(tx database.Tx) error {
			return lockThenReachFor(ctx, tx, 2, 1, &signalB, bLocked, aLocked)
		}, opts...)
	})

	wg.Wait()

	return runCount.Load(), errA, errB
}

func TestClient_WithTransaction_Container(T *testing.T) {
	T.Parallel()

	mysqltest.Run(T, func(ctx context.Context, my *mysqltest.Instance) {
		client, err := NewDatabaseClient(ctx, &testClientConfig{connectionString: my.ConnectionString, maxPingAttempts: 1})
		must.NoError(T, err)
		T.Cleanup(func() { _ = client.Close() })

		newTable := func(t *testing.T, name string) string {
			t.Helper()

			_, execErr := my.DB.ExecContext(ctx, fmt.Sprintf(
				"CREATE TABLE %[1]s (id INT PRIMARY KEY, v INT NOT NULL); INSERT INTO %[1]s (id, v) VALUES (1, 0), (2, 0)",
				name,
			))
			must.NoError(t, execErr)

			return name
		}

		T.Run("retries the deadlock victim until both transactions commit", func(t *testing.T) {
			t.Parallel()

			table := newTable(t, "retry_on_conflict")

			runs, errA, errB := deadlockPair(t, client, table, database.RetryOnConflict(3))

			test.NoError(t, errA)
			test.NoError(t, errB)
			test.EqOp(t, int64(3), runs)

			var total int
			must.NoError(t, my.DB.QueryRowContext(ctx, fmt.Sprintf("SELECT SUM(v) FROM %s", table)).Scan(&total))
			test.EqOp(t, 4, total)
		})

		T.Run("without the option the victim sees the deadlock", func(t *testing.T) {
			t.Parallel()

			table := newTable(t, "no_retry_on_conflict")

			runs, errA, errB := deadlockPair(t, client, table)

			test.EqOp(t, int64(2), runs)
			test.True(t, (errA == nil) != (errB == nil))

			victim := errA
			if victim == nil {
				victim = errB
			}

			test.True(t, isRetryableConflict(victim))
		})
	})
}
