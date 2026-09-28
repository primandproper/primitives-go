package jobscfg

import (
	"context"
	"testing"
	"time"

	"github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/jobs"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func noopRun(context.Context) error { return nil }

func TestJobConfig_ValidateWithContext(T *testing.T) {
	T.Parallel()

	T.Run("accepts an interval job", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Interval: time.Minute}

		test.NoError(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("accepts a scheduled job", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Schedule: "0 3 * * *", Timeout: time.Minute, LeaseTTL: time.Hour}

		test.NoError(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("ignores a disabled job", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Schedule: "not a cron spec", Interval: -time.Second}

		test.NoError(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("rejects both an interval and a schedule", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Schedule: "@hourly", Interval: time.Minute}

		test.ErrorIs(t, cfg.ValidateWithContext(t.Context()), jobs.ErrInvalidJob)
	})

	T.Run("rejects neither an interval nor a schedule", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true}

		test.ErrorIs(t, cfg.ValidateWithContext(t.Context()), jobs.ErrInvalidJob)
	})

	T.Run("rejects an unparseable schedule", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Schedule: "every tuesday"}

		test.ErrorIs(t, cfg.ValidateWithContext(t.Context()), jobs.ErrInvalidCronSpec)
	})

	T.Run("rejects a sub-second interval", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Interval: time.Millisecond}

		test.Error(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("rejects a negative interval", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Interval: -time.Minute}

		test.Error(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("rejects a negative timeout", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Interval: time.Minute, Timeout: -time.Second}

		test.Error(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("rejects a sub-second lease", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Interval: time.Minute, LeaseTTL: time.Millisecond}

		test.Error(t, cfg.ValidateWithContext(t.Context()))
	})

	T.Run("rejects a negative lease", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Interval: time.Minute, LeaseTTL: -time.Minute}

		test.Error(t, cfg.ValidateWithContext(t.Context()))
	})
}

func TestJobConfig_Job(T *testing.T) {
	T.Parallel()

	T.Run("renders an interval job", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{
			Enabled:    true,
			Interval:   time.Minute,
			Timeout:    10 * time.Second,
			LeaseTTL:   2 * time.Minute,
			RunOnStart: true,
		}

		job, err := cfg.Job("sweep", noopRun)
		must.NoError(t, err)

		test.EqOp(t, "sweep", job.Name)
		test.NotNil(t, job.Run)
		test.Nil(t, job.Schedule)
		test.EqOp(t, time.Minute, job.Interval)
		test.EqOp(t, 10*time.Second, job.Timeout)
		test.EqOp(t, 2*time.Minute, job.LeaseTTL)
		test.True(t, job.RunOnStart)
	})

	T.Run("renders a scheduled job", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Schedule: "0 3 * * *"}

		job, err := cfg.Job("nightly", noopRun)
		must.NoError(t, err)

		must.NotNil(t, job.Schedule)
		test.EqOp(t, time.Duration(0), job.Interval)

		from := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
		test.EqOp(t, time.Date(2026, 3, 8, 3, 0, 0, 0, time.UTC), job.Schedule.Next(from).UTC())
	})

	T.Run("renders a job the scheduler accepts", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Schedule: "@hourly"}

		job, err := cfg.Job("hourly", noopRun)
		must.NoError(t, err)

		s, err := NewScheduler(t.Context(), schedulerConfig(), nil)
		must.NoError(t, err)

		test.NoError(t, s.Register(job))
	})

	T.Run("rejects an unparseable schedule", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Schedule: "every tuesday"}

		_, err := cfg.Job("broken", noopRun)
		test.ErrorIs(t, err, jobs.ErrInvalidCronSpec)
	})

	T.Run("rejects a nil config", func(t *testing.T) {
		t.Parallel()

		var cfg *JobConfig

		_, err := cfg.Job("nothing", noopRun)
		test.ErrorIs(t, err, errors.ErrNilInputParameter)
	})

	T.Run("rejects a nil function", func(t *testing.T) {
		t.Parallel()

		cfg := &JobConfig{Enabled: true, Interval: time.Minute}

		_, err := cfg.Job("nothing", nil)
		test.ErrorIs(t, err, errors.ErrNilInputParameter)
	})
}
