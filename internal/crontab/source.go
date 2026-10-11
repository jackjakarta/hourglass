package crontab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Source reads and writes a crontab.
type Source interface {
	// Read returns the crontab; empty and nil if the user has none.
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, content []byte) error
	Name() string // "user crontab" | "file: /path"
}

// EnvCrontabFile names a plain file to use instead of the real crontab.
const EnvCrontabFile = "GOCRON_CRONTAB_FILE"

// EnvAllowSystem must be "1" for tests to construct a SystemSource.
const EnvAllowSystem = "GOCRON_ALLOW_SYSTEM_CRONTAB"

// SourceFromEnv returns a FileSource when GOCRON_CRONTAB_FILE is set, and
// the user's real crontab otherwise.
func SourceFromEnv() (Source, error) {
	if p := os.Getenv(EnvCrontabFile); p != "" {
		return FileSource{Path: p}, nil
	}
	return NewSystemSource()
}

// FileSource is a crontab stored in a plain file.
type FileSource struct{ Path string }

func (f FileSource) Name() string { return "file: " + f.Path }

func (f FileSource) Read(context.Context) ([]byte, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// Write replaces the file atomically: temp file, fsync, rename.
func (f FileSource) Write(_ context.Context, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), ".gocron-crontab-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.Path)
}

// SystemSource is the user's crontab, via the crontab(1) command.
type SystemSource struct {
	Bin string // "crontab" unless overridden
}

// NewSystemSource returns the real crontab. Under "go test" it refuses
// unless GOCRON_ALLOW_SYSTEM_CRONTAB=1, so no test touches a real crontab
// by accident.
func NewSystemSource() (*SystemSource, error) {
	if testing.Testing() && os.Getenv(EnvAllowSystem) != "1" {
		return nil, errors.New("refusing to use the real crontab in tests: set " + EnvCrontabFile + " (or " + EnvAllowSystem + "=1)")
	}
	return &SystemSource{Bin: "crontab"}, nil
}

func (s *SystemSource) Name() string { return "user crontab" }

func (s *SystemSource) Read(ctx context.Context) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, s.Bin, "-l")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.Contains(strings.ToLower(msg), "no crontab for") {
			return nil, nil
		}
		return nil, commandError("crontab -l", msg, err)
	}
	return stdout.Bytes(), nil
}

func (s *SystemSource) Write(ctx context.Context, content []byte) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, s.Bin, "-")
	cmd.Stdin, cmd.Stderr = bytes.NewReader(content), &stderr
	if err := cmd.Run(); err != nil {
		return commandError("crontab -", strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

func commandError(what, stderr string, err error) error {
	if stderr != "" {
		return fmt.Errorf("%s: %s (%w)", what, stderr, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}
