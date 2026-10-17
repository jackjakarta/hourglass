// Package runpane is the modal that streams a run started from hourglass:
// run now, a test in cron's environment, or a rerun in the user's shell.
package runpane

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/hourglass/internal/humanize"
	svc "github.com/jackjakarta/hourglass/internal/jobs"
	"github.com/jackjakarta/hourglass/internal/runner"
	"github.com/jackjakarta/hourglass/internal/store"
	"github.com/jackjakarta/hourglass/internal/tui/components"
	"github.com/jackjakarta/hourglass/internal/tui/keys"
	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

// Requests the pane returns from Update.
type (
	// CloseMsg hides the pane; a run in progress carries on.
	CloseMsg struct{}
	// StopMsg asks to stop the run.
	StopMsg struct{}
	// CompareMsg asks to rerun the command in the user's own environment.
	CompareMsg struct{}
)

// maxLines is how much output the pane keeps; the log has all of it.
const maxLines = 1000

// minOutputRows is the least output the pane shows.
const minOutputRows = 3

// Model is the run pane.
type Model struct {
	th   *theme.Theme
	keys keys.Map

	title, command string
	kind           svc.RunKind
	recorded       bool
	notRecorded    string // why the run isn't recorded, when it isn't
	jobID          string

	started, now time.Time
	frame        int
	launched     bool
	out          *components.Lines
	done         bool
	res          runner.Result
	tips         []string
	launchErr    error
	stopping     bool
	scroll       int // lines up from the bottom; 0 follows the output

	width, height int
}

// Run describes the run the pane shows.
type Run struct {
	Title, Command string
	Kind           svc.RunKind
	JobID          string
	// NotRecorded says why the run isn't kept in history ("" when it is).
	NotRecorded string
	Started     time.Time
}

// New returns a pane for a run that's starting.
func New(th *theme.Theme, km keys.Map, r Run) Model {
	return Model{
		th: th, keys: km,
		title: r.Title, command: r.Command, kind: r.Kind, jobID: r.JobID,
		recorded: r.NotRecorded == "", notRecorded: r.NotRecorded,
		started: r.Started, now: r.Started,
		out: components.NewLines(maxLines),
	}
}

// SetTheme swaps the theme.
func (m *Model) SetTheme(th *theme.Theme) { m.th = th }

// SetSize sets the largest area the pane may use.
func (m *Model) SetSize(w, h int) { m.width, m.height = w, h }

// SetNow advances the elapsed time and the spinner.
func (m *Model) SetNow(now time.Time, frame int) { m.now, m.frame = now, frame }

// Launched marks the run as started.
func (m *Model) Launched() { m.launched = true }

// LaunchFailed records that the run couldn't start.
func (m *Model) LaunchFailed(err error) { m.launchErr, m.done = err, true }

// Write adds output.
func (m *Model) Write(p []byte) { m.out.Write(p) }

// Finish records how the run ended and what might explain a failure.
func (m *Model) Finish(res runner.Result, tips []string) {
	m.done, m.res, m.tips, m.stopping = true, res, tips, false
}

// Done reports whether the run is over.
func (m Model) Done() bool { return m.done }

// Failed reports whether the run ended badly.
func (m Model) Failed() bool {
	return m.done && (m.launchErr != nil || (m.res.Status != store.StatusSuccess && m.res.Status != store.StatusSkipped))
}

// Result is how the run ended.
func (m Model) Result() runner.Result { return m.res }

// Title is the run's job title.
func (m Model) Title() string { return m.title }

// canCompare: a failed cron-environment test can be rerun in the user's
// shell to see whether the environment is the difference.
func (m Model) canCompare() bool { return m.Failed() && m.launchErr == nil && m.kind == svc.RunCronEnv }

// Update handles a key and returns a request, or nil.
func (m Model) Update(k tea.KeyPressMsg) (Model, tea.Msg) {
	km := m.keys
	switch {
	case key.Matches(k, km.Back, km.Quit):
		return m, CloseMsg{}
	case key.Matches(k, km.Stop) && !m.done && m.launched:
		m.stopping = true
		return m, StopMsg{}
	case key.Matches(k, km.Compare) && m.canCompare():
		return m, CompareMsg{}
	case key.Matches(k, km.Up):
		m.scrollBy(1)
	case key.Matches(k, km.Down):
		m.scrollBy(-1)
	case key.Matches(k, km.PageUp):
		m.scrollBy(m.outputRows() - 1)
	case key.Matches(k, km.PageDown):
		m.scrollBy(-(m.outputRows() - 1))
	case key.Matches(k, km.Top):
		m.scrollBy(m.out.Len())
	case key.Matches(k, km.Bottom):
		m.scroll = 0
	}
	return m, nil
}

func (m *Model) scrollBy(n int) {
	m.scroll = min(max(m.scroll+n, 0), max(m.out.Len()-m.outputRows(), 0))
}

