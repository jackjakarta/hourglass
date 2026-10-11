// Package jobs is the façade the TUI and CLI use to read and change cron
// jobs. Every change goes through crontab.Store's safe-write path and can
// be undone.
package jobs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackjakarta/gocron/internal/crontab"
)

// Job is a crontab job as the UI shows it. Run statistics join it in a
// later phase.
type Job struct {
	crontab.Job
}

// Snapshot is the crontab as loaded.
type Snapshot struct {
	Jobs     []Job // managed jobs and unmanaged entries, in crontab order
	Problems []crontab.Problem
	Env      []EnvLine // environment lines, in crontab order
	Source   string    // "user crontab" | "file: /path"
}

// EnvLine is an environment line and where it is.
type EnvLine struct {
	Line int // 1-based
	crontab.EnvVar
}

// DefaultCronPATH is the PATH cron gives jobs when the crontab sets none.
const DefaultCronPATH = "/usr/bin:/bin"

// CronEnv is the environment cron gives a job, as far as gocron uses it.
type CronEnv struct {
	PATH string // cron's PATH for the job
	TZ   string // CRON_TZ in effect, if any
}

// CronEnv returns the environment for a job on line (1-based): cron
// applies an environment line to the entries after it. Line 0 means a new
// job, which is appended after every line.
func (s Snapshot) CronEnv(line int) CronEnv {
	env := CronEnv{PATH: DefaultCronPATH}
	for _, e := range s.Env {
		if line > 0 && e.Line >= line {
			break
		}
		switch e.Name {
		case "PATH":
			env.PATH = e.Value
		case "CRON_TZ":
			env.TZ = e.Value
		}
	}
	return env
}

// Draft is a job as the editor or the CLI describes it.
type Draft struct {
	Name     string
	Schedule string
	Command  string
	Tags     []string
	// Wrapped asks for run tracking via gocron exec; Timeout and NoOverlap
	// only apply to wrapped jobs.
	Wrapped   bool
	Timeout   time.Duration
	NoOverlap bool
}

// ParseTags splits comma-separated tags, trimming spaces and a leading
// "#" and dropping empty ones.
func ParseTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimPrefix(strings.TrimSpace(t), "#"); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Result describes a saved change.
type Result struct {
	ID     string // the job changed or created
	Backup string // the previous crontab; "" when nothing changed
}

var (
	// ErrNothingToUndo means no change made in this session is left to undo.
	ErrNothingToUndo = errors.New("nothing to undo")
	// ErrUndoStale means the crontab changed since the last change, so
	// undoing it would discard someone else's edit.
	ErrUndoStale = errors.New("the crontab changed since your last change, so it can't be undone")
)

// ConflictError means the crontab changed outside gocron before a save.
// It matches crontab.ErrConflict with errors.Is.
type ConflictError struct {
	// Retry reloads the crontab and applies the same change to it. It is
	// nil when the change can't be replayed (undo).
	Retry func(context.Context) (Result, error)
}

func (e *ConflictError) Error() string { return crontab.ErrConflict.Error() }
func (e *ConflictError) Unwrap() error { return crontab.ErrConflict }

// adoptWraps decides whether adopting rewrites an entry to run through
// "gocron exec". It stays off until exec exists; a wrapped line would fail.
const adoptWraps = false

// WrapNew decides whether new jobs, or jobs gaining run tracking, can be
// wrapped. It stays off until gocron exec exists, for the same reason.
// Jobs that are already wrapped keep their wrapping.
const WrapNew = false

const maxUndo = 50

type undoEntry struct {
	before []byte
	after  [32]byte // hash of the crontab the change produced
}

// Service reads and changes jobs. It is safe for concurrent use.
type Service struct {
	mu    sync.Mutex
	store *crontab.Store
	bin   string // gocron binary for wrapped lines
	undo  []undoEntry
}

// New returns a Service over store. bin is the gocron binary path written
// into wrapped job lines.
func New(store *crontab.Store, bin string) *Service {
	return &Service{store: store, bin: bin}
}

// Snapshot returns the jobs and problems of the loaded crontab.
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	tab := s.store.Crontab()
	snap := Snapshot{Problems: tab.Problems(), Source: s.store.Source().Name()}
	for _, j := range tab.Jobs() {
		snap.Jobs = append(snap.Jobs, Job{j})
	}
	for i, l := range tab.Lines {
		if l.Kind == crontab.Env {
			snap.Env = append(snap.Env, EnvLine{Line: i + 1, EnvVar: *l.Env})
		}
	}
	return snap
}

// CanUndo reports whether Undo has a change to revert.
func (s *Service) CanUndo() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.undo) > 0
}

// SetEnabled pauses or resumes the managed job id.
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool) (Result, error) {
	return s.apply(ctx, func(c *crontab.Crontab) (string, error) {
		return id, c.SetEnabled(id, enabled)
	})
}

