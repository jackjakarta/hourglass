package crontab

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/humanize"
	"github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/tui/components"
	"github.com/jackjakarta/gocron/internal/tui/keys"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// Requests the view returns from Update.
type (
	RestoreMsg struct{ Rev jobs.Revision }
	EditMsg    struct{} // open the crontab in $EDITOR
)

// Model is the Crontab view.
type Model struct {
	th   *theme.Theme
	keys keys.Map
	now  time.Time

	content   []byte // the crontab as loaded
	source    string
	revs      []jobs.Revision // newest first
	summaries []string        // what changed after each revision
	err       error
	loaded    bool

	selected int  // 0 is the current crontab, i is revs[i-1]
	offset   int  // first visible list row
	scroll   int  // first visible preview line
	full     bool // show a revision's text instead of its diff
	preview  bool // narrow layouts: the preview fills the view

	lines []string // the preview, rendered

	width, height int
}

// New returns an empty Crontab view.
func New(th *theme.Theme, km keys.Map) Model { return Model{th: th, keys: km} }

// SetTheme swaps the theme.
func (m *Model) SetTheme(th *theme.Theme) {
	m.th = th
	m.refresh()
}

// SetSize sets the area the view draws into.
func (m *Model) SetSize(w, h int) {
	m.width, m.height = w, h
	if m.wide() {
		m.preview = false
	}
	m.clamp()
}

// SetNow sets the clock, for revision ages.
func (m *Model) SetNow(now time.Time) { m.now = now }

// SetContent sets the current crontab.
func (m *Model) SetContent(content []byte, source string) {
	changed := !bytes.Equal(content, m.content)
	m.content, m.source = content, source
	if changed {
		m.summarize()
		m.refresh()
	}
}

// Content is the crontab the view shows.
func (m Model) Content() []byte { return m.content }

// SetRevisions replaces the revision list, keeping the selected revision
// where it can.
func (m *Model) SetRevisions(revs []jobs.Revision, err error) {
	var path string
	if r, ok := m.SelectedRevision(); ok {
		path = r.Path
	}
	m.revs, m.err, m.loaded = revs, err, true
	m.selected = 0
	for i, r := range revs {
		if r.Path == path {
			m.selected = i + 1
		}
	}
	m.summarize()
	m.refresh()
	m.clamp()
}

// SelectedRevision returns the selected revision; false for the current
// crontab.
func (m Model) SelectedRevision() (jobs.Revision, bool) {
	if m.selected > 0 && m.selected <= len(m.revs) {
		return m.revs[m.selected-1], true
	}
	return jobs.Revision{}, false
}

// Capturing reports whether the view wants every key; it never does.
func (m Model) Capturing() bool { return false }

func (m *Model) summarize() {
	m.summaries = make([]string, len(m.revs))
	for i, r := range m.revs {
		next := m.content
		if i > 0 {
			next = m.revs[i-1].Content
		}
		m.summaries[i] = jobs.Summarize(r.Content, next).String()
	}
}

// refresh renders the preview for the selection.
func (m *Model) refresh() {
	if m.th == nil {
		return
	}
	rev, ok := m.SelectedRevision()
	switch {
	case !ok:
		m.lines = Highlight(m.th, m.content)
		if len(m.content) == 0 {
			m.lines = []string{m.th.Muted.Render("The crontab is empty.")}
		}
	case m.full:
		m.lines = Highlight(m.th, rev.Content)
	default:
		d := components.UnifiedDiff("current", "revision", m.content, rev.Content)
		if d == "" {
			m.lines = []string{m.th.Muted.Render("This revision is the same as the current crontab.")}
		} else {
			m.lines = append([]string{m.th.Label.Render("RESTORING WOULD"), ""}, components.DiffLines(m.th, d)...)
		}
	}
	m.scroll = 0
}

func (m Model) wide() bool { return m.width >= 120 }

func (m Model) listRows() int { return max(m.height-2-3, 1) } // borders, room for a note

func (m Model) previewRows() int { return max(m.height-2, 1) }

func (m *Model) clamp() {
	n := len(m.revs) + 1
	m.selected = min(max(m.selected, 0), n-1)
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+m.listRows() {
		m.offset = m.selected - m.listRows() + 1
	}
	m.offset = max(min(m.offset, n-m.listRows()), 0)
	m.scroll = max(min(m.scroll, len(m.lines)-m.previewRows()), 0)
}

func (m *Model) choose(i int) {
	if i = min(max(i, 0), len(m.revs)); i != m.selected {
		m.selected = i
		m.refresh()
	}
	m.clamp()
}

func (m *Model) scrollBy(d int) {
	m.scroll += d
	m.clamp()
}

// Update handles a key and returns a request, or nil.
func (m Model) Update(k tea.KeyPressMsg) (Model, tea.Msg) {
	km := m.keys
	page := max(m.previewRows()-1, 1)
	if m.preview { // the narrow full-screen preview scrolls with every key
		switch {
		case key.Matches(k, km.Back), key.Matches(k, km.Open):
			m.preview = false
		case key.Matches(k, km.Up):
			m.scrollBy(-1)
		case key.Matches(k, km.Down):
			m.scrollBy(1)
		case key.Matches(k, km.PageUp):
			m.scrollBy(-page)
		case key.Matches(k, km.PageDown):
			m.scrollBy(page)
		case key.Matches(k, km.Top):
			m.scrollBy(-len(m.lines))
		case key.Matches(k, km.Bottom):
			m.scrollBy(len(m.lines))
		case key.Matches(k, km.Diff):
			m.toggleFull()
		case key.Matches(k, km.Restore):
			return m, m.restore()
		case key.Matches(k, km.EditCrontab):
			return m, EditMsg{}
		}
		return m, nil
	}
	switch {
	case key.Matches(k, km.Up):
		m.choose(m.selected - 1)
	case key.Matches(k, km.Down):
		m.choose(m.selected + 1)
	case key.Matches(k, km.Top):
		m.choose(0)
	case key.Matches(k, km.Bottom):
		m.choose(len(m.revs))
	case key.Matches(k, km.PageUp):
		m.scrollBy(-page)
	case key.Matches(k, km.PageDown):
		m.scrollBy(page)
	case key.Matches(k, km.Open):
		if !m.wide() {
			m.preview = true
		}
	case key.Matches(k, km.Diff):
		m.toggleFull()
	case key.Matches(k, km.Restore):
		return m, m.restore()
	case key.Matches(k, km.EditCrontab):
		return m, EditMsg{}
	}
	return m, nil
}