// Hints are the footer's key hints.
func (m Model) Hints() []key.Binding {
	h := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	var out []key.Binding
	if !m.done {
		out = append(out, h("x", "stop"))
	}
	if m.canCompare() {
		out = append(out, h("c", "rerun in your shell"))
	}
	out = append(out, h("↑↓", "scroll"))
	if m.done {
		return append(out, h("esc", "close"))
	}
	return append(out, h("esc", "hide · keeps running"))
}

// size is the pane's outer size: as tall as its content, within the
// area it was given.
func (m Model) size() (int, int) {
	w := max(min(m.width-4, 100), 40)
	content := 2 + 3 + max(m.out.Len(), minOutputRows) + m.footerRowsAt(w-4)
	return w, min(max(content, 12), max(m.height-2, 10))
}

func (m Model) footer() []string { return m.footerAt(max(min(m.width-4, 100), 40) - 4) }

func (m Model) footerAt(iw int) []string {
	th := m.th
	var out []string
	switch {
	case m.launchErr != nil:
		out = append(out, wrap(th.Fail, "Couldn't start the run: "+m.launchErr.Error(), iw)...)
	case m.done:
		for _, tip := range m.tips {
			out = append(out, wrap(th.Warn, theme.GlyphWarn+" "+tip, iw)...)
		}
		if m.canCompare() {
			out = append(out, wrap(th.Muted, "Press c to run it in your own shell and compare: that runs the job again.", iw)...)
		}
	}
	if !m.recorded {
		out = append(out, th.Muted.Render("Not kept in history: "+m.notRecorded+"."))
	}
	return out
}

// outputRows is how many output lines fit.
func (m Model) outputRows() int {
	_, h := m.size()
	return max(h-2-3-m.footerRows(), minOutputRows)
}

func (m Model) footerRows() int {
	w, _ := m.size()
	return m.footerRowsAt(w - 4)
}

func (m Model) footerRowsAt(iw int) int {
	if n := len(m.footerAt(iw)); n > 0 {
		return n + 1
	}
	return 0
}

// View renders the pane.
func (m Model) View() string {
	th := m.th
	w, h := m.size()
	iw := w - 4

	var title string
	switch m.kind {
	case svc.RunCronEnv:
		title = "Test in cron's environment · " + m.title
	case svc.RunUserEnv:
		title = "Run in your shell · " + m.title
	default:
		title = "Run now · " + m.title
	}

	lines := []string{m.statusLine(iw), th.Muted.Render(components.Fit("$ "+m.command, iw)), ""}
	rows := m.outputRows()
	out := m.out.Lines()
	switch {
	case len(out) == 0 && m.done:
		lines = append(lines, th.Muted.Render("(no output)"))
	case len(out) == 0:
		lines = append(lines, th.Muted.Render("(no output yet)"))
	default:
		end := len(out) - m.scroll
		start := max(end-rows, 0)
		if start == 0 && m.out.Dropped() > 0 {
			lines = append(lines, th.Muted.Render(fmt.Sprintf("… %d earlier lines are in the log", m.out.Dropped())))
			start++
		}
		for _, l := range out[start:end] {
			lines = append(lines, th.Text.Render(components.Fit(l, iw)))
		}
	}
	for len(lines) < 3+rows {
		lines = append(lines, "")
	}
	if f := m.footer(); len(f) > 0 {
		lines = append(lines, th.Muted.Render(strings.Repeat("─", iw)))
		lines = append(lines, f...)
	}
	return components.Panel(th, title, lines, w, h, true)
}

func (m Model) statusLine(iw int) string {
	th := m.th
	var left string
	switch {
	case m.launchErr != nil:
		left = th.Fail.Render(theme.GlyphFail + " couldn't start")
	case !m.done && !m.launched:
		left = th.Muted.Render(theme.Spinner[m.frame%len(theme.Spinner)] + " starting…")
	case !m.done:
		verb := " running · "
		if m.stopping {
			verb = " stopping · "
		}
		left = th.Warn.Render(theme.Spinner[m.frame%len(theme.Spinner)]+verb) + th.Text.Render(components.Elapsed(m.now.Sub(m.started)))
	case m.res.Status == store.StatusSkipped:
		left = th.Muted.Render(theme.GlyphSkipped + " skipped · the previous run is still going")
	default:
		glyph, st := components.StatusGlyph(th, m.res.Status)
		left = st.Render(glyph+" "+m.res.Status) + th.Muted.Render(fmt.Sprintf(" · exit %d · %s", m.res.ExitCode, humanize.Duration(m.res.Duration)))
	}
	right := ""
	if m.done && m.res.RunID != 0 {
		right = th.Muted.Render(fmt.Sprintf("run %d", m.res.RunID))
	}
	gap := iw - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func wrap(st lipgloss.Style, s string, w int) []string {
	var out []string
	for _, l := range strings.Split(lipgloss.NewStyle().Width(w).Render(s), "\n") {
		out = append(out, st.Render(strings.TrimRight(l, " ")))
	}
	return out
}

// Wheel scrolls the output; a negative delta scrolls back in time.
func (m Model) Wheel(delta int) Model {
	m.scrollBy(-delta * 3)
	return m
}
