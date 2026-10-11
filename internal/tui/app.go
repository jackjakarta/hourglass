package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/doctor"
	"github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/tui/components"
	"github.com/jackjakarta/gocron/internal/tui/keys"
	"github.com/jackjakarta/gocron/internal/tui/theme"
	jobsview "github.com/jackjakarta/gocron/internal/tui/views/jobs"
)

const (
	minWidth, minHeight = 60, 15
	externalEvery       = 5  // ticks between external-change checks
	daemonEvery         = 60 // ticks between daemon probes
)

type tab int

const (
	tabJobs tab = iota
	tabTimeline
	tabHistory
	tabCrontab
	tabCount
)

var tabNames = []string{"Jobs", "Timeline", "History", "Crontab"}

// Messages. All I/O happens in commands that return one of these.
type (
	tickMsg     struct{}
	snapshotMsg struct{ snap jobs.Snapshot }
	daemonMsg   struct{ status doctor.DaemonStatus }
	externalMsg struct {
		changed bool
		err     error
		snap    jobs.Snapshot
		toast   string // overrides the default "reloaded" toast
	}
	mutationMsg struct {
		verb  string // "Paused", "Deleted", … ; "" for undo
		title string
		res   jobs.Result
		err   error
		snap  jobs.Snapshot
	}
)

// Model is the root of the TUI.
type Model struct {
	ctx   context.Context
	opts  Options
	clock Clock
	keys  keys.Map
	th    *theme.Theme

	now    time.Time
	width  int
	height int
	tab    tab
	ticks  int

	loaded  bool
	snap    jobs.Snapshot
	daemon  *doctor.DaemonStatus
	readErr string // last error from the external-change check

	jobs    jobsview.Model
	help    bool
	confirm *components.Confirm
	toasts  components.Toasts
}

// New returns the root model.
func New(ctx context.Context, opts Options) Model {
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	th := theme.Select(opts.NoColor, true)
	km := keys.Default()
	return Model{
		ctx:   ctx,
		opts:  opts,
		clock: opts.Clock,
		keys:  km,
		th:    th,
		now:   opts.Clock.Now(),
		jobs:  jobsview.New(th, km),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tick(), m.loadSnapshot(), m.probeDaemon(), tea.RequestBackgroundColor)
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) loadSnapshot() tea.Cmd {
	svc := m.opts.Service
	if svc == nil {
		return nil
	}
	return func() tea.Msg { return snapshotMsg{svc.Snapshot()} }
}

