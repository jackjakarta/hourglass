package editor

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jackjakarta/gocron/internal/cronexpr"
	"github.com/jackjakarta/gocron/internal/crontab"
	svc "github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// Sun 11 Oct 2026 14:32:07 UTC
var testNow = time.Date(2026, 10, 11, 14, 32, 7, 0, time.UTC)

func config() Config {
	return Config{
		Theme: theme.Mono(),
		Now:   testNow,
		Env:   svc.CronEnv{PATH: svc.DefaultCronPATH},
		PathLint: func(cmd string, _ svc.CronEnv) []svc.Issue {
			if strings.HasPrefix(cmd, "pg_dump") {
				return []svc.Issue{{Field: svc.FieldCommand, Issue: cronexpr.Issue{Severity: cronexpr.Warn, Code: svc.CodeNotOnCronPath, Message: "pg_dump isn't on cron's PATH"}}}
			}
			return nil
		},
	}
}

func newEditor() Model {
	m := New(config())
	m.SetSize(120, 40)
	return m
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "space":
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	case "ctrl+s", "ctrl+u":
		return tea.KeyPressMsg{Code: rune(k[len(k)-1]), Mod: tea.ModCtrl}
	}
	panic("unknown key " + k)
}

// press sends named keys ("tab", "ctrl+s", …) and types anything else
// character by character. It returns the last request.
func press(t *testing.T, m *Model, keys ...string) tea.Msg {
	t.Helper()
	var req tea.Msg
	for _, k := range keys {
		var msgs []tea.Msg
		if isNamed(k) {
			msgs = append(msgs, keyMsg(k))
		} else {
			for _, c := range k {
				msgs = append(msgs, tea.KeyPressMsg{Code: c, Text: string(c)})
			}
		}
		for _, msg := range msgs {
			*m, _, req = m.Update(msg)
		}
	}
	return req
}

func isNamed(k string) bool {
	switch k {
	case "tab", "shift+tab", "up", "down", "left", "right", "esc", "backspace", "space", "ctrl+s", "ctrl+u":
		return true
	}
	return false
}

func TestPresets(t *testing.T) {
	tests := []struct {
		name string
		keys []string // from the preset carousel (Daily selected)
		want string
	}{
		{"default", nil, "0 9 * * *"},
		{"every n", []string{"left", "left", "tab", "5"}, "*/5 * * * *"},
		{"every n two digits", []string{"left", "left", "tab", "20"}, "*/20 * * * *"},
		{"every minute", []string{"left", "left", "tab", "1", "-"}, "*/59 * * * *"},
		{"hourly", []string{"left", "tab", "30"}, "30 * * * *"},
		{"daily", []string{"tab", "0230"}, "30 2 * * *"},
		{"daily single digit hour", []string{"tab", "7", "45"}, "45 7 * * *"},
		{"daily +/-", []string{"tab", "-", "right", "-"}, "59 8 * * *"},
		{"daily wraps", []string{"tab", "23", "+"}, "1 23 * * *"},
		{"weekdays", []string{"right", "tab", "0915"}, "15 9 * * 1-5"},
		{"weekly", []string{"right", "right", "tab", "right", "space", "right", "right", "right", "right", "right", "space", "right", "0400"}, "0 4 * * 0-2"},
		{"weekly one day", []string{"right", "right", "tab", "right", "right", "right", "right", "space", "left", "left", "left", "left", "space"}, "0 9 * * 5"},
		{"monthly", []string{"right", "right", "right", "tab", "15", "2330"}, "30 23 15 * *"},
		{"monthly day 31", []string{"right", "right", "right", "tab", "31"}, "0 9 31 * *"},
		{"monthly day 4 completes", []string{"right", "right", "right", "tab", "4", "06"}, "0 6 4 * *"},
		{"backspace", []string{"tab", "18", "left", "backspace"}, "0 1 * * *"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newEditor()
			press(t, &m, "tab") // name → preset
			press(t, &m, tt.keys...)
			if m.schedule != tt.want {
				t.Errorf("schedule = %q, want %q", m.schedule, tt.want)
			}
			if m.exprErr != nil {
				t.Errorf("invalid: %v", m.exprErr)
			}
		})
	}
}

func TestWeeklyNeedsADay(t *testing.T) {
	m := newEditor()
	if req := press(t, &m, "tab", "right", "right", "tab", "space"); req == nil {
		t.Error("unchecking the last day isn't refused")
	}
	if m.schedule != "0 9 * * 1" {
		t.Errorf("schedule = %q", m.schedule)
	}
}

