package timeline

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/hourglass/internal/humanize"
	"github.com/jackjakarta/hourglass/internal/jobs"
	"github.com/jackjakarta/hourglass/internal/store"
	"github.com/jackjakarta/hourglass/internal/tui/components"
	"github.com/jackjakarta/hourglass/internal/tui/keys"
	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

// Mode is how much time the timeline spans.
type Mode int

const (
	ModeDay  Mode = iota // the next 24 hours, after a lead-in of past runs
	ModeWeek             // 7 days from today's midnight, in day columns
)

// LeadIn is how much of the past day mode shows, for recorded runs.
const LeadIn = 3 * time.Hour

// OpenMsg asks the app to show a job in the Jobs view.
type OpenMsg struct{ Job jobs.Job }

// Model is the Timeline view.
type Model struct {
	th   *theme.Theme
	keys keys.Map

	snap      jobs.Snapshot
	stats     map[string]store.JobStats
	now       time.Time
	threshold int

	mode   Mode
	offset int // steps scrolled forward: 6h in day mode, a day in week mode

	width, height int
	selected      string // lane key
	laneOff       int    // first visible lane

	grid  Grid
	built time.Time // the minute the grid was built for
}

// New returns an empty Timeline view. threshold is how many jobs firing in
// the same minute count as congestion (≤ 0 uses 3).
func New(th *theme.Theme, km keys.Map, threshold int) Model {
	if threshold <= 0 {
		threshold = 3
	}
	return Model{th: th, keys: km, threshold: threshold}
}

// SetTheme swaps the theme.
func (m *Model) SetTheme(th *theme.Theme) { m.th = th }

// SetSize sets the area the view draws into.
func (m *Model) SetSize(w, h int) {
	m.width, m.height = w, h
	m.rebuild()
}

// SetSnapshot replaces the jobs.
func (m *Model) SetSnapshot(s jobs.Snapshot) {
	m.snap = s
	m.rebuild()
}

// SetRuns sets the jobs' recent runs, for the marks before now.
func (m *Model) SetRuns(stats map[string]store.JobStats) {
	m.stats = stats
	m.rebuild()
}

// SetNow advances the clock. The grid is rebuilt once a minute, not on
// every tick.
func (m *Model) SetNow(now time.Time) {
	m.now = now
	if !now.Truncate(time.Minute).Equal(m.built) {
		m.rebuild()
	}
}

// SetMode switches between day and week, back at now.
func (m *Model) SetMode(mode Mode) {
	m.mode, m.offset = mode, 0
	m.rebuild()
}

// Mode returns the span.
func (m Model) Mode() Mode { return m.mode }

// window returns the span the grid covers.
func (m Model) window() (from, to time.Time) {
	if m.mode == ModeWeek {
		y, mo, d := m.now.Date()
		from = time.Date(y, mo, d+m.offset, 0, 0, 0, 0, m.now.Location())
		return from, time.Date(y, mo, d+m.offset+7, 0, 0, 0, 0, m.now.Location())
	}
	anchor := m.now.Truncate(time.Minute).Add(time.Duration(m.offset) * 6 * time.Hour)
	return anchor.Add(-LeadIn), anchor.Add(24 * time.Hour)
}

// labelWidth is the width of the lane-name column.
func (m Model) labelWidth() int {
	w := 12
	for _, j := range m.snap.Jobs {
		w = max(w, lipgloss.Width(j.Title())+2)
	}
	return min(w, 22, max((m.width-4)/4, 8))
}

func (m Model) cols() int { return max(m.width-4-m.labelWidth()-1, 0) }

func (m *Model) rebuild() {
	if m.width == 0 || m.now.IsZero() {
		return
	}
	from, to := m.window()
	m.grid = Build(m.snap.Jobs, m.stats, from, to, m.now, m.cols(), m.threshold)
	m.built = m.now.Truncate(time.Minute)
	m.clamp()
}

func laneKey(j jobs.Job) string {
	if j.Managed {
		return "id:" + j.ID
	}
	return "line:" + strconv.Itoa(j.Line) + ":" + j.Command
}

func (m Model) index() int {
	for i, l := range m.grid.Lanes {
		if laneKey(l.Job) == m.selected {
			return i
		}
	}
	if len(m.grid.Lanes) > 0 {
		return 0
	}
	return -1
}

// Selected returns the selected lane's job.
func (m Model) Selected() (jobs.Job, bool) {
	if i := m.index(); i >= 0 {
		return m.grid.Lanes[i].Job, true
	}
	return jobs.Job{}, false
}

// laneRows is how many lanes fit.
func (m Model) laneRows() int {
	n := m.height - 2 - 2 - 2 // borders, axis + now marker, separator + density
	return max(n-len(m.warnings()), 1)
}

func (m *Model) clamp() {
	i := m.index()
	if i < 0 {
		m.selected, m.laneOff = "", 0
		return
	}
	m.selected = laneKey(m.grid.Lanes[i].Job)
	vis := m.laneRows()
	if i < m.laneOff {
		m.laneOff = i
	}
	if i >= m.laneOff+vis {
		m.laneOff = i - vis + 1
	}
	m.laneOff = max(min(m.laneOff, len(m.grid.Lanes)-vis), 0)
}

