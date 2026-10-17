package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/jackjakarta/hourglass/internal/humanize"
	runs "github.com/jackjakarta/hourglass/internal/store"
)

func (a *app) logs(ctx context.Context, args []string) int {
	fs := a.flags("logs", "logs [-n N] [--run R|last] ID")
	n := fs.Int("n", 20, "how many recent runs to list")
	runFlag := fs.String("run", "", `print the output of run R ("last" for the newest)`)
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 || *n < 1 {
		fs.Usage()
		return exitUsage
	}
	id := fs.Arg(0)
	paths, _, err := a.setup()
	if err != nil {
		return a.fail(err)
	}
	db, err := openRuns(paths)(ctx)
	if err != nil {
		return a.fail(err)
	}
	defer db.Close()

	if *runFlag != "" {
		return a.printLog(ctx, db, id, *runFlag)
	}
	list, err := db.List(ctx, runs.Filter{JobIDs: []string{id}, Limit: *n})
	if err != nil {
		return a.fail(err)
	}
	if len(list) == 0 {
		fmt.Fprintf(a.stdout, "No runs of %s yet.\n", id)
		return exitOK
	}
	now := a.now()
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUN\tSTARTED\tTRIGGER\tSTATUS\tEXIT\tDURATION\tOUTPUT")
	for _, r := range list {
		exit, dur := "—", "—"
		if r.ExitCode != nil {
			exit = strconv.Itoa(*r.ExitCode)
		}
		if d := r.Duration(now); r.Status != runs.StatusSkipped && (d > 0 || r.EndedAt != nil) {
			dur = humanize.Duration(d)
		}
		out := humanize.Bytes(r.LogBytes)
		if r.Truncated {
			out += " (truncated)"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s %s\t%s\t%s\t%s\n", r.ID, humanize.Absolute(r.StartedAt, now), r.Trigger,
			statusGlyph(r.Status), r.Status, exit, dur, out)
	}
	w.Flush()
	return exitOK
}

func (a *app) printLog(ctx context.Context, db *runs.DB, id, which string) int {
	var r runs.Run
	if which == "last" {
		list, err := db.List(ctx, runs.Filter{JobIDs: []string{id}, Limit: 1})
		if err != nil {
			return a.fail(err)
		}
		if len(list) == 0 {
			fmt.Fprintf(a.stderr, "hourglass: no runs of %s yet\n", id)
			return exitError
		}
		r = list[0]
	} else {
		runID, err := strconv.ParseInt(which, 10, 64)
		if err != nil {
			fmt.Fprintf(a.stderr, "hourglass: --run takes a run number or \"last\", not %q\n", which)
			return exitUsage
		}
		if r, err = db.Get(ctx, runID); err != nil || r.JobID != id {
			fmt.Fprintf(a.stderr, "hourglass: %s has no run %d (hourglass logs %s lists them)\n", id, runID, id)
			return exitError
		}
	}
	f, err := os.Open(r.LogPath)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(a.stderr, "hourglass: run %d has no saved output\n", r.ID)
		return exitOK
	}
	if err != nil {
		return a.fail(err)
	}
	defer f.Close()
	if _, err := io.Copy(a.stdout, f); err != nil {
		return a.fail(err)
	}
	return exitOK
}

func statusGlyph(status string) string {
	switch status {
	case runs.StatusSuccess:
		return "✓"
	case runs.StatusFailed, runs.StatusError:
		return "✗"
	case runs.StatusTimeout:
		return "⏱"
	case runs.StatusSkipped:
		return "↷"
	case runs.StatusRunning:
		return "◐"
	}
	return "?"
}
