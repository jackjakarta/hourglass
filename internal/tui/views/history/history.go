// Package history is the History view: every run across jobs, newest
// first, with filters, and a log viewer for one run's output.
package history

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/humanize"
	"github.com/jackjakarta/gocron/internal/store"
	"github.com/jackjakarta/gocron/internal/tui/components"
	"github.com/jackjakarta/gocron/internal/tui/keys"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// Requests the view returns from Update.
type (
	// ReloadMsg asks for the feed again: the filters changed.
	ReloadMsg struct{}
	// OpenLogMsg asks to read a run's log for the viewer.
	OpenLogMsg struct{ Entry Entry }
	// PagerMsg asks to open a log in $PAGER.
	PagerMsg struct{ Path string }
)

// Limit is how many runs the feed loads.
const Limit = 500

// Entry is one run in the feed.
type Entry struct {
	Run   store.Run
	Title string // the job's name, or its id when it's gone from the crontab
	First string // the first line of its output
}

// triggers is the order c cycles through.
var triggers = []string{"", store.TriggerCron, store.TriggerManual, store.TriggerTest}

// Model is the History view.
type Model struct {
	th   *theme.Theme
	keys keys.Map
	now  time.Time

	entries []Entry
	err     error
	loaded  bool

	jobID, jobTitle string // the job filter, from h in the Jobs view
	failures        bool
	trigger         string
	text            string // the / filter
	typing          bool

	selected, offset int
	log              *logView

	width, height int
}

// New returns an empty History view.
func New(th *theme.Theme, km keys.Map) Model { return Model{th: th, keys: km} }

// SetTheme swaps the theme.
func (m *Model) SetTheme(th *theme.Theme) { m.th = th }

// SetSize sets the area the view draws into.
func (m *Model) SetSize(w, h int) {
	m.width, m.height = w, h
	m.clamp()
}

// SetNow sets the clock, for running durations.
func (m *Model) SetNow(now time.Time) { m.now = now }

// Filter is what the feed should load.
func (m Model) Filter() store.Filter {
	f := store.Filter{FailuresOnly: m.failures, Trigger: m.trigger, Limit: Limit}
	if m.jobID != "" {
		f.JobIDs = []string{m.jobID}
	}
	return f
}

// SetEntries replaces the feed, keeping the selected run where possible.
func (m *Model) SetEntries(entries []Entry, err error) {
	var sel int64
	if v := m.visible(); m.selected < len(v) {
		sel = v[m.selected].Run.ID
	}
	m.entries, m.err, m.loaded = entries, err, true
	m.selected = 0
	for i, e := range m.visible() {
		if e.Run.ID == sel {
			m.selected = i
		}
	}
	m.clamp()
}

// SetJob filters the feed to one job.
func (m *Model) SetJob(id, title string) {
	m.jobID, m.jobTitle, m.selected, m.offset, m.log = id, title, 0, 0, nil
}

// SetFailures shows only failed runs, or every run.
func (m *Model) SetFailures(on bool) {
	m.failures, m.selected, m.offset, m.log = on, 0, 0, nil
}

// SetTrigger shows only runs with trigger ("" for all).
func (m *Model) SetTrigger(trigger string) {
	m.trigger, m.selected, m.offset, m.log = trigger, 0, 0, nil
}

// ClearFilters shows every run of every job.
func (m *Model) ClearFilters() {
	m.SetJob("", "")
	m.failures, m.trigger, m.text = false, "", ""
}

// Capturing reports whether the view wants every key.
func (m Model) Capturing() bool { return m.typing || (m.log != nil && m.log.searching) }

// LogOpen reports whether the log viewer is showing.
func (m Model) LogOpen() bool { return m.log != nil }

// OpenLog shows a run's log.
func (m *Model) OpenLog(e Entry, content []byte, err error) {
	m.log = newLogView(e, content, err)
}

// visible applies the / filter to the loaded runs.
func (m Model) visible() []Entry {
	if m.text == "" {
		return m.entries
	}
	var out []Entry
	q := strings.ToLower(m.text)
	for _, e := range m.entries {
		if strings.Contains(strings.ToLower(e.Title+" "+e.Run.JobID+" "+e.First), q) {
			out = append(out, e)
		}
	}
	return out
}

func (m Model) rows() int { return max(m.height-2-3, 1) } // borders, filter line, header, gap

func (m *Model) clamp() {
	n := len(m.visible())
	m.selected = min(max(m.selected, 0), max(n-1, 0))
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+m.rows() {
		m.offset = m.selected - m.rows() + 1
	}
	m.offset = max(min(m.offset, n-m.rows()), 0)
}

