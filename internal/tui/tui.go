// Package tui is gocron's terminal UI.
package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jackjakarta/gocron/internal/doctor"
	"github.com/jackjakarta/gocron/internal/jobs"
)

// Clock tells the time. Views get the time from it, never from time.Now,
// so tests can freeze it.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Options configures the TUI.
type Options struct {
	Service *jobs.Service // nil when LoadErr is set
	LoadErr error         // the crontab couldn't be read; shown instead of jobs
	Clock   Clock         // default: the real clock
	// Daemon reports the cron daemon's status for the header; nil skips it.
	Daemon  func(context.Context) doctor.DaemonStatus
	NoColor bool // NO_COLOR is set: use the mono theme
	// UserPATH is the user's own PATH. The editor uses it to say where a
	// program cron can't find lives.
	UserPATH string
	// PathLint replaces the editor's PATH check (tests); nil uses
	// jobs.LintPath.
	PathLint func(cmd string, env jobs.CronEnv) []jobs.Issue
}

// Run starts the TUI and blocks until it quits.
func Run(ctx context.Context, opts Options) error {
	_, err := tea.NewProgram(New(ctx, opts), tea.WithContext(ctx)).Run()
	return err
}
