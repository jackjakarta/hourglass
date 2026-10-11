package editor

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/cronexpr"
	svc "github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/tui/components"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

const (
	labelWidth = 10
	gutter     = 1 + labelWidth // indent + label
	// modalMin is the narrowest screen the editor floats over as a modal;
	// below it the editor fills the width.
	modalMin = 92
	maxWidth = 96
)

// Width is the editor panel's width.
func (m Model) Width() int {
	if m.width >= modalMin {
		return min(m.width-4, maxWidth)
	}
	return m.width
}

// setInputWidths fits the text inputs to the value column.
func (m *Model) setInputWidths() {
	vw := max(m.Width()-4-gutter, 10)
	m.name.SetWidth(vw - 1)
	m.raw.SetWidth(vw - 1)
	m.command.SetWidth(vw - 1)
	m.tags.SetWidth(vw - 1)
	m.timeout.SetWidth(7) // plus a cell for the cursor
}

// Modal reports whether the editor fits as a box over the jobs view; if
// not, the app gives it the whole body.
func (m Model) Modal() bool {
	lines, _ := m.lines()
	return m.width >= modalMin && len(lines)+2 <= m.height
}

// View renders the editor panel: content height when Modal, else the
// full height with the focused row scrolled into view.
func (m Model) View() string {
	lines, focusLine := m.lines()
	h := len(lines) + 2
	if !m.Modal() {
		h = m.height
		if avail := h - 2; len(lines) > avail && avail > 0 {
			off := 0
			if focusLine >= avail-2 {
				off = min(focusLine-(avail-3), len(lines)-avail)
			}
			lines = lines[off : off+avail]
		}
	}
	title := "New job"
	if !m.IsNew() {
		title = "Edit · " + m.editTitle
	}
	return components.Panel(m.th, title, lines, m.Width(), h, true)
}

// lines renders the form and returns the index of the focused row.
func (m Model) lines() ([]string, int) {
	th := m.th
	iw := m.Width() - 4
	vw := max(iw-gutter, 10)
	var out []string
	focusLine := 0
	add := func(s ...string) { out = append(out, s...) }
	row := func(label string, at stop, values ...string) {
		for i, v := range values {
			l := ""
			if i == 0 {
				l = label
				if at == m.focus || (at == stopPreset && (m.focus == stopParams || m.focus == stopRaw)) {
					focusLine = len(out)
				}
			}
			add(" " + m.label(l, m.owns(at)) + v)
		}
	}
	cont := func(values ...string) { row("", -1, values...) }

	add("")
	row("Name", stopName, m.name.View())
	add("")

	row("Schedule", stopPreset, m.carousel(vw))
	if m.kind == cronexpr.Custom {
		cont(m.raw.View())
		if m.focus == stopRaw {
			focusLine = len(out) - 1
		}
		field := cronexpr.FieldAt(m.raw.Value(), m.raw.Position())
		cont(m.ruler(field))
		cont(th.Muted.Render(components.Fit(cronexpr.FieldHelp(field), vw)))
		cont(m.validity(vw)...)
	} else {
		cont(m.paramsRow())
		if m.focus == stopParams {
			focusLine = len(out) - 1
		}
		sched := th.Schedule.Render(m.schedule)
		pad := max(20-lipgloss.Width(m.schedule), 2)
		v := m.validity(vw - lipgloss.Width(m.schedule) - pad)
		cont(sched + strings.Repeat(" ", pad) + v[0])
		cont(v[1:]...)
	}
	cont(m.nextRow(vw))
	if m.kind == cronexpr.Custom && m.lit != cronexpr.Custom {
		cont(th.Muted.Render("matches ") + th.Schedule.Render(m.lit.String()) +
			th.Muted.Render(" · ↑ then ←→ to switch"))
	}
	add("")

	row("Command", stopCommand, m.command.View())
	row("Tags", stopTags, m.tags.View())
	add("")

	row("Options", stopTrack, m.options())
	if m.focus == stopTimeout || m.focus == stopNoOverlap {
		focusLine = len(out) - 1
	}
	if !m.canTrack && m.wrapErr != nil {
		for _, l := range wrap(capitalize(m.wrapErr.Error())+".", vw) {
			cont(th.Muted.Render(l))
		}
	}

	issues := m.visibleIssues()
	if m.timeoutErr != "" {
		issues = append([]svc.Issue{{Field: -1, Issue: cronexpr.Issue{Severity: cronexpr.Error, Message: m.timeoutErr}}}, issues...)
	}
	if len(issues) > 0 {
		add("")
		for _, is := range issues {
			glyph, st := theme.GlyphInfo, th.Muted
			switch is.Severity {
			case cronexpr.Error:
				glyph, st = theme.GlyphFail, th.Fail
			case cronexpr.Warn:
				glyph, st = theme.GlyphWarn, th.Warn
			}
			for i, l := range wrap(is.Message, iw-4) {
				prefix := "   "
				if i == 0 {
					prefix = " " + st.Render(glyph) + " "
				}
				text := th.Text
				if is.Severity == cronexpr.Info {
					text = th.Muted
				}
				add(prefix + text.Render(l))
			}
		}
	}

	add("")
	var hint string
	if m.saving {
		hint = th.Muted.Render("Saving…")
	} else {
		hint = th.Key.Render("esc") + th.Muted.Render(" cancel · ") + th.Key.Render("^t") + th.Muted.Render(" test · ") +
			th.Key.Render("^s") + th.Muted.Render(" save")
	}
	add(lipgloss.PlaceHorizontal(iw, lipgloss.Right, hint+" "))
	return out, focusLine
}

