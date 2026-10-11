package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/doctor"
	"github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/store"
	"github.com/jackjakarta/gocron/internal/tui/components"
	"github.com/jackjakarta/gocron/internal/tui/keys"
	"github.com/jackjakarta/gocron/internal/tui/theme"
	"github.com/jackjakarta/gocron/internal/tui/views/editor"
	historyview "github.com/jackjakarta/gocron/internal/tui/views/history"
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
		verb       string // "Paused", "Deleted", … ; "" for undo
		title      string
		res        jobs.Result
		err        error
		snap       jobs.Snapshot
		fromEditor bool // a save from the editor, which stays open on failure
	}
	closeEditorMsg struct{}
	historyMsg     struct {
		filter  store.Filter // what was loaded; a stale load is dropped
		entries []historyview.Entry
		err     error
	}
	logMsg struct {
		entry   historyview.Entry
		content []byte
		err     error
	}
	pagerDoneMsg struct{ err error }
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
	history historyview.Model
	editor  *editor.Model // the open new/edit form
	help    bool
	palette *paletteState // the open command palette or tag picker
	confirm *components.Confirm
	toasts  components.Toasts

	stats    map[string]store.JobStats // recent runs by job id
	sessions map[int]*session          // runs started here
	runSeq   int
	paneID   int // the session whose pane shows; 0 for none
}