func (m Model) probeDaemon() tea.Cmd {
	probe := m.opts.Daemon
	if probe == nil {
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg { return daemonMsg{probe(ctx)} }
}

func (m Model) checkExternal() tea.Cmd {
	svc := m.opts.Service
	if svc == nil {
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
		changed, err := svc.CheckExternal(ctx)
		msg := externalMsg{changed: changed, err: err}
		if changed {
			msg.snap = svc.Snapshot()
		}
		return msg
	}
}

func (m Model) reload(toast string) tea.Cmd {
	svc, ctx := m.opts.Service, m.ctx
	return func() tea.Msg {
		err := svc.Reload(ctx)
		return externalMsg{changed: err == nil, err: err, snap: svc.Snapshot(), toast: toast}
	}
}

// mutate runs a change in the background. An empty title is looked up
// from the snapshot by the result's job ID.
func (m Model) mutate(verb, title string, change func(context.Context) (jobs.Result, error)) tea.Cmd {
	svc, ctx := m.opts.Service, m.ctx
	return func() tea.Msg {
		res, err := change(ctx)
		snap := svc.Snapshot()
		if title == "" {
			for _, j := range snap.Jobs {
				if j.Managed && j.ID == res.ID {
					title = j.Title()
				}
			}
		}
		return mutationMsg{verb: verb, title: title, res: res, err: err, snap: snap}
	}
}

func (m *Model) toast(kind components.ToastKind, glyph, text string) {
	m.toasts.Push(components.Toast{Kind: kind, Glyph: glyph, Text: text, At: m.now})
}

func (m *Model) setSnapshot(s jobs.Snapshot) {
	m.snap, m.loaded = s, true
	m.jobs.SetSnapshot(s, m.now)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.jobs.SetSize(m.width, m.bodyHeight())
		return m, nil

	case tea.BackgroundColorMsg:
		m.th = theme.Select(m.opts.NoColor, msg.IsDark())
		m.jobs.SetTheme(m.th)
		return m, nil

	case tickMsg:
		m.now = m.clock.Now()
		m.ticks++
		m.jobs.SetNow(m.now)
		m.toasts.Expire(m.now)
		cmds := []tea.Cmd{tick()}
		if m.ticks%externalEvery == 0 {
			cmds = append(cmds, m.checkExternal())
		}
		if m.ticks%daemonEvery == 0 {
			cmds = append(cmds, m.probeDaemon())
		}
		return m, tea.Batch(cmds...)

	case tea.FocusMsg:
		return m, m.checkExternal()

	case snapshotMsg:
		m.setSnapshot(msg.snap)
		return m, nil

	case daemonMsg:
		m.daemon = &msg.status
		return m, nil

	case externalMsg:
		return m.handleExternal(msg), nil

	case mutationMsg:
		return m.handleMutation(msg)

	case jobsview.ToggleMsg:
		j, svc := msg.Job, m.opts.Service
		verb := "Paused"
		if !j.Enabled {
			verb = "Resumed"
		}
		return m, m.mutate(verb, j.Title(), func(ctx context.Context) (jobs.Result, error) {
			return svc.SetEnabled(ctx, j.ID, !j.Enabled)
		})

	case jobsview.DeleteMsg:
		j, svc := msg.Job, m.opts.Service
		m.confirm = &components.Confirm{
			Title:  "Delete this job?",
			Body:   []string{fmt.Sprintf("%q will be removed from your crontab.", clip(j.Title(), 40)), "A backup is kept, and u undoes it."},
			Safe:   "Cancel",
			Action: "Delete",
			Danger: true,
			OnOK: m.mutate("Deleted", j.Title(), func(ctx context.Context) (jobs.Result, error) {
				return svc.Delete(ctx, j.ID)
			}),
		}
		return m, nil

	case jobsview.AdoptMsg:
		j, svc := msg.Job, m.opts.Service
		return m, m.mutate("Adopted", "", func(ctx context.Context) (jobs.Result, error) {
			return svc.Adopt(ctx, j.Line, j.Raw)
		})

	case jobsview.NeedAdoptMsg:
		m.toast(components.ToastInfo, "", "That's an unmanaged entry. Press a to adopt it first.")
		return m, nil

	case jobsview.ComingSoonMsg:
		m.toast(components.ToastInfo, "", msg.What+" is coming in a later release.")
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	km := m.keys
	if key.Matches(msg, km.ForceQuit) {
		return m, tea.Quit
	}
	if m.confirm != nil {
		done, cmd := m.confirm.Update(msg)
		if done {
			m.confirm = nil
		}
		return m, cmd
	}
	if m.help {
		if key.Matches(msg, km.Help, km.Back, km.Quit) {
			m.help = false
		}
		return m, nil
	}
	if m.tab == tabJobs && m.jobs.Capturing() {
		return m.updateJobs(msg)
	}
	switch {
	case key.Matches(msg, km.Quit):
		return m, tea.Quit
	case key.Matches(msg, km.Help):
		m.help = true
		return m, nil
	case key.Matches(msg, km.Tab1):
		m.tab = tabJobs
	case key.Matches(msg, km.Tab2):
		m.tab = tabTimeline
	case key.Matches(msg, km.Tab3):
		m.tab = tabHistory
	case key.Matches(msg, km.Tab4):
		m.tab = tabCrontab
	case key.Matches(msg, km.NextTab):
		m.tab = (m.tab + 1) % tabCount
	case key.Matches(msg, km.PrevTab):
		m.tab = (m.tab + tabCount - 1) % tabCount
	case key.Matches(msg, km.Undo):
		if svc := m.opts.Service; svc != nil {
			return m, m.mutate("", "", svc.Undo)
		}
	case m.tab == tabJobs && m.opts.Service != nil:
		return m.updateJobs(msg)
	}
	return m, nil
}

// updateJobs passes a key to the Jobs view and acts on its request at once,
// so keys typed right after it (e.g. in a confirm dialog) see the result.
func (m Model) updateJobs(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var req tea.Msg
	m.jobs, req = m.jobs.Update(msg)
	if req == nil {
		return m, nil
	}
	return m.Update(req)
}

func (m Model) handleExternal(msg externalMsg) Model {
	switch {
	case msg.err != nil:
		if text := msg.err.Error(); text != m.readErr {
			m.readErr = text
			m.toast(components.ToastFail, "", "Can't read the crontab: "+text)
		}
	case msg.changed:
		m.readErr = ""
		m.setSnapshot(msg.snap)
		text := "Crontab reloaded (changed outside gocron)"
		if msg.toast != "" {
			text = msg.toast
		}
		m.toast(components.ToastInfo, theme.GlyphReload, text)
	default:
		m.readErr = ""
	}
	return m
}

func (m Model) handleMutation(msg mutationMsg) (tea.Model, tea.Cmd) {
	m.setSnapshot(msg.snap)
	var conflict *jobs.ConflictError
	switch {
	case msg.err == nil && msg.verb == "":
		m.toast(components.ToastOK, "↶", "Undid the last change · backup created")
	case msg.err == nil && msg.res.Backup == "":
		m.toast(components.ToastInfo, "", "Nothing changed.")
	case msg.err == nil:
		m.toast(components.ToastOK, "", fmt.Sprintf("%s %q · backup created · u undo", msg.verb, clip(msg.title, 28)))
		m.jobs.SelectJob(msg.res.ID)
	case errors.As(msg.err, &conflict) && conflict.Retry != nil:
		retry := conflict.Retry
		m.confirm = &components.Confirm{
			Title: "Crontab changed outside gocron",
			Body: []string{
				"Someone edited your crontab after gocron loaded it,",
				"so your change wasn't saved.",
				"",
				"Reapply: load their version and apply your change to it.",
				"Discard: load their version and drop your change.",
			},
			Safe:   "Discard",
			Action: "Reapply",
			OnSafe: m.reload("Crontab reloaded · your change was discarded"),
			OnOK:   m.mutate(msg.verb, msg.title, retry),
		}
	case errors.As(msg.err, &conflict):
		return m, m.reload("Crontab changed outside gocron, so nothing was undone · reloaded")
	case errors.Is(msg.err, jobs.ErrNothingToUndo):
		m.toast(components.ToastInfo, "", "Nothing to undo.")
	case errors.Is(msg.err, jobs.ErrUndoStale):
		m.toast(components.ToastFail, "", "Can't undo: the crontab changed since your last change.")
	case errors.Is(msg.err, crontab.ErrNotFound):
		m.toast(components.ToastFail, "", "That entry is no longer in the crontab. The list is up to date now.")
	default:
		m.toast(components.ToastFail, "", "Couldn't save: "+msg.err.Error()+". Run gocron doctor for details.")
	}
	return m, nil
}

func (m Model) bodyHeight() int { return max(m.height-3, 0) }

// View renders the screen.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.ReportFocus = true
	v.WindowTitle = "gocron"
	return v
}