func (m *Model) move(d int) {
	if n := len(m.grid.Lanes); n > 0 {
		i := min(max(m.index()+d, 0), n-1)
		m.selected = laneKey(m.grid.Lanes[i].Job)
		m.clamp()
	}
}

// Update handles a key and returns a request, or nil.
func (m Model) Update(k tea.KeyPressMsg) (Model, tea.Msg) {
	km := m.keys
	switch {
	case key.Matches(k, km.Up):
		m.move(-1)
	case key.Matches(k, km.Down):
		m.move(1)
	case key.Matches(k, km.Top):
		m.move(-len(m.grid.Lanes))
	case key.Matches(k, km.Bottom):
		m.move(len(m.grid.Lanes))
	case k.String() == "left" || k.String() == "h":
		if m.offset > 0 {
			m.offset--
			m.rebuild()
		}
	case k.String() == "right" || k.String() == "l":
		m.offset++
		m.rebuild()
	case key.Matches(k, km.Now):
		m.offset = 0
		m.rebuild()
	case key.Matches(k, km.Day):
		m.SetMode(ModeDay)
	case key.Matches(k, km.Week):
		m.SetMode(ModeWeek)
	case key.Matches(k, km.Open):
		if j, ok := m.Selected(); ok {
			return m, OpenMsg{j}
		}
	}
	return m, nil
}

// Hints are the footer's key hints.
func (m Model) Hints() []key.Binding {
	h := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	mode := h("w", "week")
	if m.mode == ModeWeek {
		mode = h("d", "day")
	}
	out := []key.Binding{h("↑↓", "lane"), h("←→", "scroll"), mode}
	if m.offset > 0 {
		out = append(out, h(".", "now"))
	}
	return append(out, h("↵", "open job"), h("^k", "commands"), h("?", "help"), h("q", "quit"))
}

func (m Model) title() string {
	from, to := m.window()
	switch {
	case m.mode == ModeWeek && m.offset == 0:
		return "Timeline · next 7 days"
	case m.mode == ModeWeek:
		return "Timeline · " + from.Format("Mon 2 Jan") + " – " + to.AddDate(0, 0, -1).Format("Mon 2 Jan")
	case m.offset == 0:
		return "Timeline · next 24h"
	}
	return "Timeline · " + from.Add(LeadIn).Format("Mon 15:04") + " – " + to.Format("Mon 15:04")
}

// View renders the view into its width × height.
func (m Model) View() string {
	th := m.th
	w, h := m.width, m.height
	g := m.grid
	lw := m.labelWidth()
	pad := strings.Repeat(" ", lw+1)

	lines := []string{pad + m.axis(), pad + m.nowMarker()}
	if len(g.Lanes) == 0 {
		lines = append(lines, "", th.Muted.Render("No enabled jobs to show."),
			th.Muted.Render("Paused jobs are left out. Press 1 for Jobs, n to create one."))
		return components.Panel(th, m.title(), lines, w, h, true)
	}
	sel := m.index()
	end := min(m.laneOff+m.laneRows(), len(g.Lanes))
	for i := m.laneOff; i < end; i++ {
		lines = append(lines, m.lane(g.Lanes[i], lw, i == sel))
	}
	for len(lines) < 2+m.laneRows() {
		lines = append(lines, "")
	}
	lines = append(lines, th.Muted.Render(strings.Repeat("─", w-4)))
	lines = append(lines, th.Muted.Render(components.Fit("density", lw))+" "+m.density())
	lines = append(lines, m.warnings()...)
	return components.Panel(th, m.title(), lines, w, h, true)
}

// axis labels hours (day mode) or days (week mode) above the grid.
func (m Model) axis() string {
	g := m.grid
	cells := []rune(strings.Repeat(" ", g.Cols))
	put := func(c int, s string) {
		if c < 0 || c+len([]rune(s)) > len(cells) {
			return
		}
		for i := c - 1; i < c+len([]rune(s))+1; i++ { // keep a gap to the previous label
			if i >= 0 && i < len(cells) && cells[i] != ' ' {
				return
			}
		}
		copy(cells[c:], []rune(s))
	}
	if m.mode == ModeWeek {
		for d := g.From; d.Before(g.To); d = d.AddDate(0, 0, 1) {
			put(g.Col(d), d.Format("Mon 2"))
		}
		return m.th.Muted.Render(string(cells))
	}
	span := g.To.Sub(g.From)
	step := 1
	for _, s := range []int{1, 2, 3, 6, 12} {
		step = s
		if float64(g.Cols)*float64(time.Duration(s)*time.Hour)/float64(span) >= 7 {
			break
		}
	}
	t := g.From.Truncate(time.Hour)
	for ; t.Before(g.To); t = t.Add(time.Hour) {
		if t.Before(g.From) || t.Hour()%step != 0 {
			continue
		}
		put(g.Col(t), t.Format("15:04"))
	}
	return m.th.Muted.Render(string(cells))
}

func (m Model) nowCol() int { return m.grid.Col(m.now) }