// New returns the root model.
func New(ctx context.Context, opts Options) Model {
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	th := theme.Select(opts.NoColor, true)
	km := keys.Default()
	history := historyview.New(th, km)
	history.SetNow(opts.Clock.Now())
	jv := jobsview.New(th, km)
	jv.SetSort(jobsview.ParseSort(opts.UI.DefaultSort))
	return Model{
		ctx:      ctx,
		opts:     opts,
		clock:    opts.Clock,
		keys:     km,
		th:       th,
		now:      opts.Clock.Now(),
		jobs:     jv,
		history:  history,
		sessions: map[int]*session{},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tick(), m.loadSnapshot(), m.loadStats(), m.probeDaemon(), tea.RequestBackgroundColor)
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

// editorSave is mutate for a save from the editor.
func (m Model) editorSave(verb, title string, change func(context.Context) (jobs.Result, error)) tea.Cmd {
	cmd := m.mutate(verb, title, change)
	return func() tea.Msg {
		msg := cmd().(mutationMsg)
		msg.fromEditor = true
		return msg
	}
}

func (m Model) editorConfig(env jobs.CronEnv) editor.Config {
	svc := m.opts.Service
	return editor.Config{
		Theme: m.th, Now: m.now, Env: env, UserPATH: m.opts.UserPATH, PathLint: m.opts.PathLint,
		Defaults: svc.Defaults(), WrapErr: svc.WrapError(),
	}
}

func (m Model) openEditor(ed editor.Model) (tea.Model, tea.Cmd) {
	ed.SetSize(m.width, m.bodyHeight())
	m.editor = &ed
	return m, ed.Init()
}

// editorDirty reports an open editor with unsaved changes.
func (m Model) editorDirty() bool { return m.editor != nil && m.editor.Dirty() }

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
		m.history.SetSize(m.width, m.bodyHeight())
		if m.editor != nil {
			m.editor.SetSize(m.width, m.bodyHeight())
		}
		for _, s := range m.sessions {
			s.pane.SetSize(m.width, m.bodyHeight())
		}
		return m, nil

	case tea.BackgroundColorMsg:
		m.th = theme.Select(m.opts.NoColor, msg.IsDark())
		m.jobs.SetTheme(m.th)
		m.history.SetTheme(m.th)
		if m.editor != nil {
			m.editor.SetTheme(m.th)
		}
		for _, s := range m.sessions {
			s.pane.SetTheme(m.th)
		}
		return m, nil

	case tickMsg:
		m.now = m.clock.Now()
		m.ticks++
		m.jobs.SetNow(m.now)
		m.history.SetNow(m.now)
		if m.editor != nil {
			m.editor.SetNow(m.now)
		}
		for _, s := range m.sessions {
			s.pane.SetNow(m.now, m.ticks)
		}
		m.toasts.Expire(m.now)
		cmds := []tea.Cmd{tick()}
		// With unsaved edits, an outside change surfaces as a conflict on
		// save instead of a silent reload (spec §6.5).
		if m.ticks%externalEvery == 0 && !m.editorDirty() {
			cmds = append(cmds, m.checkExternal())
		}
		// Cron's runs land in the history without telling us; look again.
		if m.ticks%externalEvery == 0 {
			cmds = append(cmds, m.loadStats())
			if m.tab == tabHistory && !m.history.LogOpen() {
				cmds = append(cmds, m.loadHistory())
			}
		}
		if m.ticks%daemonEvery == 0 {
			cmds = append(cmds, m.probeDaemon())
		}
		return m, tea.Batch(cmds...)

	case tea.FocusMsg:
		if m.editorDirty() {
			return m, nil
		}
		return m, m.checkExternal()

	case snapshotMsg:
		m.setSnapshot(msg.snap)
		return m, nil

	case statsMsg:
		if msg.err == nil {
			m.stats = msg.stats
			m.syncRuns()
		}
		return m, nil

	case runLaunchedMsg:
		return m.handleRunLaunched(msg)

	case runOutputMsg:
		return m.handleRunOutput(msg)

	case runDoneMsg:
		return m.handleRunDone(msg)

	case historyMsg:
		if sameFilter(msg.filter, m.history.Filter()) {
			m.history.SetEntries(msg.entries, msg.err)
		}
		return m, nil

	case logMsg:
		m.history.OpenLog(msg.entry, msg.content, msg.err)
		return m, nil

	case pagerDoneMsg:
		if msg.err != nil {
			m.toast(components.ToastFail, "", "The pager failed: "+msg.err.Error()+". Set $PAGER to the one you use.")
		}
		return m, nil

	case jobsview.RunMsg:
		return m.startRun(msg.Job.Job, jobs.RunManual)

	case jobsview.TestMsg:
		return m.startRun(msg.Job.Job, jobs.RunCronEnv)

	case jobsview.HistoryMsg:
		m.tab = tabHistory
		m.history.SetJob(msg.Job.ID, msg.Job.Title())
		return m, m.loadHistory()

	case historyview.ReloadMsg:
		return m, m.loadHistory()

	case historyview.OpenLogMsg:
		e := msg.Entry
		return m, func() tea.Msg {
			b, err := readLog(e.Run.LogPath)
			return logMsg{entry: e, content: b, err: err}
		}

	case historyview.PagerMsg:
		return m, openPager(msg.Path)

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

	case jobsview.TagPickerMsg:
		return m.openTagPicker()

	case jobsview.CopyMsg:
		if msg.Text == "" {
			return m, nil
		}
		m.toast(components.ToastOK, "", "Copied the "+msg.What+" to the clipboard")
		return m, tea.SetClipboard(msg.Text)

	case jobsview.NeedAdoptMsg:
		m.toast(components.ToastInfo, "", "That's an unmanaged entry. Press a to adopt it first.")
		return m, nil

	case jobsview.NewMsg:
		return m.openEditor(editor.New(m.editorConfig(m.snap.CronEnv(0))))

	case jobsview.EditMsg:
		return m.openEditor(editor.Edit(m.editorConfig(m.snap.CronEnv(msg.Job.Line)), msg.Job.Job))

	case editor.SaveMsg:
		return m.saveEditor(msg)

	case editor.CancelMsg:
		if !msg.Dirty {
			m.editor = nil
			return m, nil
		}
		m.confirm = &components.Confirm{
			Title:  "Discard your changes?",
			Body:   []string{"Nothing has been saved yet."},
			Safe:   "Keep editing",
			Action: "Discard",
			Danger: true,
			OnOK:   func() tea.Msg { return closeEditorMsg{} },
		}
		return m, nil

	case closeEditorMsg:
		m.editor = nil
		return m, nil

	case editor.TestMsg:
		if strings.TrimSpace(msg.Draft.Command) == "" {
			m.toast(components.ToastInfo, "", "Enter a command to test first.")
			return m, nil
		}
		return m.startRun(draftJob(msg), jobs.RunCronEnv)

	case editor.BlockedMsg:
		m.toast(components.ToastFail, "", msg.Text)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	if m.editor != nil {
		return m.updateEditor(msg)
	}
	return m, nil
}

func (m Model) saveEditor(msg editor.SaveMsg) (tea.Model, tea.Cmd) {
	svc, d := m.opts.Service, msg.Draft
	if svc == nil || m.editor == nil {
		return m, nil
	}
	ed := *m.editor
	ed.SetSaving(true)
	m.editor = &ed
	title := d.Name
	if title == "" {
		title = d.Command
	}
	if msg.ID == "" {
		return m, m.editorSave("Created", title, func(ctx context.Context) (jobs.Result, error) {
			return svc.Create(ctx, d)
		})
	}
	return m, m.editorSave("Saved", title, func(ctx context.Context) (jobs.Result, error) {
		return svc.Update(ctx, msg.ID, d)
	})
}

