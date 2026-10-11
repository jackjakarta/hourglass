// Package editor is the new/edit job form: name, a schedule builder with
// presets and a raw mode with a field ruler, the command, tags and run
// options, with a live preview and lints.
package editor

import (
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/jackjakarta/gocron/internal/cronexpr"
	"github.com/jackjakarta/gocron/internal/crontab"
	svc "github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// Requests the editor returns from Update for the app to act on.
type (
	// SaveMsg asks to create (ID == "") or update the job.
	SaveMsg struct {
		ID    string
		Draft svc.Draft
	}
	// CancelMsg asks to close the editor; Dirty means there are unsaved
	// changes to confirm discarding.
	CancelMsg struct{ Dirty bool }
	// TestMsg asks for a cron-environment test run of the draft.
	TestMsg struct{ Draft svc.Draft }
	// BlockedMsg explains why a save didn't happen.
	BlockedMsg struct{ Text string }
)

// Internal messages for the debounced PATH lint.
type (
	lintDueMsg  struct{ seq int }
	pathLintMsg struct {
		seq    int
		issues []svc.Issue
	}
)

// lintDelay is how long typing must pause before the command's program is
// looked up.
const lintDelay = 250 * time.Millisecond

// stop is a focusable control.
type stop int

const (
	stopName stop = iota
	stopPreset
	stopParams
	stopRaw
	stopCommand
	stopTags
	stopTrack
	stopTimeout
	stopNoOverlap
)

// Config is what the editor needs from the app.
type Config struct {
	Theme    *theme.Theme
	Now      time.Time
	Env      svc.CronEnv // cron's environment where the job lives
	UserPATH string      // the user's own PATH, to suggest fixes
	// PathLint checks the command's program against cron's PATH. It runs
	// in the background after typing pauses. nil uses jobs.LintPath with
	// a cache that lives as long as the editor.
	PathLint func(cmd string, env svc.CronEnv) []svc.Issue
}

// Model is the editor.
type Model struct {
	th       *theme.Theme
	now      time.Time
	env      svc.CronEnv
	pathLint func(cmd string, env svc.CronEnv) []svc.Issue

	id        string // "" for a new job
	editTitle string
	orig      svc.Draft
	canTrack  bool // run options are available (an already wrapped job)

	name, raw, command, tags, timeout textinput.Model

	kind cronexpr.PresetKind // the selected preset
	// touched is set once a preset input changes. Until then the schedule
	// keeps the text it opened with, so "@daily" isn't rewritten as
	// "0 0 * * *" by an unrelated edit.
	touched bool
	p       cronexpr.Preset // values for every preset's inputs
	sub     int             // focused input within the preset row
	typed   string          // digits typed into the focused number
	track   bool
	overlap bool // skip if still running

	focus stop

	// Derived in refresh, never in View.
	schedule   string
	expr       *cronexpr.Expr
	exprErr    error
	lit        cronexpr.PresetKind // preset the raw expression matches; Custom if none
	desc       string
	next       []time.Time
	issues     []svc.Issue
	timeoutErr string
	pathSeq    int
	pathFor    string // the command pathIssues describe
	pathIssues []svc.Issue
	attempted  bool // a save was tried, so show every error

	saving        bool
	width, height int
}

// New returns an editor for a new job.
func New(cfg Config) Model {
	m := newModel(cfg)
	m.kind, m.touched = cronexpr.Daily, true
	m.refresh()
	m.orig = m.draft()
	m.focusStop(stopName)
	return m
}

// Edit returns an editor for an existing managed job.
func Edit(cfg Config, j crontab.Job) Model {
	m := newModel(cfg)
	m.id, m.editTitle = j.ID, j.Title()
	m.name.SetValue(j.Name)
	m.command.SetValue(j.Command)
	m.tags.SetValue(strings.Join(j.Tags, ", "))
	m.canTrack, m.track, m.overlap = j.Wrapped || svc.WrapNew, j.Wrapped, j.NoOverlap
	if j.Timeout > 0 {
		m.timeout.SetValue(crontab.FormatDuration(j.Timeout))
	}
	m.kind = cronexpr.Custom
	if e, err := cronexpr.Parse(j.Schedule); err == nil {
		if p, ok := cronexpr.DetectPreset(e); ok {
			m.kind = p.Kind
			m.mergePreset(p)
		}
	}
	m.raw.SetValue(j.Schedule)
	m.refresh()
	m.orig = m.draft()
	m.focusStop(stopName)
	return m
}

func newModel(cfg Config) Model {
	input := func(placeholder string, limit int) textinput.Model {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = placeholder
		ti.CharLimit = limit
		ti.KeyMap.AcceptSuggestion.SetEnabled(false)
		ti.KeyMap.NextSuggestion.SetEnabled(false)
		ti.KeyMap.PrevSuggestion.SetEnabled(false)
		return ti
	}
	if cfg.PathLint == nil {
		cache, userPATH := svc.NewPathCache(), cfg.UserPATH
		cfg.PathLint = func(cmd string, env svc.CronEnv) []svc.Issue {
			return svc.LintPath(cmd, env, userPATH, cache)
		}
	}
	m := Model{
		th:       cfg.Theme,
		now:      cfg.Now,
		env:      cfg.Env,
		pathLint: cfg.PathLint,
		name:     input("defaults to the program's name", 120),
		raw:      input("min hour day-of-month month day-of-week", 200),
		command:  input("e.g. pg_dump app | gzip > /backups/app.sql.gz", 0),
		tags:     input("e.g. backup, db", 200),
		timeout:  input("none", 12),
		canTrack: svc.WrapNew,
		track:    svc.WrapNew,
		overlap:  svc.WrapNew,
		p:        cronexpr.Preset{Every: 15, Hour: 9, Dom: 1, Days: 1 << time.Monday},
		lit:      cronexpr.Custom,
	}
	m.SetTheme(cfg.Theme)
	return m
}

// Init starts the PATH lint for the command the editor opened with.
func (m Model) Init() tea.Cmd { return m.lintPath() }

// SetTheme swaps the theme.
func (m *Model) SetTheme(th *theme.Theme) {
	m.th = th
	for _, ti := range m.inputs() {
		s := textinput.DefaultStyles(!th.Mono)
		s.Focused.Text, s.Blurred.Text = th.Text, th.Text
		s.Focused.Placeholder, s.Blurred.Placeholder = th.Muted, th.Muted
		s.Cursor.Color = th.Accent
		s.Cursor.Blink = false
		ti.SetStyles(s)
	}
}

// SetSize sets the largest area the editor may use.
func (m *Model) SetSize(w, h int) {
	m.width, m.height = w, h
	m.setInputWidths()
}

// SetNow advances the clock for the next-runs preview.
func (m *Model) SetNow(now time.Time) {
	m.now = now
	m.refreshPreview()
}

// SetSaving marks a save in flight; further saves are ignored until it ends.
func (m *Model) SetSaving(v bool) { m.saving = v }

// IsNew reports whether the editor creates a job.
func (m Model) IsNew() bool { return m.id == "" }

// Dirty reports unsaved changes.
func (m Model) Dirty() bool {
	a, b := m.draft(), m.orig
	return a.Name != b.Name || a.Schedule != b.Schedule || a.Command != b.Command ||
		strings.Join(a.Tags, ",") != strings.Join(b.Tags, ",") ||
		a.Wrapped != b.Wrapped || a.Timeout != b.Timeout || a.NoOverlap != b.NoOverlap
}

func (m *Model) inputs() []*textinput.Model {
	return []*textinput.Model{&m.name, &m.raw, &m.command, &m.tags, &m.timeout}
}

func (m *Model) input(s stop) *textinput.Model {
	switch s {
	case stopName:
		return &m.name
	case stopRaw:
		return &m.raw
	case stopCommand:
		return &m.command
	case stopTags:
		return &m.tags
	case stopTimeout:
		return &m.timeout
	}
	return nil
}

// draft is the form's current content.
func (m Model) draft() svc.Draft {
	d := svc.Draft{
		Name:     strings.TrimSpace(m.name.Value()),
		Schedule: strings.Join(strings.Fields(m.schedule), " "),
		Command:  strings.TrimSpace(m.command.Value()),
		Tags:     svc.ParseTags(m.tags.Value()),
		Wrapped:  m.canTrack && m.track,
	}
	if d.Wrapped {
		d.NoOverlap = m.overlap
		d.Timeout, _ = parseTimeout(m.timeout.Value())
	}
	return d
}

func parseTimeout(s string) (time.Duration, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ""
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, "Timeout must be a duration like 30m, 1h or 1h30m."
	}
	if d > 0 && d < time.Second {
		return 0, "Timeout must be at least a second."
	}
	return d, ""
}

