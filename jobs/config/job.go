package jobscfg

import (
	"context"
	"time"

	"github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/jobs"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// JobConfig is one scheduled job's configuration: whether it runs, when, and
// under what limits. The work itself is code, supplied to Job.
//
// Exactly one of Schedule and Interval is set on an enabled job. A job belongs
// either at an hour or at a frequency, and one carrying both is rejected rather
// than resolved by precedence.
//
// It carries no prefix of its own, because the name is the embedding struct's
// to choose. A service with several jobs gives each field its own:
//
//	AuditSweep jobscfg.JobConfig `envPrefix:"AUDIT_SWEEP_"`
type JobConfig struct {
	_ struct{} `json:"-" yaml:"-"`

	// Schedule is a five-field crontab expression — minute, hour, day of
	// month, month, day of week — for work that belongs at a wall-clock time
	// rather than at a frequency. The descriptors (@daily, @hourly) are
	// accepted too. See jobs.Cron.
	//
	// The zone is the Scheduler's Timezone unless the expression names its own
	// with a CRON_TZ= prefix.
	Schedule string `env:"SCHEDULE" json:"schedule,omitempty" yaml:"schedule,omitempty"`
	// Interval is how often the job fires, for work that belongs at a
	// frequency rather than at an hour.
	Interval time.Duration `env:"INTERVAL" json:"interval,omitempty" yaml:"interval,omitempty"`
	// Timeout bounds one execution. Zero falls back to the Scheduler's
	// DefaultTimeout.
	Timeout time.Duration `env:"TIMEOUT" json:"timeout,omitempty" yaml:"timeout,omitempty"`
	// LeaseTTL is how long the run's lock is held. Zero falls back to the
	// Scheduler's DefaultLeaseTTL. It is not renewed while the job runs, so it
	// must comfortably exceed the job's worst-case duration — past it, a second
	// replica may start the same job.
	LeaseTTL time.Duration `env:"LEASE_TTL" json:"leaseTTL,omitempty" yaml:"leaseTTL,omitempty"`
	// Enabled says whether the job is registered at all. A disabled job is not
	// validated, and Job does not consult it: the caller skips a disabled job
	// rather than registering one that never fires, so it costs nothing and
	// reports nothing.
	Enabled bool `env:"ENABLED" json:"enabled" yaml:"enabled"`
	// RunOnStart fires the job once when the Scheduler starts, instead of
	// waiting a full interval or for the schedule's next fire time.
	RunOnStart bool `env:"RUN_ON_START" json:"runOnStart,omitempty" yaml:"runOnStart,omitempty"`
}

var _ validation.ValidatableWithContext = (*JobConfig)(nil)

// ValidateWithContext validates a JobConfig. A disabled job is not validated:
// it is never registered, so its schedule is inert.
//
// The schedule is parsed here rather than left to Scheduler.Register so that a
// bad expression fails config validation, where it is a red build, instead of
// scheduler startup, where it is a crash loop.
func (cfg *JobConfig) ValidateWithContext(ctx context.Context) error {
	if !cfg.Enabled {
		return nil
	}

	switch {
	case cfg.Schedule != "" && cfg.Interval != 0:
		return errors.Wrapf(jobs.ErrInvalidJob, "job sets both an interval and a cron schedule %q", cfg.Schedule)
	case cfg.Schedule == "" && cfg.Interval == 0:
		return errors.Wrap(jobs.ErrInvalidJob, "job sets neither an interval nor a cron schedule")
	}

	if cfg.Schedule != "" {
		if _, err := jobs.Cron(cfg.Schedule); err != nil {
			return errors.Wrap(err, "parsing cron schedule")
		}
	}

	return validation.ValidateStructWithContext(ctx, cfg,
		validation.Field(&cfg.Interval, validation.When(cfg.Schedule == "", validation.Min(time.Second))),
		validation.Field(&cfg.Timeout, validation.Min(time.Duration(0))),
		validation.Field(&cfg.LeaseTTL, validation.When(cfg.LeaseTTL != 0, validation.Min(time.Second))),
	)
}

// Job renders the config as the jobs.Job a Scheduler registers, under the given
// name and running the given work.
//
// Which of Interval and Schedule the job is shaped by is decided here rather
// than at the call site: jobs.Job takes them as separate fields and rejects a
// job that sets both, so the mapping is the other half of the invariant
// ValidateWithContext enforces.
func (cfg *JobConfig) Job(name string, run func(context.Context) error) (jobs.Job, error) {
	if cfg == nil || run == nil {
		return jobs.Job{}, errors.ErrNilInputParameter
	}

	job := jobs.Job{
		Name:       name,
		Run:        run,
		Interval:   cfg.Interval,
		Timeout:    cfg.Timeout,
		LeaseTTL:   cfg.LeaseTTL,
		RunOnStart: cfg.RunOnStart,
	}

	if cfg.Schedule != "" {
		schedule, err := jobs.Cron(cfg.Schedule)
		if err != nil {
			return jobs.Job{}, errors.Wrapf(err, "parsing cron schedule for job %q", name)
		}

		job.Schedule = schedule
	}

	return job, nil
}
