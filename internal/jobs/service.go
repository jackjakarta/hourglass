// Package jobs is the façade the TUI and CLI use to read and change cron
// jobs. Every change goes through crontab.Store's safe-write path and can
// be undone.
package jobs

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"

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
	Source   string // "user crontab" | "file: /path"
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
