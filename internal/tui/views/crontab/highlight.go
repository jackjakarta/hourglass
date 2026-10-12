// Package crontab is the Crontab view: the raw crontab with light syntax
// highlighting, and its revisions with diffs and one-key restore.
package crontab

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/tui/components"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// offPrefix starts the cron line of a paused job.
const offPrefix = "#gocron:off "

// Highlight renders a crontab's lines with line numbers: schedules in the
// schedule color, env names as keys, comments muted, gocron's meta lines
// dimmed, paused prefixes muted and invalid lines as failures.
func Highlight(th *theme.Theme, content []byte) []string {
	tab := crontab.Parse(content)
	digits := len(fmt.Sprint(len(tab.Lines)))
	var out []string
	for i, l := range tab.Lines {
		gutter := th.Muted.Render(fmt.Sprintf("%*d ", digits, i+1))
		raw := components.Sanitize(l.Raw)
		var s string
		switch {
		case l.Kind == crontab.Blank:
		case l.Kind == crontab.Invalid:
			s = th.Fail.Render(raw)
		case l.Kind == crontab.Comment && l.Err != nil, l.Orphan:
			s = th.Warn.Render(raw)
		case l.Kind == crontab.Comment:
			s = th.Muted.Render(raw)
		case l.Kind == crontab.JobMeta:
			s = th.Meta.Render(raw)
		case l.Kind == crontab.Env:
			name, value, _ := strings.Cut(raw, "=")
			s = th.Key.Render(name) + th.Muted.Render("=") + th.Text.Render(value)
		default: // Entry, JobLine
			s = entry(th, raw)
		}
		out = append(out, gutter+s)
	}
	return out
}

// entry splits a cron line into its paused prefix, schedule and command.
func entry(th *theme.Theme, raw string) string {
	var b strings.Builder
	if rest, ok := strings.CutPrefix(raw, offPrefix); ok {
		b.WriteString(th.Muted.Render(offPrefix))
		raw = rest
	}
	fields := 5
	trimmed := strings.TrimLeft(raw, " \t")
	if strings.HasPrefix(trimmed, "@") {
		fields = 1
	} else if f := strings.Fields(trimmed); len(f) > 0 && strings.Contains(f[0], "=") {
		fields = 6 // an inline CRON_TZ= prefix
	}
	end := fieldsEnd(raw, fields)
	b.WriteString(th.Schedule.Render(raw[:end]))
	cmd := raw[end:]
	// A wrapped command: the gocron exec part is plumbing, dimmed.
	if words := strings.Fields(cmd); len(words) > 1 && filepath.Base(words[0]) == "gocron" && words[1] == "exec" {
		if i := strings.Index(cmd, " -- "); i >= 0 {
			b.WriteString(th.Meta.Render(cmd[:i+4]) + th.Text.Render(cmd[i+4:]))
			return b.String()
		}
	}
	b.WriteString(th.Text.Render(cmd))
	return b.String()
}

// fieldsEnd is the index in s just after its first n space-separated fields.
func fieldsEnd(s string, n int) int {
	i := 0
	for range n {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
	}
	return i
}
