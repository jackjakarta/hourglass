package jobs

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/cronexpr"
	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/humanize"
	svc "github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/tui/components"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

const (
	labelWidth = 11
	nextCount  = 10
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
	return components.Panel(m.th, title, m.detailLines(j, w-4), w, h, focused)
}

func (m Model) detailLines(j svc.Job, iw int) []string {
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
	}
	field("NEXT", m.nextLines(j)...)

	var notes []string
	for _, is := range cronexpr.Lint(j.Schedule) {
		st, glyph := th.Muted, theme.GlyphInfo
		if is.Severity >= cronexpr.Warn {
			st, glyph = th.Warn, theme.GlyphWarn
		}
		notes = append(notes, wrapPlain(st, glyph+" "+is.Message, iw)...)
	}
	if !j.Managed {
		notes = append(notes, wrapPlain(th.Muted, "Press a to adopt it: gocron adds a comment line above it and leaves the cron line unchanged.", iw)...)
	}
	if len(notes) > 0 {
		add("")
		add(notes...)
	}
	return out
}

func (m Model) nextLines(j svc.Job) []string {
	th := m.th
	switch {
	case j.Managed && !j.Enabled:
		return []string{th.Muted.Render("paused · ") + th.Key.Render("space") + th.Muted.Render(" resumes")}
	case j.Expr == nil:
		return []string{""}
	case j.Expr.Reboot:
		return []string{th.Text.Render("at boot")}
	}
	runs := j.NextRuns(m.now, nextCount)
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
	parts := []string{"runs via gocron exec"}
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
