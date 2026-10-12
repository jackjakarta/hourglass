package jobs

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/jackjakarta/gocron/internal/crontab"
)

// Revision is a backup of the crontab, taken just before a save.
type Revision struct {
	Path    string
	At      time.Time // when the save it was taken for happened
	Content []byte
}

// Revisions returns the crontab's backups, newest first.
func (s *Service) Revisions(ctx context.Context) ([]Revision, error) {
	paths, err := s.store.Backups()
	if err != nil {
		return nil, err
	}
	out := make([]Revision, 0, len(paths))
	for _, p := range slices.Backward(paths) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue // pruned since it was listed
		}
		at, _ := crontab.BackupTime(p)
		out = append(out, Revision{Path: p, At: at, Content: b})
	}
	return out, nil
}

// Restore makes the backup at path the crontab again. Like every change it
// is a safe write with its own backup, so Undo reverts it.
func (s *Service) Restore(ctx context.Context, path string) (Result, error) {
	paths, err := s.store.Backups()
	if err != nil {
		return Result{}, err
	}
	if !slices.Contains(paths, path) {
		return Result{}, fmt.Errorf("%s is not one of gocron's backups", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	return s.apply(ctx, func(c *crontab.Crontab) (string, error) {
		*c = *crontab.Parse(content)
		return "", nil
	})
}

// Replace makes next the crontab, provided the loaded crontab is still
// base; otherwise it returns a ConflictError without a Retry. It's for
// whole-file edits, which mustn't be replayed over someone else's change.
func (s *Service) Replace(ctx context.Context, base, next []byte) (Result, error) {
	return s.applyOn(ctx, &base, func(c *crontab.Crontab) (string, error) {
		*c = *crontab.Parse(next)
		return "", nil
	})
}

// Disk reads the crontab as it is now, which may differ from the loaded
// one after an outside change.
func (s *Service) Disk(ctx context.Context) ([]byte, error) {
	return s.store.Source().Read(ctx)
}
