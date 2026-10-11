package main

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/humanize"
)

type jsonJob struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name,omitempty"`
	Desc        string   `json:"desc,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Schedule    string   `json:"schedule"`
	Description string   `json:"description"`
	Command     string   `json:"command"`
	Enabled     bool     `json:"enabled"`
	Managed     bool     `json:"managed"`
	Wrapped     bool     `json:"wrapped"`
	Timeout     string   `json:"timeout,omitempty"`
	NoOverlap   bool     `json:"no_overlap,omitempty"`
	TZ          string   `json:"tz,omitempty"`
	Line        int      `json:"line"`
	Next        *string  `json:"next"` // RFC 3339; null when paused, @reboot, or never
}

func (a *app) list(ctx context.Context, args []string) int {
	fs := a.flags("list", "list [--json] [--all]")
	asJSON := fs.Bool("json", false, "print JSON")
	all := fs.Bool("all", false, "include unmanaged crontab entries")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	src, err := a.source()
	if err != nil {
		return a.fail(err)
	}
	content, err := src.Read(ctx)
	if err != nil {
		return a.fail(err)
	}
	tab := crontab.Parse(content)
	now := a.now()

	var jobs []crontab.Job
	unmanaged := 0
	for _, j := range tab.Jobs() {
		if !j.Managed {
			unmanaged++
			if !*all {
				continue
			}
		}
		jobs = append(jobs, j)
	}
	if problems := tab.Problems(); len(problems) > 0 {
		fmt.Fprintf(a.stderr, "⚠ %d problem(s) in the crontab. Run gocron doctor for details.\n", len(problems))
	}

	if *asJSON {
		out := make([]jsonJob, 0, len(jobs))
		for _, j := range jobs {
			out = append(out, toJSON(j, now))
		}
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return a.fail(err)
		}
		return exitOK
	}

	if len(jobs) == 0 {
		fmt.Fprintf(a.stdout, "No gocron jobs in %s.\n", src.Name())
		if unmanaged > 0 {
			fmt.Fprintf(a.stdout, "It has %d unmanaged %s; see them with gocron list --all.\n", unmanaged, plural(unmanaged, "entry", "entries"))
		}
		return exitOK
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSCHEDULE\tWHEN\tNEXT\tSTATE")
	for _, j := range jobs {
		id, state := j.ID, "● enabled"
		switch {
		case !j.Managed:
			id, state = "-", "· unmanaged"
		case !j.Enabled:
			state = "○ paused"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", id, truncate(j.Title(), 32), j.Schedule, j.Describe(), nextLabel(j, now), state)
	}
	w.Flush()
	if !*all && unmanaged > 0 {
		fmt.Fprintf(a.stdout, "\n+ %d unmanaged %s (gocron list --all)\n", unmanaged, plural(unmanaged, "entry", "entries"))
	}
	return exitOK
}

func nextLabel(j crontab.Job, now time.Time) string {
	switch {
	case !j.Enabled:
		return "—"
	case j.Expr.Reboot:
		return "at boot"
	}
	next := j.NextRuns(now, 1)
	if len(next) == 0 {
		return "never"
	}
	return humanize.Next(next[0], now)
}

func toJSON(j crontab.Job, now time.Time) jsonJob {
	out := jsonJob{
		ID: j.ID, Name: j.Name, Desc: j.Desc, Tags: j.Tags, Schedule: j.Schedule, Description: j.Describe(),
		Command: j.Command, Enabled: j.Enabled, Managed: j.Managed, Wrapped: j.Wrapped,
		NoOverlap: j.NoOverlap, TZ: j.TZ, Line: j.Line,
	}
	if j.Timeout > 0 {
		out.Timeout = crontab.FormatDuration(j.Timeout)
	}
	if j.Enabled {
		if next := j.NextRuns(now, 1); len(next) > 0 {
			s := next[0].Format(time.RFC3339)
			out.Next = &s
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
