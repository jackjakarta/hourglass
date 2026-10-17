package jobs

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/hourglass/internal/cronexpr"
	"github.com/jackjakarta/hourglass/internal/crontab"
	"github.com/jackjakarta/hourglass/internal/humanize"
	svc "github.com/jackjakarta/hourglass/internal/jobs"
	"github.com/jackjakarta/hourglass/internal/store"
	"github.com/jackjakarta/hourglass/internal/tui/components"
	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

const (
	labelWidth = 11
	nextCount  = 10 // next runs shown when there's room
	nextMin    = 3
)

func (m Model) detailPanel(w, h int, focused bool) string {
	j, ok := m.Selected()
	if !ok {
		return components.Panel(m.th, "", []string{"", m.th.Muted.Render("Nothing selected.")}, w, h, focused)
	}
	title := j.Title()
	if !j.Managed {
		title = "Unmanaged entry"
	}
	return components.Panel(m.th, title, m.detailLines(j, w-4, h-2), w, h, focused)
}

func (m Model) detailLines(j svc.Job, iw, ih int) []string {
	th := m.th
	var out []string
	add := func(lines ...string) { out = append(out, lines...) }
	field := func(label string, values ...string) {
		for i, v := range values {
			l := ""
			if i == 0 {
				l = label
			}
			add(th.Label.Render(components.Fit(l, labelWidth)) + v)
		}
	}
	vw := max(iw-labelWidth, 10)
	wrap := func(st lipgloss.Style, s string) []string { return wrapPlain(st, s, vw) }

	// Status line.
	var status []string
	switch {
	case !j.Managed:
		status = append(status, th.Muted.Render(theme.GlyphInfo+" unmanaged"))
	case j.Enabled:
		status = append(status, th.OK.Render(theme.GlyphEnabled)+th.Text.Render(" enabled"))
	default:
		status = append(status, th.Muted.Render(theme.GlyphPaused+" paused"))
	}
	if len(j.Tags) > 0 {
		status = append(status, th.Schedule.Render("#"+strings.Join(j.Tags, " #")))
	}
	if j.Managed {
		status = append(status, th.Muted.Render(j.ID))
	} else {
		status = append(status, th.Muted.Render(fmt.Sprintf("line %d", j.Line)))
	}
	add(strings.Join(status, th.Muted.Render(" · ")))
	if j.Desc != "" {
		add(wrapPlain(th.Muted, j.Desc, iw)...)
	}
	add("")

	sched := th.Schedule.Render(j.Schedule)
	if j.TZ != "" && j.Expr != nil && j.Expr.TZ == nil {
		sched += th.Muted.Render("  CRON_TZ=" + j.TZ)
	}
	field("SCHEDULE", append([]string{sched}, wrap(th.Text, j.Describe())...)...)
	add("")
	field("COMMAND", wrap(th.Text, j.Command)...)
	add("")
	if j.Managed {
		field("TRACKING", th.Text.Render(tracking(j.Job)))
		add("")
		if recent := m.recentLines(j, vw); len(recent) > 0 {
			field("RECENT", recent...)
			add("")
		}
	}
	nextAt := len(out)

	var notes []string
	for _, is := range cronexpr.Lint(j.Schedule) {
		st, glyph := th.Muted, theme.GlyphInfo
		if is.Severity >= cronexpr.Warn {
			st, glyph = th.Warn, theme.GlyphWarn
		}
		notes = append(notes, wrapPlain(st, glyph+" "+is.Message, iw)...)
	}
	if !j.Managed {
		notes = append(notes, wrapPlain(th.Muted, "Press a to adopt it: hourglass adds a comment line above it and leaves the cron line unchanged.", iw)...)
	}
	if len(notes) > 0 {
		add("")
		add(notes...)
	}
	// NEXT gives way to everything else on a short screen.
	n := min(max(ih-len(out), nextMin), nextCount)
	var next []string
	for i, v := range m.nextLines(j, n) {
		l := ""
		if i == 0 {
			l = "NEXT"
		}
		next = append(next, th.Label.Render(components.Fit(l, labelWidth))+v)
	}
	return append(out[:nextAt], append(next, out[nextAt:]...)...)
}