// updateEditor passes a message to the editor and acts on its request at
// once, like updateJobs.
func (m Model) updateEditor(msg tea.Msg) (tea.Model, tea.Cmd) {
	ed, cmd, req := m.editor.Update(msg)
	m.editor = &ed
	if req == nil {
		return m, cmd
	}
	next, reqCmd := m.Update(req)
	return next, tea.Batch(cmd, reqCmd)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	km := m.keys
	if key.Matches(msg, km.ForceQuit) {
		return m, tea.Quit
	}
	if m.palette != nil {
		return m.updatePalette(msg)
	}
	if m.confirm != nil {
		done, cmd := m.confirm.Update(msg)
		if done {
			m.confirm = nil
		}
		return m, cmd
	}
	if m.paneID != 0 {
		return m.updatePane(msg)
	}
	if m.editor != nil {
		return m.updateEditor(msg)
	}
	if m.help {
		if key.Matches(msg, km.Help, km.Back, km.Quit) {
			m.help = false
		}
		return m, nil
	}
	// ctrl+k opens the palette even while a filter is being typed; ":"
	// is text there, so it only opens the palette from the globals below.
	if msg.String() == "ctrl+k" {
		return m.openPalette()
	}
	if m.tab == tabJobs && m.jobs.Capturing() {
		return m.updateJobs(msg)
	}
	if m.tab == tabHistory && m.history.Capturing() {
		return m.updateHistory(msg)
	}
	switch {
	case key.Matches(msg, km.Quit):
		return m, tea.Quit
	case key.Matches(msg, km.Help):
		m.help = true
		return m, nil
	case key.Matches(msg, km.Palette):
		return m.openPalette()
	case key.Matches(msg, km.Tab1):
		return m.switchTab(tabJobs)
	case key.Matches(msg, km.Tab2):
		return m.switchTab(tabTimeline)
	case key.Matches(msg, km.Tab3):
		return m.switchTab(tabHistory)
	case key.Matches(msg, km.Tab4):
		return m.switchTab(tabCrontab)
	case key.Matches(msg, km.NextTab):
		return m.switchTab((m.tab + 1) % tabCount)
	case key.Matches(msg, km.PrevTab):
		return m.switchTab((m.tab + tabCount - 1) % tabCount)
	case key.Matches(msg, km.Undo):
		if svc := m.opts.Service; svc != nil {
			return m, m.mutate("", "", svc.Undo)
		}
	case m.tab == tabJobs && m.opts.Service != nil:
		return m.updateJobs(msg)
	case m.tab == tabHistory && m.opts.Service != nil:
		return m.updateHistory(msg)
	}
	return m, nil
}

// switchTab shows tab t, loading what it needs when it wasn't showing.
func (m Model) switchTab(t tab) (tea.Model, tea.Cmd) {
	prev := m.tab
	m.tab = t
	if t == prev {
		return m, nil
	}
	switch t {
	case tabHistory:
		return m, m.loadHistory()
	}
	return m, nil
}