func (m *Model) toggleFull() {
	if _, ok := m.SelectedRevision(); ok {
		m.full = !m.full
		m.refresh()
	}
}

func (m Model) restore() tea.Msg {
	if r, ok := m.SelectedRevision(); ok {
		return RestoreMsg{r}
	}
	return nil
}

// Hints are the footer's key hints.
func (m Model) Hints() []key.Binding {
	h := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	_, rev := m.SelectedRevision()
	var out []key.Binding
	switch {
	case m.preview:
		out = append(out, h("↑↓", "scroll"), h("esc", "back"))
	default:
		out = append(out, h("↑↓", "revision"))
		if m.wide() {
			out = append(out, h("pgup/pgdn", "scroll"))
		} else {
			out = append(out, h("↵", "view"))
		}
	}
	if rev {
		what := "full text"
		if m.full {
			what = "diff"
		}
		out = append(out, h("d", what), h("R", "restore"))
	}
	return append(out, h("E", "edit in $EDITOR"), h("u", "undo"), h("^k", "commands"), h("?", "help"))
}

// View renders the view into its width × height.
func (m Model) View() string {
	w, h := m.width, m.height
	if m.preview {
		return m.previewPanel(w, h)
	}
	if !m.wide() {
		return m.listPanel(w, h)
	}
	pw := w * 55 / 100
	return lipgloss.JoinHorizontal(lipgloss.Top, m.previewPanel(pw, h), " ", m.listPanel(w-pw-1, h))
}

func (m Model) previewTitle() string {
	if r, ok := m.SelectedRevision(); ok {
		return "Revision · " + r.At.In(m.now.Location()).Format("Mon 2 Jan 15:04:05")
	}
	if m.source != "" {
		return "Crontab · " + m.source
	}
	return "Crontab"
}

func (m Model) previewPanel(w, h int) string {
	end := min(m.scroll+m.previewRows(), len(m.lines))
	var body []string
	if m.scroll < end {
		body = m.lines[m.scroll:end]
	}
	return components.Panel(m.th, m.previewTitle(), body, w, h, m.preview)
}

func (m Model) listPanel(w, h int) string {
	th := m.th
	iw := w - 4
	var lines []string
	rows := []string{m.row(iw, 0, th.Text.Render("Current crontab"), th.Muted.Render(lineCount(m.content)))}
	for i, r := range m.revs {
		when := r.At.In(m.now.Location()).Format("Mon 2 Jan 15:04:05")
		if !m.now.IsZero() && !r.At.After(m.now) {
			when += th.Muted.Render(" · " + humanize.Duration(m.now.Sub(r.At)) + " ago")
		}
		rows = append(rows, m.row(iw, i+1, th.Text.Render(when), th.Muted.Render(m.summaries[i])))
	}
	end := min(m.offset+m.listRows(), len(rows))
	lines = append(lines, rows[m.offset:end]...)
	switch {
	case m.err != nil:
		lines = append(lines, "", th.Fail.Render("Can't read the backups: "+m.err.Error()))
	case m.loaded && len(m.revs) == 0:
		lines = append(lines, "", th.Muted.Render("No revisions yet. gocron backs up the"), th.Muted.Render("crontab before every change it makes."))
	}
	title := "Revisions"
	if n := len(m.revs); n > 0 {
		title = fmt.Sprintf("Revisions · %d", n)
	}
	return components.Panel(th, title, lines, w, h, !m.preview)
}

// row lays out a list row: when (or "Current crontab") and a summary,
// right-aligned when there's room.
func (m Model) row(iw, i int, left, right string) string {
	gap := iw - 2 - lipgloss.Width(left) - lipgloss.Width(right)
	text := left + strings.Repeat(" ", max(gap, 2)) + right
	if gap < 2 {
		text = left + "  " + right
	}
	if i == m.selected {
		return m.th.SelectionBar.Render(theme.GlyphBar) + " " + text
	}
	return "  " + text
}

func lineCount(b []byte) string {
	n := bytes.Count(b, []byte("\n"))
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

// Click handles a left click at x, y in the view's own coordinates: it
// selects a revision, and on narrow screens clicking the selected one
// opens its preview.
func (m Model) Click(x, y int) (Model, tea.Msg) {
	if m.preview {
		return m, nil
	}
	left := 0
	if m.wide() {
		left = m.width*55/100 + 1
	}
	r := y - 1
	if x <= left || x >= m.width-1 || r < 0 || r >= m.listRows() {
		return m, nil
	}
	i := m.offset + r
	if i > len(m.revs) {
		return m, nil
	}
	if i == m.selected && !m.wide() {
		m.preview = true
		return m, nil
	}
	m.choose(i)
	return m, nil
}

// Wheel scrolls the preview under the pointer at x, or moves through the
// revisions.
func (m Model) Wheel(x, delta int) Model {
	if m.preview || (m.wide() && x < m.width*55/100) {
		m.scrollBy(3 * delta)
		return m
	}
	m.choose(m.selected + delta)
	return m
}