// recentLines summarizes the job's recent runs: the run strip, then
// durations and the last result.
func (m Model) recentLines(j svc.Job, vw int) []string {
	th := m.th
	st, ok := m.stats[j.ID]
	var out []string
	if since, running := m.running[j.ID]; running {
		out = append(out, th.Warn.Render(theme.GlyphRunning+" running")+th.Muted.Render(" · "+components.Elapsed(m.now.Sub(since))))
	}
	if !ok || len(st.Recent) == 0 {
		switch {
		case !j.Wrapped:
			out = append(out, wrapPlain(th.Muted, "cron runs aren't tracked · e, then Track runs, to keep their history", vw)...)
		case len(out) == 0:
			out = append(out, th.Muted.Render("no runs yet · r runs it now"))
		}
		return out
	}
	strip := components.RunStrip(th, st.Recent)
	if st.Counted > 0 {
		strip += th.Muted.Render(fmt.Sprintf("  %d/%d ok", st.OK, st.Counted))
	}
	out = append(out, strip)

	var durs []time.Duration
	for _, r := range st.Recent {
		if r.EndedAt != nil && r.Status != store.StatusSkipped {
			durs = append(durs, r.EndedAt.Sub(r.StartedAt))
		}
	}
	var parts []string
	if len(durs) > 0 {
		parts = append(parts, "avg "+humanize.Duration(st.AvgDur))
	}
	line := ""
	if len(durs) > 1 {
		line = components.Sparkline(th, durs[max(len(durs)-10, 0):]) + "  "
	}
	result := ""
	if last := st.Last; last != nil {
		glyph, style := components.StatusGlyph(th, last.Status)
		parts = append(parts, "last "+humanize.Duration(m.now.Sub(last.StartedAt))+" ago")
		result = " " + style.Render(glyph)
		if last.ExitCode != nil && *last.ExitCode != 0 {
			result += style.Render(fmt.Sprintf(" exit %d", *last.ExitCode))
		}
	}
	out = append(out, line+th.Muted.Render(strings.Join(parts, " · "))+result)
	if !j.Wrapped {
		out = append(out, wrapPlain(th.Muted, "only runs started from hourglass · cron runs aren't tracked", vw)...)
	}
	return out
}

func (m Model) nextLines(j svc.Job, count int) []string {
	th := m.th
	switch {
	case j.Managed && !j.Enabled:
		return []string{th.Muted.Render("paused · ") + th.Key.Render("space") + th.Muted.Render(" resumes")}
	case j.Expr == nil:
		return []string{""}
	case j.Expr.Reboot:
		return []string{th.Text.Render("at boot")}
	}
	runs := j.NextRuns(m.now, count)
	if len(runs) == 0 {
		return []string{th.Muted.Render("never: no date matches this schedule")}
	}
	var out []string
	for _, t := range runs {
		rel := "now"
		if d := t.Sub(m.now); d >= time.Second {
			rel = "in " + humanize.Duration(d)
		}
		out = append(out, th.Text.Render(components.Fit(humanize.Absolute(t, m.now), 18))+th.Muted.Render(rel))
	}
	return out
}

func tracking(j crontab.Job) string {
	if !j.Wrapped {
		return "not tracked (plain cron line)"
	}
	parts := []string{"runs via hourglass exec"}
	if j.Timeout > 0 {
		parts = append(parts, "timeout "+crontab.FormatDuration(j.Timeout))
	}
	if j.NoOverlap {
		parts = append(parts, "skips if still running")
	}
	return strings.Join(parts, " · ")
}

func wrapPlain(st lipgloss.Style, s string, w int) []string {
	var out []string
	for _, l := range strings.Split(lipgloss.NewStyle().Width(w).Render(s), "\n") {
		out = append(out, st.Render(strings.TrimRight(l, " ")))
	}
	return out
}