// owns reports whether the row labelled at contains the focus.
func (m Model) owns(at stop) bool {
	switch at {
	case stopPreset:
		return m.focus == stopPreset || m.focus == stopParams || m.focus == stopRaw
	case stopTrack:
		return m.focus == stopTrack || m.focus == stopTimeout || m.focus == stopNoOverlap
	}
	return at == m.focus
}

func (m Model) label(s string, focused bool) string {
	st := m.th.Label
	if focused {
		st = m.th.AccentText.Bold(true)
	}
	return st.Render(components.Fit(s, labelWidth))
}

func (m Model) carousel(vw int) string {
	th := m.th
	focused := m.focus == stopPreset
	arrow := th.Muted
	if focused {
		arrow = th.Key
	}
	style := func(k cronexpr.PresetKind) lipgloss.Style {
		switch {
		case k == m.kind && focused:
			return th.AccentText.Bold(true).Underline(true)
		case k == m.kind:
			return th.Title
		case m.kind == cronexpr.Custom && k == m.lit:
			return th.Schedule.Underline(true)
		}
		return th.Muted
	}
	var items []string
	for k := range cronexpr.PresetKind(len(cronexpr.PresetNames)) {
		items = append(items, style(k).Render(k.String()))
	}
	full := arrow.Render("‹ ") + strings.Join(items, th.Muted.Render(" · ")) + arrow.Render(" ›")
	if lipgloss.Width(full) <= vw {
		return full
	}
	return arrow.Render("‹ ") + style(m.kind).Render(m.kind.String()) + arrow.Render(" ›") +
		th.Muted.Render(fmt.Sprintf("  %d/%d", m.kind+1, len(cronexpr.PresetNames)))
}

func (m Model) paramsRow() string {
	th := m.th
	ps := m.params()
	sub := min(m.sub, len(ps)-1)
	num := func(i int, pad bool) string {
		v := fmt.Sprint(*ps[i].val)
		if pad {
			v = fmt.Sprintf("%02d", *ps[i].val)
		}
		st := th.Text.Bold(true)
		if m.focus == stopParams && i == sub {
			st = th.ButtonFocused.Padding(0)
		}
		return th.Muted.Render("[") + st.Render(v) + th.Muted.Render("]")
	}
	hhmm := func(i int) string { return num(i, true) + th.Text.Render(":") + num(i+1, true) }
	text := th.Text.Render
	switch m.kind {
	case cronexpr.EveryNMin:
		return text("Every ") + num(0, false) + text(" minutes")
	case cronexpr.Hourly:
		return text("Every hour at  ") + text(":") + num(0, true)
	case cronexpr.Daily:
		return text("Daily at  ") + hhmm(0)
	case cronexpr.Weekdays:
		return text("Mon–Fri at  ") + hhmm(0)
	case cronexpr.Weekly:
		var chips []string
		for i, d := range weekOrder {
			on := m.p.Days&(1<<d) != 0
			st := th.Muted
			switch {
			case m.focus == stopParams && i == sub && on:
				st = th.ButtonFocused.Padding(0)
			case m.focus == stopParams && i == sub:
				st = th.Button.Padding(0)
			case on:
				st = th.AccentText.Bold(true)
			}
			chips = append(chips, st.Render(dayLetters[d]))
		}
		return strings.Join(chips, " ") + text("  at  ") + hhmm(7)
	case cronexpr.Monthly:
		return text("On day ") + num(0, false) + text(" of the month at  ") + hhmm(1)
	}
	return ""
}