// stops returns the focusable controls in order.
func (m Model) stops() []stop {
	out := []stop{stopName, stopPreset}
	if m.kind == cronexpr.Custom {
		out = append(out, stopRaw)
	} else {
		out = append(out, stopParams)
	}
	out = append(out, stopCommand, stopTags)
	if m.canTrack {
		out = append(out, stopTrack)
		if m.track {
			out = append(out, stopTimeout, stopNoOverlap)
		}
	}
	return out
}

func (m *Model) focusStop(s stop) {
	m.focus = s
	m.typed = ""
	for _, ti := range m.inputs() {
		ti.Blur()
	}
	if ti := m.input(s); ti != nil {
		ti.Focus()
	}
}

func (m *Model) moveFocus(delta int) {
	stops := m.stops()
	i := 0
	for k, s := range stops {
		if s == m.focus {
			i = k
		}
	}
	i = min(max(i+delta, 0), len(stops)-1)
	m.focusStop(stops[i])
}

// Update handles a message. It returns a request for the app (SaveMsg, …)
// or nil, and a command for the editor's own background work.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd, tea.Msg) {
	switch msg := msg.(type) {
	case lintDueMsg:
		if msg.seq == m.pathSeq {
			return m, m.lintPath(), nil
		}
		return m, nil, nil
	case pathLintMsg:
		if msg.seq == m.pathSeq {
			m.pathFor, m.pathIssues = m.draft().Command, msg.issues
		}
		return m, nil, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	// Anything else (e.g. a paste) belongs to the focused input.
	if ti := m.input(m.focus); ti != nil {
		var cmd tea.Cmd
		before := ti.Value()
		*ti, cmd = ti.Update(msg)
		if ti.Value() != before {
			return m, tea.Batch(cmd, m.changed()), nil
		}
		return m, cmd, nil
	}
	return m, nil, nil
}

