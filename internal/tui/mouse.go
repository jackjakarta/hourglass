package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// headerLead is the start of the header, before the tabs.
var headerLead = " " + theme.GlyphClock + " gocron" + "   "

// tabGap separates the header's tabs.
const tabGap = "  "

// tabAt returns the tab drawn at column x of the header.
func tabAt(x int) (tab, bool) {
	pos := lipgloss.Width(headerLead)
	for i, name := range tabNames {
		w := lipgloss.Width(name)
		if x >= pos && x < pos+w {
			return tab(i), true
		}
		pos += w + lipgloss.Width(tabGap)
	}
	return 0, false
}

// click handles a left click. Overlays take clicks first, in the order
// they take keys; a click outside the palette closes it.
func (m Model) click(x, y int) (tea.Model, tea.Cmd) {
	w, h := m.width, m.height
	switch {
	case w < minWidth || h < minHeight:
		return m, nil
	case m.palette != nil:
		box := m.palette.p.View(m.th, w-8)
		x0, y0 := (w-lipgloss.Width(box))/2, 2
		if x < x0 || x >= x0+lipgloss.Width(box) || y < y0 || y >= y0+lipgloss.Height(box) {
			m.palette = nil
			return m, nil
		}
		chosen, done := m.palette.p.Click(x-x0, y-y0)
		if !done {
			return m, nil
		}
		actions := m.palette.actions
		m.palette = nil
		return actions[chosen].run(m)
	case m.diff != nil:
		return m, nil
	case m.confirm != nil:
		box := m.confirm.View(m.th)
		x0, y0 := (w-lipgloss.Width(box))/2, (h-lipgloss.Height(box))/2
		done, cmd := m.confirm.Click(m.th, x-x0, y-y0)
		if done {
			m.confirm = nil
		}
		return m, cmd
	case m.paneID != 0, m.editor != nil:
		return m, nil
	case m.help:
		m.help = false
		return m, nil
	case y == 0:
		if t, ok := tabAt(x); ok {
			return m.switchTab(t)
		}
		return m, nil
	}
	by := y - 2 // the body starts below the header and a blank line
	if by < 0 || by >= m.bodyHeight() || m.opts.Service == nil || !m.loaded {
		return m, nil
	}
	var req tea.Msg
	switch m.tab {
	case tabJobs:
		m.jobs, req = m.jobs.Click(x, by)
	case tabHistory:
		m.history, req = m.history.Click(x, by)
	case tabTimeline:
		m.timeline, req = m.timeline.Click(x, by)
	case tabCrontab:
		m.crontab, req = m.crontab.Click(x, by)
	}
	if req == nil {
		return m, nil
	}
	return m.Update(req)
}

// wheel scrolls whatever is under the pointer by delta steps (negative
// is up).
func (m Model) wheel(x, y, delta int) (tea.Model, tea.Cmd) {
	switch {
	case m.palette != nil:
		m.palette.p.Wheel(delta)
		return m, nil
	case m.diff != nil, m.confirm != nil, m.editor != nil, m.help:
		return m, nil
	case m.paneID != 0:
		if s, ok := m.sessions[m.paneID]; ok {
			s.pane = s.pane.Wheel(delta)
		}
		return m, nil
	}
	if by := y - 2; by < 0 || by >= m.bodyHeight() || m.opts.Service == nil || !m.loaded {
		return m, nil
	}
	switch m.tab {
	case tabJobs:
		m.jobs = m.jobs.Wheel(delta)
	case tabHistory:
		m.history = m.history.Wheel(delta)
	case tabTimeline:
		m.timeline = m.timeline.Wheel(delta)
	case tabCrontab:
		m.crontab = m.crontab.Wheel(x, delta)
	}
	return m, nil
}
