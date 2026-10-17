package crontab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrConflict means the crontab changed since it was loaded.
var ErrConflict = errors.New("crontab changed outside hourglass")

// VerifyError means the crontab read back after a write didn't match what
// was written.
type VerifyError struct{ BackupPath string }

func (e *VerifyError) Error() string {
	return "crontab didn't read back as written; the previous version is saved at " + e.BackupPath
}

// Options configures a Store.
type Options struct {
	BackupDir string           // required
	Keep      int              // backups to keep; default 50
	Now       func() time.Time // default time.Now
}

const (
	backupPrefix = "crontab-"
	backupLayout = "2006-01-02T15-04-05.000000000Z"
)

// BackupTime returns when the backup at path was taken: the time of the
// save it was made for.
func BackupTime(path string) (time.Time, bool) {
	name, ok := strings.CutPrefix(filepath.Base(path), backupPrefix)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(backupLayout, name)
	return t, err == nil
}

// Store loads a crontab and saves changes to it safely.
type Store struct {
	src     Source
	opts    Options
	content []byte
	hash    [32]byte
}

// Open loads the crontab from src.
func Open(ctx context.Context, src Source, opts Options) (*Store, error) {
	if opts.BackupDir == "" {
		return nil, errors.New("crontab store: BackupDir is required")
	}
	if opts.Keep <= 0 {
		opts.Keep = 50
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Store{src: src, opts: opts}
	return s, s.Reload(ctx)
}

// Reload re-reads the crontab, discarding the loaded state.
func (s *Store) Reload(ctx context.Context) error {
	b, err := s.src.Read(ctx)
	if err != nil {
		return err
	}
	s.content, s.hash = b, sha256.Sum256(b)
	return nil
}

// Source returns the store's source.
func (s *Store) Source() Source { return s.src }

// Content returns the crontab bytes as loaded.
func (s *Store) Content() []byte { return bytes.Clone(s.content) }

// Crontab returns a fresh parse of the loaded crontab. Callers may mutate
// it and pass it to Save.
func (s *Store) Crontab() *Crontab { return Parse(s.content) }

// Changed reports whether the crontab differs from the loaded state.
func (s *Store) Changed(ctx context.Context) (bool, error) {
	b, err := s.src.Read(ctx)
	if err != nil {
		return false, err
	}
	return sha256.Sum256(b) != s.hash, nil
}

// Save writes c with the safe-write protocol:
//  1. re-read and compare with the loaded hash (ErrConflict on change);
//  2. back up the current content;
//  3. write the new content;
//  4. read it back and verify it (VerifyError on mismatch);
//  5. adopt it as the loaded state;
//  6. prune old backups.
//
// It returns the backup path, or "" if c is unchanged and nothing was written.
func (s *Store) Save(ctx context.Context, c *Crontab) (string, error) {
	next := c.Serialize()
	if len(next) > 0 && next[len(next)-1] != '\n' {
		next = append(next, '\n') // crontab(1) rejects a final line without a newline
	}

	current, err := s.src.Read(ctx)
	if err != nil {
		return "", err
	}
	if sha256.Sum256(current) != s.hash {
		return "", ErrConflict
	}
	if bytes.Equal(next, current) {
		return "", nil
	}

	backup, err := s.backup(current)
	if err != nil {
		return "", fmt.Errorf("backing up crontab: %w", err)
	}
	if err := s.src.Write(ctx, next); err != nil {
		return backup, fmt.Errorf("writing crontab (previous version saved at %s): %w", backup, err)
	}
	got, err := s.src.Read(ctx)
	if err != nil || !bytes.Equal(got, next) {
		return backup, &VerifyError{BackupPath: backup}
	}
	s.content, s.hash = next, sha256.Sum256(next)
	if err := s.prune(); err != nil {
		return backup, fmt.Errorf("pruning backups: %w", err)
	}
	return backup, nil
}

func (s *Store) backup(content []byte) (string, error) {
	if err := os.MkdirAll(s.opts.BackupDir, 0o700); err != nil {
		return "", err
	}
	now := s.opts.Now().UTC()
	for {
		name := filepath.Join(s.opts.BackupDir, backupPrefix+now.Format(backupLayout))
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			now = now.Add(time.Nanosecond)
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(content); err != nil {
			f.Close()
			return "", err
		}
		return name, f.Close()
	}
}

// Backups returns backup paths, oldest first.
func (s *Store) Backups() ([]string, error) {
	entries, err := os.ReadDir(s.opts.BackupDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), backupPrefix) {
			out = append(out, filepath.Join(s.opts.BackupDir, e.Name()))
		}
	}
	sort.Strings(out) // fixed-width UTC timestamps sort chronologically
	return out, nil
}

func (s *Store) prune() error {
	all, err := s.Backups()
	if err != nil {
		return err
	}
	for len(all) > s.opts.Keep {
		if err := os.Remove(all[0]); err != nil {
			return err
		}
		all = all[1:]
	}
	return nil
}