func (m Model) handleKey(k tea.KeyPressMsg) (Model, tea.Cmd, tea.Msg) {
	switch k.String() {
	case "ctrl+s":
		return m.save()
	case "ctrl+t":
		return m, nil, TestMsg{m.draft()}
	case "esc":
		return m, nil, CancelMsg{Dirty: m.Dirty()}
	case "tab", "down", "enter":
		m.moveFocus(1)
		return m, nil, nil
	case "shift+tab", "up":
		m.moveFocus(-1)
		return m, nil, nil
	}
	switch m.focus {
	case stopPreset:
		return m.presetKey(k)
	case stopParams:
		return m.paramsKey(k)
	case stopTrack, stopNoOverlap:
		if k.String() == "space" || k.String() == "x" {
			if m.focus == stopTrack {
				m.track = !m.track
			} else {
				m.overlap = !m.overlap
			}
			return m, m.changed(), nil
		}
		return m, nil, nil
	}
	ti := m.input(m.focus)
	if ti == nil {
		return m, nil, nil
	}
	before, pos := ti.Value(), ti.Position()
	var cmd tea.Cmd
	*ti, cmd = ti.Update(k)
	if ti.Value() != before {
		return m, tea.Batch(cmd, m.changed()), nil
	}
	if m.focus == stopRaw && ti.Position() != pos {
		m.refresh() // the ruler follows the cursor
	}
	return m, cmd, nil
}