// Update handles a key and returns a request, or nil.
func (m Model) Update(k tea.KeyPressMsg) (Model, tea.Msg) {
	if m.log != nil {
		var req tea.Msg
		var closed bool
		*m.log, req, closed = m.log.update(k, m.keys, m.logRows())
		if closed {
			m.log = nil
		}
		return m, req
	}
	if m.typing {
		switch k.String() {
		case "esc":
			m.typing, m.text = false, ""
		case "enter", "up", "down":
			m.typing = false
		case "backspace":
			if r := []rune(m.text); len(r) > 0 {
				m.text = string(r[:len(r)-1])
			}
		case "ctrl+u":
			m.text = ""
		default:
			m.text += k.Text
		}
		m.selected = 0
		m.clamp()
		return m, nil
	}
	km := m.keys
	v := m.visible()
	switch {
	case key.Matches(k, km.Up):
		m.selected--
	case key.Matches(k, km.Down):
		m.selected++
	case key.Matches(k, km.Top):
		m.selected = 0
	case key.Matches(k, km.Bottom):
		m.selected = len(v) - 1
	case key.Matches(k, km.PageUp):
		m.selected -= m.rows() - 1
	case key.Matches(k, km.PageDown):
		m.selected += m.rows() - 1
	case key.Matches(k, km.Filter):
		m.typing = true
	case key.Matches(k, km.Failures):
		m.failures = !m.failures
		return m, ReloadMsg{}
	case key.Matches(k, km.Trigger):
		for i, t := range triggers {
			if t == m.trigger {
				m.trigger = triggers[(i+1)%len(triggers)]
				break
			}
		}
		return m, ReloadMsg{}
	case key.Matches(k, km.Back):
		switch {
		case m.text != "":
			m.text = ""
		case m.jobID != "" || m.failures || m.trigger != "":
			m.jobID, m.jobTitle, m.failures, m.trigger = "", "", false, ""
			return m, ReloadMsg{}
		}
	case key.Matches(k, km.Open):
		if m.selected < len(v) {
			return m, OpenLogMsg{v[m.selected]}
		}
	case key.Matches(k, km.Pager):
		if m.selected < len(v) {
			return m, PagerMsg{v[m.selected].Run.LogPath}
		}
	}
	m.clamp()
	return m, nil
}

// Selected returns the selected entry.
func (m Model) Selected() (Entry, bool) {
	v := m.visible()
	if m.selected < len(v) {
		return v[m.selected], true
	}
	return Entry{}, false
}

// Hints are the footer's key hints.
func (m Model) Hints() []key.Binding {
	h := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	switch {
	case m.log != nil && m.log.searching:
		return []key.Binding{h("type", "to search"), h("↵", "done"), h("esc", "cancel")}
	case m.log != nil:
		return []key.Binding{h("↑↓", "scroll"), h("/", "search"), h("n/N", "next/prev"), h("g/G", "top/bottom"), h("o", "pager"), h("esc", "back")}
	case m.typing:
		return []key.Binding{h("type", "to filter"), h("↵", "done"), h("esc", "clear")}
	}
	trig := "trigger"
	if m.trigger != "" {
		trig = "trigger: " + m.trigger
	}
	out := []key.Binding{h("↑↓", "move"), h("↵", "log"), h("f", "failures"), h("c", trig), h("/", "filter")}
	if m.jobID != "" || m.failures || m.trigger != "" || m.text != "" {
		out = append(out, h("esc", "clear"))
	}
	return append(out, h("1", "jobs"), h("?", "help"), h("q", "quit"))
}

// View renders the view.
func (m Model) View() string {
	if m.log != nil {
		return m.log.view(m.th, m.now, m.width, m.height)
	}
	th := m.th
	w, h := m.width, m.height
	iw := w - 4
	title := "History"
	if n := len(m.visible()); m.loaded {
		title = fmt.Sprintf("History · %d", n)
		if n == Limit {
			title += "+"
		}
	}

	var lines []string
	lines = append(lines, m.filterLine(iw))
	switch {
	case m.err != nil:
		lines = append(lines, "", th.Fail.Render(theme.GlyphFail+" Can't read the run history"))
		lines = append(lines, wrapLines(th.Text, m.err.Error(), iw)...)
		lines = append(lines, "", th.Muted.Render("Run ")+th.Key.Render("gocron doctor")+th.Muted.Render(" to see what's wrong."))
		return components.Panel(th, title, lines, w, h, true)
	case !m.loaded:
		lines = append(lines, "", th.Muted.Render("Reading run history…"))
		return components.Panel(th, title, lines, w, h, true)
	}

	v := m.visible()
	if len(v) == 0 {
		lines = append(lines, "")
		if m.jobID == "" && !m.failures && m.trigger == "" && m.text == "" {
			lines = append(lines,
				th.Title.Render("No runs yet"), "",
				th.Text.Render("Jobs with Track runs on record every run here: when it ran, how it"),
				th.Text.Render("ended, and what it printed. Press 1, pick a job and r to run it now."))
		} else {
			lines = append(lines, th.Muted.Render("No runs match. esc clears the filters."))
		}
		return components.Panel(th, title, lines, w, h, true)
	}
	c := m.columns(iw)
	lines = append(lines, th.Label.Render(c.header()), "")
	end := min(m.offset+m.rows(), len(v))
	for i := m.offset; i < end; i++ {
		lines = append(lines, m.row(v[i], c, i == m.selected))
	}
	return components.Panel(th, title, lines, w, h, true)
}