func TestSwitchingPresetsKeepsTheTime(t *testing.T) {
	m := newEditor()
	press(t, &m, "tab", "tab", "0230", "up", "right", "right", "right")
	if m.schedule != "30 2 1 * *" {
		t.Errorf("monthly = %q, want 30 2 1 * *", m.schedule)
	}
	press(t, &m, "right") // Custom keeps the expression
	if m.kind != cronexpr.Custom || m.raw.Value() != "30 2 1 * *" || m.schedule != "30 2 1 * *" {
		t.Errorf("custom = %v %q", m.kind, m.raw.Value())
	}
}

func TestCustomLightsUpMatchingPreset(t *testing.T) {
	m := newEditor()
	press(t, &m, "tab", "left", "left", "left", "tab", "ctrl+u")
	if m.kind != cronexpr.Custom || m.focus != stopRaw {
		t.Fatalf("kind %v, focus %v", m.kind, m.focus)
	}
	for _, c := range "0 9 * * 1-" {
		press(t, &m, string(c))
		if m.lit == cronexpr.Weekdays {
			t.Fatalf("lit up early at %q", m.raw.Value())
		}
	}
	press(t, &m, "5")
	if m.lit != cronexpr.Weekdays || m.kind != cronexpr.Custom {
		t.Errorf("lit %v, kind %v; want Weekdays lit while staying Custom", m.lit, m.kind)
	}
	if !strings.Contains(ansi.Strip(m.View()), "matches Weekdays") {
		t.Error("no matches hint")
	}
	press(t, &m, "backspace", "3") // 1-3 is Weekly now
	if m.lit != cronexpr.Weekly {
		t.Errorf("lit %v, want Weekly", m.lit)
	}
	// Switching to the lit preset takes its values.
	press(t, &m, "up", "left", "left")
	if m.kind != cronexpr.Weekly || m.schedule != "0 9 * * 1-3" {
		t.Errorf("weekly = %v %q", m.kind, m.schedule)
	}
}

func TestPreviewFollowsEveryKeystroke(t *testing.T) {
	m := newEditor()
	press(t, &m, "tab", "left", "left", "left", "tab", "ctrl+u")
	var descs []string
	for _, c := range "*/5 * * * *" {
		press(t, &m, string(c))
		d := m.desc
		if m.exprErr != nil {
			d = "✗"
		}
		descs = append(descs, d)
	}
	if descs[len(descs)-1] != "Every 5 minutes" || descs[0] != "✗" {
		t.Errorf("descriptions: %q", descs)
	}
	if len(m.next) != 5 || !m.next[0].Equal(time.Date(2026, 10, 11, 14, 35, 0, 0, time.UTC)) {
		t.Errorf("next = %v", m.next)
	}
	press(t, &m, "backspace", "backspace", "backspace", "backspace", "backspace", "7")
	if m.exprErr == nil {
		t.Error("expected an error for a 3-field schedule")
	}
}

func TestEditKeepsScheduleText(t *testing.T) {
	j := crontab.Job{ID: "abc234", Name: "Hi", Schedule: "0 9 * * mon-fri", Command: "notify-send hi", Enabled: true, Managed: true}
	m := Edit(config(), j)
	m.SetSize(120, 40)
	if m.kind != cronexpr.Weekdays || m.schedule != "0 9 * * mon-fri" || m.Dirty() {
		t.Fatalf("kind %v schedule %q dirty %v", m.kind, m.schedule, m.Dirty())
	}
	press(t, &m, "!")
	req := press(t, &m, "ctrl+s")
	save, ok := req.(SaveMsg)
	if !ok || save.ID != "abc234" || save.Draft.Name != "Hi!" || save.Draft.Schedule != "0 9 * * mon-fri" {
		t.Fatalf("save = %#v", req)
	}
	press(t, &m, "tab", "tab", "+") // touching the preset writes its canonical form
	if m.schedule != "0 10 * * 1-5" {
		t.Errorf("schedule = %q", m.schedule)
	}
}

func TestEditUnmatchedScheduleIsCustom(t *testing.T) {
	j := crontab.Job{ID: "abc234", Schedule: "@reboot", Command: "x", Managed: true}
	m := Edit(config(), j)
	if m.kind != cronexpr.Custom || m.raw.Value() != "@reboot" || m.desc != "At boot" {
		t.Errorf("kind %v raw %q desc %q", m.kind, m.raw.Value(), m.desc)
	}
}