var dayLetters = []string{"S", "M", "T", "W", "T", "F", "S"}

func (m Model) ruler(field int) string {
	var parts []string
	for i, l := range cronexpr.FieldLabels {
		st := m.th.Muted
		if i == field {
			st = m.th.AccentText.Bold(true).Underline(true)
		}
		parts = append(parts, st.Render(l))
	}
	return strings.Join(parts, "  ")
}

// validity is ✓ and the description, or ✗ and the parse error, wrapped:
// the first line fits w, the rest the full value column.
func (m Model) validity(w int) []string {
	th := m.th
	if m.exprErr != nil {
		msg := m.exprErr.Error()
		if strings.TrimSpace(m.schedule) == "" {
			msg = "Enter a schedule"
		}
		return []string{th.Fail.Render(theme.GlyphFail + " " + components.Fit(msg, max(w-2, 1)))}
	}
	return []string{th.OK.Render(theme.GlyphOK+" ") + th.Text.Render(components.Fit(m.desc, max(w-2, 1)))}
}

func (m Model) nextRow(vw int) string {
	th := m.th
	label := th.Muted.Render("next  ")
	switch {
	case m.expr == nil:
		return label + th.Muted.Render("—")
	case m.expr.Reboot:
		return label + th.Text.Render("at boot")
	case len(m.next) == 0:
		return label + th.Muted.Render("never: no date matches this schedule")
	}
	var parts []string
	for _, t := range m.next {
		layout := "Mon 15:04"
		if t.Sub(m.now) > 6*24*time.Hour {
			layout = "Mon 2 Jan 15:04"
		}
		parts = append(parts, th.Text.Render(t.Format(layout)))
	}
	return components.Fit(label+strings.Join(parts, th.Muted.Render(" · ")), vw)
}

func (m Model) options() string {
	th := m.th
	box := func(on bool, at stop, label string) string {
		mark := "[ ]"
		if on {
			mark = "[x]"
		}
		switch {
		case !m.canTrack || (at != stopTrack && !m.track):
			return th.Muted.Render(mark + " " + label)
		case m.focus == at:
			return th.ButtonFocused.Padding(0).Render(mark) + " " + th.Text.Render(label)
		}
		return th.Text.Render(mark+" ") + th.Text.Render(label)
	}
	timeout := m.timeout.View()
	if !m.canTrack || !m.track {
		timeout = th.Muted.Render(components.Fit(m.timeout.Value(), 8))
	}
	tl := th.Text.Render("Timeout ")
	if !m.canTrack || !m.track {
		tl = th.Muted.Render("Timeout ")
	} else if m.focus == stopTimeout {
		tl = th.AccentText.Render("Timeout ")
	}
	return box(m.canTrack && m.track, stopTrack, "Track runs") + "   " +
		tl + th.Muted.Render("[") + components.Fit(timeout, 8) + th.Muted.Render("]") + "   " +
		box(m.canTrack && m.track && m.overlap, stopNoOverlap, "Skip if still running")
}

func wrap(s string, w int) []string {
	return strings.Split(lipgloss.NewStyle().Width(max(w, 1)).Render(s), "\n")
}

// Hints are the footer's key hints for the focused control.
func (m Model) Hints() []key.Binding {
	h := func(k, desc string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, desc)) }
	out := []key.Binding{h("tab", "next")}
	switch m.focus {
	case stopPreset:
		out = append(out, h("←→", "preset"))
	case stopParams:
		out = append(out, h("←→", "move"))
		if m.kind == cronexpr.Weekly && m.sub < 7 {
			out = append(out, h("space", "toggle day"))
		} else {
			out = append(out, h("0-9", "type"), h("+/-", "adjust"))
		}
	case stopTrack, stopNoOverlap:
		out = append(out, h("space", "toggle"))
	}
	return append(out, h("^s", "save"), h("^t", "test"), h("esc", "cancel"))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