// updateHistory passes a key to the History view and acts on its request.
func (m Model) updateHistory(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var req tea.Msg
	m.history, req = m.history.Update(msg)
	if req == nil {
		return m, nil
	}
	return m.Update(req)
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
	if msg.fromEditor && m.editor != nil {
		return m.handleEditorSave(msg)
	}
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

// handleEditorSave closes the editor after a save. On failure the editor
// stays open with the draft, so nothing typed is lost.
func (m Model) handleEditorSave(msg mutationMsg) (tea.Model, tea.Cmd) {
	ed := *m.editor
	ed.SetSaving(false)
	m.editor = &ed
	var conflict *jobs.ConflictError
	switch {
	case msg.err == nil:
		m.editor = nil
		if msg.res.Backup == "" {
			m.toast(components.ToastInfo, "", "Nothing changed.")
		} else {
			m.toast(components.ToastOK, "", fmt.Sprintf("%s %q · backup created · u undo", msg.verb, clip(msg.title, 28)))
		}
		m.jobs.SelectJob(msg.res.ID)
	case errors.As(msg.err, &conflict) && conflict.Retry != nil:
		m.confirm = &components.Confirm{
			Title: "Crontab changed outside gocron",
			Body: []string{
				"Someone edited your crontab after gocron loaded it,",
				"so your job wasn't saved.",
				"",
				"Keep editing: load their version; your edits stay here.",
				"Reapply: load their version and save your job into it.",
			},
			Safe:   "Keep editing",
			Action: "Reapply",
			OnSafe: m.reload("Crontab reloaded · your edits are still here · ^s saves"),
			OnOK:   m.editorSave(msg.verb, msg.title, conflict.Retry),
		}
	case errors.Is(msg.err, crontab.ErrNotFound):
		m.toast(components.ToastFail, "", "This job is no longer in the crontab, so it can't be saved. esc closes the editor.")
	default:
		m.toast(components.ToastFail, "", "Couldn't save: "+msg.err.Error()+".")
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

	if m.editor != nil && m.editor.Modal() {
		screen = components.OverlayCenter(screen, m.editor.View(), w, h)
	}
	if s, ok := m.sessions[m.paneID]; ok {
		screen = components.OverlayCenter(screen, s.pane.View(), w, h)
	}
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
	if m.palette != nil {
		p := m.palette.p.View(m.th, w-8)
		screen = components.Overlay(screen, p, (w-lipgloss.Width(p))/2, 2)
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
		return m.history.View()
	case m.tab == tabCrontab:
		return comingSoon(m.th, "Crontab", "Your raw crontab, its revisions and diffs,", "with one-key restore.", w, h)
	case !m.loaded:
		return components.Panel(m.th, "Jobs", []string{"", m.th.Muted.Render("Reading your crontab…")}, w, h, true)
	case m.editor != nil && !m.editor.Modal():
		return lipgloss.PlaceHorizontal(w, lipgloss.Center, m.editor.View())
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
	case m.palette != nil:
		return []key.Binding{hint("type", "to search"), hint("↑↓", "choose"), hint("↵", "run"), hint("esc", "close")}
	case m.confirm != nil:
		return []key.Binding{hint("←→", "choose"), hint("↵", "confirm"), hint("esc", "cancel")}
	case m.help:
		return []key.Binding{hint("?", "close"), hint("esc", "close")}
	case m.opts.LoadErr != nil:
		return []key.Binding{hint("q", "quit")}
	case m.paneID != 0:
		return m.sessions[m.paneID].pane.Hints()
	case m.editor != nil:
		return m.editor.Hints()
	case m.tab == tabHistory:
		return m.history.Hints()
	case m.tab != tabJobs:
		return []key.Binding{hint("1-4", "views"), hint("tab", "next view"), hint("?", "help"), hint("q", "quit")}
	case m.jobs.Capturing():
		return []key.Binding{hint("type", "to filter"), hint("↵", "done"), hint("esc", "clear")}
	}
	j, ok := m.jobs.Selected()
	if !ok {
		return []key.Binding{hint("n", "new job"), hint("u", "undo"), hint("?", "help"), hint("q", "quit")}
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
		out = append(out, hint("e", "edit"), hint("space", toggle), hint("r", "run"), hint("t", "test"), hint("h", "history"), hint("x", "delete"))
	} else {
		out = append(out, hint("a", "adopt"), hint("r", "run"), hint("t", "test"))
	}
	// The palette and help come before the rest, so narrow footers that
	// cut the end still show where to find everything.
	out = append(out, hint("^k", "commands"), hint("?", "help"), hint("n", "new"), hint("u", "undo"), hint("/", "filter"), hint("q", "quit"))
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

// loadHistory reads the History feed for the view's filters, with the
// first line of each run's output.
func (m Model) loadHistory() tea.Cmd {
	svc, ctx := m.opts.Service, m.ctx
	if svc == nil {
		return nil
	}
	f := m.history.Filter()
	titles := map[string]string{}
	for _, j := range m.snap.Jobs {
		if j.Managed {
			titles[j.ID] = j.Title()
		}
	}
	return func() tea.Msg {
		runs, err := svc.History(ctx, f)
		entries := make([]historyview.Entry, 0, len(runs))
		for _, r := range runs {
			title := titles[r.JobID]
			if title == "" {
				title = r.JobID
			}
			entries = append(entries, historyview.Entry{Run: r, Title: title, First: firstLine(r.LogPath)})
		}
		return historyMsg{f, entries, err}
	}
}

func sameFilter(a, b store.Filter) bool {
	return a.FailuresOnly == b.FailuresOnly && a.Trigger == b.Trigger && a.Limit == b.Limit &&
		strings.Join(a.JobIDs, ",") == strings.Join(b.JobIDs, ",")
}

// firstLine is the first non-blank line of a log, from its first bytes.
func firstLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	for _, l := range strings.Split(string(buf[:n]), "\n") {
		if l = strings.TrimSpace(components.Sanitize(l)); l != "" {
			return l
		}
	}
	return ""
}

// maxLogRead caps how much of a log the viewer loads.
const maxLogRead = 8 << 20

func readLog(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("this run wasn't recorded with a log")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("the log file is gone")
		}
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxLogRead))
}

// openPager hands a log to $PAGER (less -R by default) and returns when it
// exits.
func openPager(path string) tea.Cmd {
	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less -R"
	}
	words, err := crontab.ShellWords(pager)
	if err != nil || len(words) == 0 {
		words = []string{"less", "-R"}
	}
	c := exec.Command(words[0], append(words[1:], path)...)
	return tea.ExecProcess(c, func(err error) tea.Msg { return pagerDoneMsg{err} })
}