func (m Model) render() string {
	w, h := m.width, m.height
	if w == 0 || h == 0 {
		return ""
	}
	if w < minWidth || h < minHeight {
		return m.tooSmall()
	}
	body := m.body()
	screen := lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top,
		m.header()+"\n\n"+lipgloss.Place(w, m.bodyHeight(), lipgloss.Left, lipgloss.Top, body)+"\n"+m.footer())

	if t := m.toasts.View(m.th, m.now, w-2); t != "" {
		// Bottom right, inside the body's panel border.
		screen = components.Overlay(screen, t, w-lipgloss.Width(t)-2, h-2-lipgloss.Height(t))
	}
	switch {
	case m.confirm != nil:
		screen = components.OverlayCenter(screen, m.confirm.View(m.th), w, h)
	case m.help:
		screen = components.OverlayCenter(screen, components.HelpOverlay(m.th, m.keys.FullHelp(), keys.GroupTitles), w, h)
	}
	return screen
}

func (m Model) tooSmall() string {
	msg := m.th.Title.Render("Make me a bit bigger") + "\n\n" +
		m.th.Muted.Render(fmt.Sprintf("gocron needs %d×%d;", minWidth, minHeight)) + "\n" +
		m.th.Muted.Render(fmt.Sprintf("this terminal is %d×%d.", m.width, m.height))
	return components.Center(lipgloss.NewStyle().Align(lipgloss.Center).Render(msg), m.width, m.height)
}