func (m Model) nowMarker() string {
	c := m.nowCol()
	if c < 0 {
		return ""
	}
	return strings.Repeat(" ", c) + m.th.AccentText.Render("▼ now")
}

func (m Model) lane(l Lane, lw int, selected bool) string {
	th := m.th
	label := l.Job.Title()
	labelStyle := th.Text
	if !l.Job.Managed {
		labelStyle = th.Muted
	}
	var head string
	if selected {
		head = th.SelectionBar.Render(theme.GlyphBar) + th.Selected.Render(components.Fit(label, lw-1))
	} else {
		head = " " + labelStyle.Render(components.Fit(label, lw-1))
	}

	g := m.grid
	type cell struct {
		s  string
		st lipgloss.Style
	}
	cells := make([]cell, g.Cols)
	for i := range cells {
		cells[i] = cell{" ", th.Text}
	}
	if c := m.nowCol(); c >= 0 {
		cells[c] = cell{"┊", th.Muted}
	}
	from := max(m.nowCol()+1, 0)
	if m.now.Before(g.From) {
		from = 0
	}
	switch {
	case l.Reboot:
		writeText(cells, from, "at boot", th.Muted, func(s string, st lipgloss.Style) cell { return cell{s, st} })
	case l.Band:
		first, last := -1, -1
		for c, n := range l.Cells {
			if n > 0 {
				if first < 0 {
					first = c
				}
				last = c
			}
		}
		for c := first; c >= 0 && c <= last; c++ {
			cells[c] = cell{"▬", th.Schedule}
		}
		label := fmt.Sprintf(" %s× ", group(l.Fires))
		if first >= 0 && last-first+1 > len(label)+4 {
			writeText(cells, first+2, label, th.Muted, func(s string, st lipgloss.Style) cell { return cell{s, st} })
		}
	case l.Fires == 0:
		text := "(next: never)"
		if !l.Next.IsZero() {
			text = "(next: " + humanize.Absolute(l.Next, m.now) + ")"
		}
		writeText(cells, from, text, th.Muted, func(s string, st lipgloss.Style) cell { return cell{s, st} })
	default:
		glyph := "▮"
		if l.Fires > g.Cols/4 {
			glyph = "╷"
		}
		for c, n := range l.Cells {
			if n > 0 {
				cells[c] = cell{glyph, th.Schedule}
			}
		}
	}
	for _, p := range l.Past {
		glyph, st := components.StatusGlyph(th, p.Status)
		cells[p.Col] = cell{glyph, st}
	}
	var b strings.Builder
	b.WriteString(head + " ")
	for _, c := range cells {
		b.WriteString(c.st.Render(c.s))
	}
	return b.String()
}

func writeText[C any](cells []C, at int, text string, st lipgloss.Style, mk func(string, lipgloss.Style) C) {
	for i, r := range []rune(text) {
		if at+i >= 0 && at+i < len(cells) {
			cells[at+i] = mk(string(r), st)
		}
	}
}

// group writes n with thousands separators.
func group(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// density draws how many jobs fire in each column.
func (m Model) density() string {
	th := m.th
	peak := 0
	for _, n := range m.grid.Density {
		peak = max(peak, n)
	}
	var b strings.Builder
	for _, n := range m.grid.Density {
		if n == 0 || peak == 0 {
			b.WriteString(th.Muted.Render(theme.Sparks[0]))
			continue
		}
		lvl := min((n*len(theme.Sparks)+peak-1)/peak, len(theme.Sparks)) - 1
		b.WriteString(th.AccentText.Render(theme.Sparks[max(lvl, 0)]))
	}
	return b.String()
}

// warnings describe congested minutes.
func (m Model) warnings() []string {
	cg := m.grid.Congestion
	if len(cg) == 0 {
		return nil
	}
	first := cg[0]
	at := first.At.Format("15:04")
	if y, mo, d := first.At.Date(); y != m.now.Year() || mo != m.now.Month() || d != m.now.Day() {
		at = first.At.Format("Mon 15:04")
	}
	text := fmt.Sprintf("%s %d jobs fire at %s", theme.GlyphWarn, first.Jobs, at)
	if n := len(cg) - 1; n > 0 {
		text += fmt.Sprintf(" (and %d more %s)", n, plural(n, "minute"))
	}
	return []string{m.th.Warn.Render(text + ". Consider staggering them.")}
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// Click handles a left click at x, y in the view's own coordinates: it
// selects a lane, and clicking the selected lane opens its job.
func (m Model) Click(x, y int) (Model, tea.Msg) {
	r := y - 3 // below the border, axis and now marker
	if x <= 0 || x >= m.width-1 || r < 0 || r >= m.laneRows() {
		return m, nil
	}
	i := m.laneOff + r
	if i >= len(m.grid.Lanes) {
		return m, nil
	}
	if i == m.index() {
		if j, ok := m.Selected(); ok {
			return m, OpenMsg{j}
		}
	}
	m.selected = laneKey(m.grid.Lanes[i].Job)
	m.clamp()
	return m, nil
}

// Wheel moves between lanes.
func (m Model) Wheel(delta int) Model {
	m.move(delta)
	return m
}
