package jobs

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/humanize"
	"github.com/jackjakarta/gocron/internal/tui/components"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// View renders the view into its width × height.
func (m Model) View() string {
	w, h := m.width, m.height
	if len(m.snap.Jobs) == 0 {
		return m.emptyView(w, h)
	}
	layout := LayoutFor(w)
	if m.detail {
		return m.detailPanel(w, h, true)
	}
	if layout != LayoutSplit {
		return m.listPanel(w, h, layout)
	}
	lw := w * 45 / 100
	return lipgloss.JoinHorizontal(lipgloss.Top, m.listPanel(lw, h, layout), " ", m.detailPanel(w-lw-1, h, false))
}

func (m Model) listTitle() string {
	managed := 0
	for _, j := range m.snap.Jobs {
		if j.Managed {
			managed++
		}
	}
	return fmt.Sprintf("Jobs · %d", managed)
}

// listRows is how many lines the list can show below the filter line.
func (m Model) listRows() int {
	n := m.height - 2 - 2 // borders, filter line + gap
	if len(m.snap.Problems) > 0 {
		n -= 2
	}
	return max(n, 1)
}

// lineOf returns the line (below the filter) where row i is drawn, and the
// total number of lines.
func (m Model) lineOf(i int) (line, total int) {
	gap := 0
	if len(m.unmanaged) > 0 {
		gap = 1 // section header
		if len(m.managed) > 0 {
			gap = 2 // blank + header
		}
	}
	total = len(m.managed) + len(m.unmanaged) + gap
	if i < len(m.managed) {
		return i, total
	}
	return i + gap, total
}

func (m *Model) clampOffset() {
	vis := m.listRows()
	i := m.index(m.rows())
	if i < 0 {
		m.offset = 0
		return
	}
	line, total := m.lineOf(i)
	top := line
	if i == len(m.managed) {
		top = len(m.managed) // show the section header with the first unmanaged row
	}
	if top < m.offset {
		m.offset = top
	}
	if line >= m.offset+vis {
		m.offset = line - vis + 1
	}
	m.offset = max(min(m.offset, total-vis), 0)
}

func (m Model) listPanel(w, h int, layout Layout) string {
	iw := w - 4
	th := m.th
	var lines []string

	// Filter line.
	switch {
	case m.filtering:
		lines = append(lines, th.AccentText.Render("/ ")+th.Text.Render(m.filter)+th.AccentText.Render("█"))
	case m.filter != "":
		lines = append(lines, th.AccentText.Render("/ ")+th.Text.Render(m.filter)+th.Muted.Render("  · esc clears"))
	default:
		lines = append(lines, th.Muted.Render("/ filter…"))
	}
	lines = append(lines, "")

	rows := m.rows()
	sel := m.index(rows)
	var body []string
	switch {
	case len(rows) == 0:
		body = append(body, th.Muted.Render(fmt.Sprintf("No jobs match %q.", m.filter)))
	default:
		cols := m.columns(iw, layout)
		for i, r := range m.managed {
			body = append(body, m.managedRow(r, cols, i == sel))
		}
		if len(m.unmanaged) > 0 {
			if len(m.managed) > 0 {
				body = append(body, "")
			}
			body = append(body, th.Label.Render(fmt.Sprintf("UNMANAGED · %d", len(m.unmanaged))))
			for i, r := range m.unmanaged {
				body = append(body, m.unmanagedRow(r, iw, len(m.managed)+i == sel))
			}
		}
	}
	vis := m.listRows()
	end := min(m.offset+vis, len(body))
	if m.offset < end {
		body = body[m.offset:end]
	}
	lines = append(lines, body...)

	if n := len(m.snap.Problems); n > 0 {
		for len(lines) < h-4 {
			lines = append(lines, "")
		}
		lines = append(lines, "", th.Warn.Render(fmt.Sprintf("%s %d %s in the crontab · run gocron doctor", theme.GlyphWarn, n, plural(n, "problem"))))
	}
	return components.Panel(th, m.listTitle(), lines, w, h, true)
}

type columns struct {
	compact                 bool
	width, name, sched, nxt int
}