func (m Model) header() string {
	th := m.th
	left := " " + th.AccentText.Bold(true).Render(theme.GlyphClock+" gocron") + "   "
	for i, name := range tabNames {
		if i > 0 {
			left += "  "
		}
		if tab(i) == m.tab {
			left += th.TabActive.Render(name)
		} else {
			left += th.Tab.Render(name)
		}
	}
	clock := th.Text.Render(m.now.Format("Mon 15:04:05")) + " "
	var daemon string
	switch {
	case m.daemon == nil:
	case m.daemon.Running:
		daemon = th.OK.Render(theme.GlyphEnabled) + th.Muted.Render(" cron active")
	default:
		daemon = th.Fail.Render(theme.GlyphFail + " cron not running")
	}
	right := clock
	if daemon != "" {
		right = daemon + th.Muted.Render(" · ") + clock
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		right = clock
		gap = m.width - lipgloss.Width(left) - lipgloss.Width(right)
	}
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) body() string {
	w, h := m.width, m.bodyHeight()
	switch {
	case m.opts.LoadErr != nil:
		return m.loadError(w, h)
	case m.tab == tabTimeline:
		return comingSoon(m.th, "Timeline", "See when your jobs fire over the next day or week,", "and spot the minutes where too many run at once.", w, h)
	case m.tab == tabHistory:
		return comingSoon(m.th, "History", "Every run with its exit code, duration and log.", "It arrives with run tracking.", w, h)
	case m.tab == tabCrontab:
		return comingSoon(m.th, "Crontab", "Your raw crontab, its revisions and diffs,", "with one-key restore.", w, h)
	case !m.loaded:
		return components.Panel(m.th, "Jobs", []string{"", m.th.Muted.Render("Reading your crontab…")}, w, h, true)
	}
	return m.jobs.View()
}

func (m Model) loadError(w, h int) string {
	th := m.th
	lines := []string{
		"",
		th.Fail.Render(theme.GlyphFail + " gocron can't read your crontab"),
		"",
	}
	for _, l := range strings.Split(lipgloss.NewStyle().Width(w-6).Render(m.opts.LoadErr.Error()), "\n") {
		lines = append(lines, th.Text.Render(l))
	}
	lines = append(lines, "", th.Muted.Render("Run ")+th.Key.Render("gocron doctor")+th.Muted.Render(" to see what's wrong. Press q to quit."))
	return components.Panel(th, "Jobs", lines, w, h, true)
}

func comingSoon(th *theme.Theme, name, line1, line2 string, w, h int) string {
	card := th.Heading.Render(name) + "\n\n" + th.Text.Render(line1) + "\n" + th.Text.Render(line2) + "\n\n" + th.Muted.Render("Coming soon · press 1 for Jobs")
	card = lipgloss.NewStyle().Align(lipgloss.Center).Render(card)
	return components.Panel(th, name, strings.Split(components.Center(card, w-4, h-2), "\n"), w, h, false)
}

func (m Model) footer() string {
	return " " + components.Footer(m.th, m.footerKeys(), m.width-2)
}

func hint(k, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(k), key.WithHelp(k, desc))
}

func (m Model) footerKeys() []key.Binding {
	switch {
	case m.confirm != nil:
		return []key.Binding{hint("←→", "choose"), hint("↵", "confirm"), hint("esc", "cancel")}
	case m.help:
		return []key.Binding{hint("?", "close"), hint("esc", "close")}
	case m.opts.LoadErr != nil:
		return []key.Binding{hint("q", "quit")}
	case m.tab != tabJobs:
		return []key.Binding{hint("1-4", "views"), hint("tab", "next view"), hint("?", "help"), hint("q", "quit")}
	case m.jobs.Capturing():
		return []key.Binding{hint("type", "to filter"), hint("↵", "done"), hint("esc", "clear")}
	}
	j, ok := m.jobs.Selected()
	if !ok {
		return []key.Binding{hint("u", "undo"), hint("?", "help"), hint("q", "quit")}
	}
	var out []key.Binding
	if m.jobs.DetailOpen() {
		out = append(out, hint("esc", "back"))
	} else {
		out = append(out, hint("↑↓", "move"))
		if jobsview.LayoutFor(m.width) != jobsview.LayoutSplit {
			out = append(out, hint("↵", "open"))
		}
	}
	if j.Managed {
		toggle := "pause"
		if !j.Enabled {
			toggle = "resume"
		}
		out = append(out, hint("space", toggle), hint("x", "delete"))
	} else {
		out = append(out, hint("a", "adopt"))
	}
	out = append(out, hint("u", "undo"), hint("/", "filter"), hint("?", "help"), hint("q", "quit"))
	return out
}

// clip shortens s to n runes, ending in "…" when cut.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
