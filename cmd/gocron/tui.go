package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/doctor"
	"github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/runner"
	"github.com/jackjakarta/gocron/internal/tui"
)

func (a *app) tui(ctx context.Context) int {
	paths, cfg, err := a.setup()
	if err != nil {
		return a.fail(err)
	}
	src, err := a.source()
	if err != nil {
		return a.fail(err)
	}
	opts := tui.Options{
		NoColor:  os.Getenv("NO_COLOR") != "",
		UI:       cfg.UI,
		UserPATH: os.Getenv("PATH"),
		Daemon: func(ctx context.Context) doctor.DaemonStatus {
			return doctor.DetectDaemon(ctx, doctor.DefaultEnv(src))
		},
	}
	store, err := crontab.Open(ctx, src, crontab.Options{BackupDir: paths.BackupsDir, Keep: cfg.Backups.Keep})
	if err != nil {
		opts.LoadErr = err // shown in the TUI, which stays read-only
	} else {
		bin := a.binPath()
		opts.Service = jobs.New(store, jobs.Options{
			Bin:      bin,
			Defaults: cfg.Defaults,
			OpenRuns: openRuns(paths),
			Launcher: runner.SpawnLauncher{Bin: bin, Paths: paths},
		})
	}
	if err := tui.Run(ctx, opts); err != nil {
		return a.fail(err)
	}
	return exitOK
}

// selfPath is the running gocron binary, symlinks resolved.
func selfPath() string {
	p, err := os.Executable()
	if err != nil {
		return "gocron"
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
