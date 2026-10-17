package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/jackjakarta/hourglass/internal/tui/components"
	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

// diffOverlay shows what changed outside hourglass, over the conflict dialog
// it came from.
type diffOverlay struct {
	lines  []string
	offset int
	back   *components.Confirm // the dialog to return to
}

// showDiffMsg carries the outside change for the overlay.
type showDiffMsg struct {
	lines []string
	back  *components.Confirm
	err   error
}

// viewDiff reads the crontab on disk and diffs it against the one hourglass
// loaded, then shows it over back.
func (m Model) viewDiff(back *components.Confirm) tea.Cmd {
	svc, ctx, loaded, th := m.opts.Service, m.ctx, m.snap.Content, m.th
	return func() tea.Msg {
		disk, err := svc.Disk(ctx)
		if err != nil {
			return showDiffMsg{back: back, err: err}
		}
		d := components.UnifiedDiff("as hourglass loaded it", "on disk now", loaded, disk)
		if d == "" {
			return showDiffMsg{back: back, lines: []string{th.Muted.Render("The crontab on disk is the same as hourglass's copy now.")}}
		}
		return showDiffMsg{back: back, lines: components.DiffLines(th, d)}
	}
}

func (d *diffOverlay) rows(h int) int { return max(h-8, 3) }

func (m Model) updateDiff(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	km := m.keys
	d := *m.diff
	rows := d.rows(m.height)
	switch {
	case key.Matches(msg, km.Back, km.Quit, km.Open):
		m.confirm, m.diff = d.back, nil
		return m, nil
	case key.Matches(msg, km.Up):
		d.offset--
	case key.Matches(msg, km.Down):
		d.offset++
	case key.Matches(msg, km.PageUp):
		d.offset -= rows - 1
	case key.Matches(msg, km.PageDown):
		d.offset += rows - 1
	case key.Matches(msg, km.Top):
		d.offset = 0
	case key.Matches(msg, km.Bottom):
		d.offset = len(d.lines)
	}
	d.offset = max(min(d.offset, len(d.lines)-rows), 0)
	m.diff = &d
	return m, nil
}

func (d *diffOverlay) view(th *theme.Theme, w, h int) string {
	rows := d.rows(h)
	end := min(d.offset+rows, len(d.lines))
	body := append([]string(nil), d.lines[d.offset:end]...)
	body = append(body, "", th.Muted.Render("esc goes back to the question"))
	return components.Panel(th, "The outside change", body, min(w-8, 110), len(body)+2, true)
}