func TestTrackingOptions(t *testing.T) {
	m := newEditor()
	if m.canTrack != svc.WrapNew {
		t.Errorf("new job canTrack = %v", m.canTrack)
	}
	j := crontab.Job{ID: "abc234", Schedule: "@daily", Command: "x", Managed: true, Wrapped: true, Timeout: 30 * time.Minute, NoOverlap: true}
	m = Edit(config(), j)
	if d := m.draft(); !d.Wrapped || d.Timeout != 30*time.Minute || !d.NoOverlap {
		t.Errorf("draft = %+v", d)
	}
	// Name → preset, params, command, tags, track, timeout.
	press(t, &m, "tab", "tab", "tab", "tab", "tab", "tab")
	if m.focus != stopTimeout {
		t.Fatalf("focus = %v", m.focus)
	}
	press(t, &m, "x")
	if req := press(t, &m, "ctrl+s"); req == nil || m.focus != stopTimeout {
		t.Errorf("bad timeout saved: %#v", req)
	}
	press(t, &m, "backspace", "backspace", "backspace", "backspace", "1h")
	press(t, &m, "up", "space") // tracking off: options drop out
	d := m.draft()
	if d.Wrapped || d.Timeout != 0 || d.NoOverlap {
		t.Errorf("draft = %+v", d)
	}
	if _, ok := press(t, &m, "ctrl+s").(SaveMsg); !ok {
		t.Error("can't save")
	}
}

func TestSaveBlocksOnErrors(t *testing.T) {
	m := newEditor()
	req := press(t, &m, "ctrl+s")
	if _, ok := req.(BlockedMsg); !ok || m.focus != stopCommand {
		t.Fatalf("req %#v, focus %v", req, m.focus)
	}
	if !strings.Contains(ansi.Strip(m.View()), "Enter the command to run.") {
		t.Error("the empty-command error isn't shown after a save attempt")
	}
	press(t, &m, "~/bin/backup.sh --all")
	req = press(t, &m, "ctrl+s")
	save, ok := req.(SaveMsg)
	if !ok || save.ID != "" || save.Draft.Name != "backup.sh" || save.Draft.Command != "~/bin/backup.sh --all" || save.Draft.Schedule != "0 9 * * *" {
		t.Errorf("save = %#v", req)
	}
	m.SetSaving(true)
	if req := press(t, &m, "ctrl+s"); req != nil {
		t.Errorf("saved twice: %#v", req)
	}
}

func TestEscReportsDirty(t *testing.T) {
	m := newEditor()
	if req := press(t, &m, "esc"); req != (CancelMsg{Dirty: false}) {
		t.Errorf("clean esc = %#v", req)
	}
	press(t, &m, "x")
	if req := press(t, &m, "esc"); req != (CancelMsg{Dirty: true}) {
		t.Errorf("dirty esc = %#v", req)
	}
	press(t, &m, "backspace")
	if m.Dirty() {
		t.Error("dirty after undoing the edit")
	}
}

func TestPathLintIsDebounced(t *testing.T) {
	m := newEditor()
	press(t, &m, "tab", "tab", "tab")
	if m.focus != stopCommand {
		t.Fatalf("focus = %v", m.focus)
	}
	var cmd tea.Cmd
	for _, c := range "pg_dump" {
		m, cmd, _ = m.Update(tea.KeyPressMsg{Code: c, Text: string(c)})
	}
	if cmd == nil {
		t.Fatal("typing schedules no lint")
	}
	seq := m.pathSeq

	// A stale timer does nothing; the latest one runs the lint.
	if _, cmd, _ := m.Update(lintDueMsg{seq - 1}); cmd != nil {
		t.Error("stale timer ran the lint")
	}
	_, cmd, _ = m.Update(lintDueMsg{seq})
	if cmd == nil {
		t.Fatal("no lint for the current command")
	}
	res := cmd().(pathLintMsg)

	// A result for an older command is dropped.
	m, _, _ = m.Update(pathLintMsg{seq: seq - 1, issues: res.issues})
	if len(m.visibleIssues()) != 0 {
		t.Errorf("stale result shown: %v", m.visibleIssues())
	}
	m, _, _ = m.Update(res)
	if is := m.visibleIssues(); len(is) != 1 || is[0].Code != svc.CodeNotOnCronPath {
		t.Errorf("issues = %v", is)
	}
	// Editing the command hides the result until the next lint.
	press(t, &m, "x")
	if len(m.visibleIssues()) != 0 {
		t.Errorf("outdated result shown: %v", m.visibleIssues())
	}
}

func TestNarrowEditorScrollsToFocus(t *testing.T) {
	m := New(config())
	m.SetSize(70, 10)
	if m.Modal() {
		t.Fatal("modal on a small screen")
	}
	press(t, &m, "tab", "tab", "tab", "tab")
	if m.focus != stopTags {
		t.Fatalf("focus = %v", m.focus)
	}
	view := ansi.Strip(m.View())
	if got := strings.Count(view, "\n") + 1; got != 10 {
		t.Errorf("height %d, want 10", got)
	}
	if !strings.Contains(view, "Tags") || strings.Contains(view, "Name ") {
		t.Errorf("focused row not scrolled into view:\n%s", view)
	}
}