func (m Model) presetKey(k tea.KeyPressMsg) (Model, tea.Cmd, tea.Msg) {
	n := cronexpr.PresetKind(len(cronexpr.PresetNames))
	switch k.String() {
	case "left", "h":
		m.setKind((m.kind + n - 1) % n)
	case "right", "l", "space":
		m.setKind((m.kind + 1) % n)
	default:
		return m, nil, nil
	}
	return m, m.changed(), nil
}

// setKind switches presets, seeding the new preset's inputs from the
// current schedule when it fits, and keeping the time of day otherwise.
func (m *Model) setKind(k cronexpr.PresetKind) {
	if m.expr != nil {
		if p, ok := cronexpr.DetectPreset(m.expr); ok {
			m.mergePreset(p)
		}
	}
	if k == cronexpr.Custom {
		m.raw.SetValue(m.schedule)
		m.raw.CursorEnd()
	}
	m.kind, m.sub, m.typed, m.touched = k, 0, "", true
}

// mergePreset copies the values p's kind uses into the shared inputs.
func (m *Model) mergePreset(p cronexpr.Preset) {
	switch p.Kind {
	case cronexpr.EveryNMin:
		m.p.Every = p.Every
	case cronexpr.Hourly:
		m.p.Minute = p.Minute
	default:
		m.p.Minute, m.p.Hour = p.Minute, p.Hour
	}
	switch p.Kind {
	case cronexpr.Weekly:
		m.p.Days = p.Days
	case cronexpr.Monthly:
		m.p.Dom = p.Dom
	}
}

// param is one input in the preset row.
type param struct {
	day      int // ≥ 0: a weekday chip (0 = Sunday); -1: a number
	val      *int
	min, max int
	label    string // shown after the number: "minutes", ":" …
}

// params lists the preset row's inputs for the selected kind.
func (m *Model) params() []param {
	num := func(v *int, lo, hi int) param { return param{day: -1, val: v, min: lo, max: hi} }
	hour, minute := num(&m.p.Hour, 0, 23), num(&m.p.Minute, 0, 59)
	switch m.kind {
	case cronexpr.EveryNMin:
		return []param{num(&m.p.Every, 1, 59)}
	case cronexpr.Hourly:
		return []param{minute}
	case cronexpr.Daily, cronexpr.Weekdays:
		return []param{hour, minute}
	case cronexpr.Weekly:
		var out []param
		for _, d := range weekOrder {
			out = append(out, param{day: d})
		}
		return append(out, hour, minute)
	case cronexpr.Monthly:
		return []param{num(&m.p.Dom, 1, 31), hour, minute}
	}
	return nil
}

// weekOrder is Monday first, as the chips show it.
var weekOrder = []int{1, 2, 3, 4, 5, 6, 0}

func (m Model) paramsKey(k tea.KeyPressMsg) (Model, tea.Cmd, tea.Msg) {
	ps := m.params()
	if len(ps) == 0 {
		return m, nil, nil
	}
	m.sub = min(m.sub, len(ps)-1)
	p := ps[m.sub]
	key := k.String()
	switch {
	case key == "left" || key == "h":
		m.sub, m.typed = max(m.sub-1, 0), ""
		return m, nil, nil
	case key == "right" || key == "l":
		m.sub, m.typed = min(m.sub+1, len(ps)-1), ""
		return m, nil, nil
	case p.day >= 0 && (key == "space" || key == "x"):
		bit := uint8(1) << p.day
		if m.p.Days&^bit == 0 {
			return m, nil, BlockedMsg{"Pick at least one day."}
		}
		m.p.Days ^= bit
	case p.day >= 0:
		return m, nil, nil
	case len(key) == 1 && key[0] >= '0' && key[0] <= '9':
		m.typed += key
		v, _ := strconv.Atoi(m.typed)
		if v > p.max {
			m.typed = key
			v, _ = strconv.Atoi(key)
		}
		*p.val = min(max(v, p.min), p.max)
		if len(m.typed) >= 2 || v*10 > p.max {
			m.typed = "" // the number is complete: move on, like a time picker
			if m.sub+1 < len(ps) {
				m.sub++
			}
		}
	case key == "backspace":
		m.typed = ""
		*p.val = max(*p.val/10, p.min)
	case key == "+" || key == "=" || key == "-":
		step := 1
		if key == "-" {
			step = -1
		}
		span := p.max - p.min + 1
		*p.val = p.min + ((*p.val-p.min+step)%span+span)%span
		m.typed = ""
	default:
		return m, nil, nil
	}
	m.touched = true
	return m, m.changed(), nil
}