func (m Model) filterLine(iw int) string {
	th := m.th
	var parts []string
	switch {
	case m.typing:
		parts = append(parts, th.AccentText.Render("/ ")+th.Text.Render(m.text)+th.AccentText.Render("█"))
	case m.text != "":
		parts = append(parts, th.AccentText.Render("/ ")+th.Text.Render(m.text))
	default:
		parts = append(parts, th.Muted.Render("/ filter…"))
	}
	if m.jobID != "" {
		parts = append(parts, th.Schedule.Render("job: "+m.jobTitle))
	}
	if m.failures {
		parts = append(parts, th.Fail.Render("failures only"))
	}
	if m.trigger != "" {
		parts = append(parts, th.Schedule.Render("trigger: "+m.trigger))
	}
	return components.Fit(strings.Join(parts, th.Muted.Render("  ·  ")), iw)
}

type columns struct {
	compact                    bool
	when, job, trig, dur, exit int
	out                        int
}

func (m Model) columns(iw int) columns {
	c := columns{when: 16, trig: 7, dur: 7, exit: 4}
	if iw < 76 {
		c.compact, c.when, c.trig, c.exit = true, 11, 0, 0
	}
	fixed := 3 + c.when + 2 + 2 + c.dur + 2 // bar, glyph, space; gaps after each column
	if !c.compact {
		fixed += c.trig + 2 + c.exit + 2
	}
	longest := 3
	for _, e := range m.visible() {
		longest = max(longest, lipgloss.Width(e.Title))
	}
	rest := iw - fixed
	c.job = min(longest, max(rest*40/100, 8))
	c.out = max(rest-c.job, 0)
	return c
}

func (c columns) header() string {
	s := "   " + components.Fit("STARTED", c.when) + "  " + components.Fit("JOB", c.job) + "  "
	if !c.compact {
		s += components.Fit("TRIGGER", c.trig) + "  "
	}
	s += components.Fit("TOOK", c.dur) + "  "
	if !c.compact {
		s += components.Fit("EXIT", c.exit) + "  "
	}
	if c.out > 0 {
		s += "OUTPUT"
	}
	return s
}

func (m Model) row(e Entry, c columns, selected bool) string {
	th := m.th
	paint := func(st lipgloss.Style, s string) string {
		if selected {
			st = st.Inherit(th.Selected)
		}
		return st.Render(s)
	}
	r := e.Run
	bar := " "
	if selected {
		bar = th.SelectionBar.Render(theme.GlyphBar)
	}
	glyph, gst := components.StatusGlyph(th, r.Status)
	when := humanize.Absolute(r.StartedAt, m.now)
	if c.compact {
		when = r.StartedAt.Local().Format("02 Jan 15:04")
	}
	dur := "—"
	if d := r.Duration(m.now); (r.EndedAt != nil && r.Status != store.StatusSkipped) || r.Status == store.StatusRunning {
		dur = humanize.Duration(d)
	}
	exit := ""
	if r.ExitCode != nil {
		exit = strconv.Itoa(*r.ExitCode)
	}
	s := bar + paint(gst, glyph) + paint(th.Text, " ") + paint(th.Muted, components.Fit(when, c.when)) + paint(th.Text, "  ") +
		paint(th.Text, components.Fit(e.Title, c.job)) + paint(th.Text, "  ")
	if !c.compact {
		s += paint(triggerStyle(th, r.Trigger), components.Fit(r.Trigger, c.trig)) + paint(th.Text, "  ")
	}
	s += paint(th.Text, components.Fit(dur, c.dur)) + paint(th.Text, "  ")
	if !c.compact {
		est := th.Muted
		if r.ExitCode != nil && *r.ExitCode != 0 {
			est = th.Fail
		}
		s += paint(est, components.Fit(exit, c.exit)) + paint(th.Text, "  ")
	}
	if c.out > 0 {
		s += paint(th.Muted, components.Fit(e.First, c.out))
	}
	return s + paint(th.Text, strings.Repeat(" ", max(m.width-4-lipgloss.Width(s), 0)))
}

func triggerStyle(th *theme.Theme, trigger string) lipgloss.Style {
	switch trigger {
	case store.TriggerManual:
		return th.AccentText
	case store.TriggerTest:
		return th.Schedule
	}
	return th.Muted
}

func (m Model) logRows() int { return max(m.height-2-3, 1) }

func wrapLines(st lipgloss.Style, s string, w int) []string {
	var out []string
	for _, l := range strings.Split(lipgloss.NewStyle().Width(w).Render(s), "\n") {
		out = append(out, st.Render(strings.TrimRight(l, " ")))
	}
	return out
}
