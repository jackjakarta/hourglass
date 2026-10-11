package jobs

import (
	"context"
	"errors"

	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/runner"
	"github.com/jackjakarta/gocron/internal/store"
)

// ErrNoHistory means there's no run history to read.
var ErrNoHistory = errors.New("run history isn't available")

// Runs returns the run history, opening it on first use.
func (s *Service) Runs(ctx context.Context) (*store.DB, error) {
	s.runsOnce.Do(func() {
		if s.opts.OpenRuns == nil {
			s.runsErr = ErrNoHistory
			return
		}
		s.runs, s.runsErr = s.opts.OpenRuns(ctx)
	})
	return s.runs, s.runsErr
}

// Stats summarizes each job's recent runs, by job id.
func (s *Service) Stats(ctx context.Context) (map[string]store.JobStats, error) {
	db, err := s.Runs(ctx)
	if err != nil {
		return nil, err
	}
	return db.Stats(ctx)
}

// History lists runs, newest first.
func (s *Service) History(ctx context.Context, f store.Filter) ([]store.Run, error) {
	db, err := s.Runs(ctx)
	if err != nil {
		return nil, err
	}
	return db.List(ctx, f)
}

// RunKind says how a launched run is set up.
type RunKind int

const (
	// RunManual runs the job now: the user's environment with the
	// crontab's on top, cron's shell, from $HOME.
	RunManual RunKind = iota
	// RunCronEnv tests the job in the environment cron would give it.
	RunCronEnv
	// RunUserEnv runs the job exactly as the user's shell would, to
	// compare with a failed cron-environment test.
	RunUserEnv
)

// Launched is a run started from gocron.
type Launched struct {
	*runner.Handle
	Spec    runner.Spec
	Kind    RunKind
	CronEnv []string // the environment cron would give the job, for Diagnose
}

// Plan describes how to run j: a managed job, an unmanaged entry (run
// unrecorded), or a draft (ID "" for a new one; Line places it among the
// crontab's environment lines). It honors the job's timeout and overlap
// setting. It also returns the environment cron would give the job.
func (s *Service) Plan(j crontab.Job, kind RunKind) (spec runner.Spec, cronEnv []string) {
	lines := s.Snapshot().EnvAt(j.Line)
	cronEnv = runner.CronEnv(*s.opts.Identity, lines)
	spec = runner.Spec{
		JobID:   j.ID,
		Record:  j.Managed && j.ID != "",
		Command: j.Command,
		Trigger: store.TriggerManual,
		Dir:     runner.Lookup(cronEnv, "HOME"),
	}
	if j.Wrapped {
		spec.Timeout, spec.NoOverlap = j.Timeout, j.NoOverlap
	}
	switch kind {
	case RunManual:
		spec.Env = runner.ManualEnv(s.opts.Environ, lines)
	case RunCronEnv:
		spec.Env, spec.Trigger = cronEnv, store.TriggerTest
	case RunUserEnv:
		spec.Env = append([]string(nil), s.opts.Environ...)
	}
	return spec, cronEnv
}

// Launch starts a run of j as Plan describes it.
func (s *Service) Launch(j crontab.Job, kind RunKind) (*Launched, error) {
	if s.opts.Launcher == nil {
		return nil, runner.ErrNoLauncher
	}
	spec, cronEnv := s.Plan(j, kind)
	h, err := s.opts.Launcher.Launch(spec)
	if err != nil {
		return nil, err
	}
	return &Launched{Handle: h, Spec: spec, Kind: kind, CronEnv: cronEnv}, nil
}

// UserEnv is the user's own environment, as gocron sees it.
func (s *Service) UserEnv() []string { return s.opts.Environ }