// columns sizes the row columns to their content; the name takes what's
// left, up to the longest name.
func (m Model) columns(iw int, layout Layout) columns {
	c := columns{compact: layout == LayoutCompact, width: iw}
	longest := 0
	for _, r := range m.managed {
		c.nxt = max(c.nxt, lipgloss.Width(m.nextLabel(r)))
		c.sched = max(c.sched, lipgloss.Width(r.job.Expr.Short()))
		longest = max(longest, lipgloss.Width(r.job.Title()))
	}
	const fixed = 3 // bar, dot, space
	if c.compact {
		c.sched = 0
		c.name = min(max(iw-fixed-2-c.nxt, 4), longest)
		return c
	}
	c.sched = min(c.sched, max(iw*35/100, 12))
	avail := iw - fixed - 2 - c.sched - 2 - c.nxt
	if avail < 12 {
		c.sched = max(c.sched-(12-avail), 6)
		avail = max(iw-fixed-2-c.sched-2-c.nxt, 4)
	}
	c.name = min(avail, max(longest, 12))
	return c
}

func (m Model) nextLabel(r row) string {
	j := r.job
	switch {
	case !j.Enabled:
		return "paused"
	case j.Expr == nil:
		return ""
	case j.Expr.Reboot:
		return "at boot"
	case r.next.IsZero():
		return "never"
	}
	return humanize.Next(r.next, m.now)
}

func (m Model) managedRow(r row, c columns, selected bool) string {
	th := m.th
	paint := func(st lipgloss.Style, s string) string {
		if selected {
			st = st.Inherit(th.Selected)
		}
		return st.Render(s)
	}
	bar := " "
	if selected {
		bar = th.SelectionBar.Render(theme.GlyphBar)
	}
	dot, dotStyle := theme.GlyphEnabled, th.OK
	nameStyle, nextStyle := th.Text, th.Text
	if !r.job.Enabled {
		dot, dotStyle, nameStyle, nextStyle = theme.GlyphPaused, th.Muted, th.Muted, th.Muted
	} else if r.next.IsZero() {
		nextStyle = th.Muted
	}
	if selected {
		nameStyle = nameStyle.Bold(true)
	}
	s := bar + paint(dotStyle, dot) + paint(th.Text, " ") + paint(nameStyle, components.Fit(r.job.Title(), c.name)) + paint(th.Text, "  ")
	if !c.compact {
		s += paint(th.Schedule, components.Fit(r.job.Expr.Short(), c.sched)) + paint(th.Text, "  ")
	}
	s += paint(nextStyle, components.Fit(m.nextLabel(r), c.nxt))
	return s + paint(th.Text, strings.Repeat(" ", max(c.width-lipgloss.Width(s), 0)))
}

func (m Model) unmanagedRow(r row, iw int, selected bool) string {
	th := m.th
	paint := func(st lipgloss.Style, s string) string {
		if selected {
			st = st.Inherit(th.Selected)
		}
		return st.Render(s)
	}
	bar := " "
	hint := ""
	if selected {
		bar = th.SelectionBar.Render(theme.GlyphBar)
		hint = "  " + th.Key.Render("a") + " " + th.Muted.Render("adopt")
	}
	text := r.job.Schedule + " " + r.job.Command
	w := iw - 3 - lipgloss.Width(hint)
	return bar + paint(th.Muted, theme.GlyphInfo+" ") + paint(th.Muted, components.Fit(text, w)) + paint(th.Text, hint)
}

func (m Model) emptyView(w, h int) string {
	th := m.th
	if LayoutFor(w) == LayoutCompact {
		w = max(w, 20)
	}
	lines := []string{
		"",
		th.Title.Render("Welcome to gocron"),
		"",
		th.Text.Render("There are no cron jobs in your crontab (" + m.snap.Source + ") yet."),
		"",
		th.Text.Render("gocron keeps jobs in your real crontab, so they run whether or not"),
		th.Text.Render("gocron is open. Each job is a comment line plus a cron line:"),
		"",
		"  " + th.Muted.Render(`# gocron:job id=k3f9a2 name="Nightly backup"`),
		"  " + th.Schedule.Render("30 2 * * *") + " " + th.Text.Render("pg_dump app > /bk/app.sql"),
		"",
		th.Text.Render("Press ") + th.Key.Render("n") + th.Text.Render(" to create your first job."),
	}
	return components.Panel(th, "Jobs", lines, w, h, true)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