// changed recomputes everything derived from the form and schedules the
// PATH lint if the command changed.
func (m *Model) changed() tea.Cmd {
	m.refresh()
	if m.draft().Command == m.pathFor {
		return nil
	}
	m.pathSeq++
	seq := m.pathSeq
	return tea.Tick(lintDelay, func(time.Time) tea.Msg { return lintDueMsg{seq} })
}

func (m Model) lintPath() tea.Cmd {
	cmd, env, lint, seq := m.draft().Command, m.env, m.pathLint, m.pathSeq
	return func() tea.Msg {
		return pathLintMsg{seq: seq, issues: lint(cmd, env)}
	}
}

// refresh derives the schedule, preview and lints from the inputs.
func (m *Model) refresh() {
	if m.kind == cronexpr.Custom || !m.touched {
		m.schedule = m.raw.Value()
	} else {
		p := m.p
		p.Kind = m.kind
		m.schedule = p.Expr()
	}
	m.expr, m.exprErr = cronexpr.Parse(m.schedule)
	m.lit = cronexpr.Custom
	if m.expr != nil {
		if p, ok := cronexpr.DetectPreset(m.expr); ok {
			m.lit = p.Kind
		}
	}
	m.refreshPreview()
	d := m.draft()
	m.issues = svc.LintDraft(d)
	_, m.timeoutErr = parseTimeout(m.timeout.Value())
	if !m.canTrack || !m.track {
		m.timeoutErr = ""
	}
}

func (m *Model) refreshPreview() {
	m.desc, m.next = "", nil
	if m.expr == nil {
		return
	}
	j := crontab.Job{Expr: m.expr, TZ: m.env.TZ}
	m.desc = j.Describe()
	if !m.expr.Reboot {
		m.next = j.NextRuns(m.now, 5)
	}
}

// visibleIssues are the lints to show: an empty command only counts once
// a save was tried, and PATH results only for the command they checked.
func (m Model) visibleIssues() []svc.Issue {
	var out []svc.Issue
	for _, is := range m.issues {
		if is.Code == cronexpr.CodeUnparsable { // the preview line shows it
			continue
		}
		if is.Code == svc.CodeEmptyCommand && !m.attempted {
			continue
		}
		out = append(out, is)
	}
	if m.pathFor == m.draft().Command {
		out = append(out, m.pathIssues...)
	}
	return out
}

func (m Model) save() (Model, tea.Cmd, tea.Msg) {
	if m.saving {
		return m, nil, nil
	}
	m.attempted = true
	m.refresh()
	var bad stop = -1
	switch {
	case m.exprErr != nil:
		bad = stopRaw
		if m.kind != cronexpr.Custom {
			bad = stopParams
		}
	case m.timeoutErr != "":
		bad = stopTimeout
	}
	for _, is := range m.issues {
		if is.Severity != cronexpr.Error || bad >= 0 {
			continue
		}
		switch is.Field {
		case svc.FieldCommand:
			bad = stopCommand
		case svc.FieldTags:
			bad = stopTags
		}
	}
	if bad >= 0 {
		m.focusStop(bad)
		return m, nil, BlockedMsg{"Fix the problems marked ✗ before saving."}
	}
	d := m.draft()
	if d.Name == "" && m.IsNew() {
		d.Name = crontab.CommandName(d.Command)
	}
	return m, nil, SaveMsg{ID: m.id, Draft: d}
}