// Create appends a new, enabled managed job.
func (s *Service) Create(ctx context.Context, d Draft) (Result, error) {
	return s.apply(ctx, func(c *crontab.Crontab) (string, error) {
		j := crontab.Job{
			Name:     d.Name,
			Schedule: d.Schedule,
			Command:  d.Command,
			Tags:     d.Tags,
			Enabled:  true,
			Wrapped:  d.Wrapped && WrapNew,
		}
		if j.Wrapped {
			j.Timeout, j.NoOverlap = d.Timeout, d.NoOverlap
		}
		return c.AddJob(j, s.bin)
	})
}

// Update applies the draft to the managed job id. It reads the job from
// the crontab at save time and changes only what a draft describes, so
// its paused state, description and binary path stay as they are there.
// A draft that changes nothing writes nothing.
func (s *Service) Update(ctx context.Context, id string, d Draft) (Result, error) {
	return s.apply(ctx, func(c *crontab.Crontab) (string, error) {
		cur, ok := c.Job(id)
		if !ok {
			return id, fmt.Errorf("%w: %s", crontab.ErrNotFound, id)
		}
		next := cur
		next.Name, next.Schedule, next.Command, next.Tags = d.Name, d.Schedule, d.Command, d.Tags
		next.Wrapped = d.Wrapped && (cur.Wrapped || WrapNew)
		next.Timeout, next.NoOverlap = 0, false
		if next.Wrapped {
			next.Timeout, next.NoOverlap = d.Timeout, d.NoOverlap
		}
		if sameDefinition(cur, next) {
			return id, nil
		}
		bin := "" // keep the path a wrapped line already has
		if next.Wrapped && next.Bin == "" {
			bin = s.bin
		}
		return id, c.UpdateJob(next, bin)
	})
}

func sameDefinition(a, b crontab.Job) bool {
	return a.Name == b.Name && strings.Join(strings.Fields(a.Schedule), " ") == strings.Join(strings.Fields(b.Schedule), " ") &&
		a.Command == b.Command && slices.Equal(a.Tags, b.Tags) &&
		a.Wrapped == b.Wrapped && a.Timeout == b.Timeout && a.NoOverlap == b.NoOverlap
}

// Delete removes the managed job id.
func (s *Service) Delete(ctx context.Context, id string) (Result, error) {
	return s.apply(ctx, func(c *crontab.Crontab) (string, error) {
		return id, c.RemoveJob(id)
	})
}

// Adopt makes the unmanaged entry on line (1-based) with text raw a managed
// job, named after the comment above it or its program.
func (s *Service) Adopt(ctx context.Context, line int, raw string) (Result, error) {
	return s.apply(ctx, func(c *crontab.Crontab) (string, error) {
		return c.Adopt(line, raw, c.DefaultAdoptName(line), adoptWraps, s.bin)
	})
}

// Undo restores the crontab as it was before the most recent change made
// through this Service. The restore is itself a safe write with a backup.
func (s *Service) Undo(ctx context.Context) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.undo) == 0 {
		return Result{}, ErrNothingToUndo
	}
	top := s.undo[len(s.undo)-1]
	if sha256.Sum256(s.store.Content()) != top.after {
		s.undo = nil
		return Result{}, ErrUndoStale
	}
	backup, err := s.store.Save(ctx, crontab.Parse(top.before))
	if errors.Is(err, crontab.ErrConflict) {
		return Result{}, &ConflictError{}
	}
	if err != nil {
		return Result{}, err
	}
	s.undo = s.undo[:len(s.undo)-1]
	return Result{Backup: backup}, nil
}

// CheckExternal reloads the crontab if it changed outside gocron and
// reports whether it did. Changes made before the reload can no longer be
// undone.
func (s *Service) CheckExternal(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed, err := s.store.Changed(ctx)
	if err != nil || !changed {
		return false, err
	}
	return true, s.reload(ctx)
}

// Reload re-reads the crontab, discarding the undo history.
func (s *Service) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reload(ctx)
}

func (s *Service) reload(ctx context.Context) error {
	s.undo = nil
	return s.store.Reload(ctx)
}

// apply runs op on a fresh parse of the loaded crontab and saves the result.
func (s *Service) apply(ctx context.Context, op func(*crontab.Crontab) (string, error)) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.store.Content()
	c := s.store.Crontab()
	id, err := op(c)
	if err != nil {
		return Result{}, err
	}
	backup, err := s.store.Save(ctx, c)
	if errors.Is(err, crontab.ErrConflict) {
		return Result{}, &ConflictError{Retry: func(ctx context.Context) (Result, error) {
			if err := s.Reload(ctx); err != nil {
				return Result{}, err
			}
			return s.apply(ctx, op)
		}}
	}
	if err != nil {
		return Result{ID: id, Backup: backup}, err
	}
	if backup != "" {
		s.undo = append(s.undo, undoEntry{before: before, after: sha256.Sum256(s.store.Content())})
		if len(s.undo) > maxUndo {
			s.undo = s.undo[1:]
		}
	}
	return Result{ID: id, Backup: backup}, nil
}
