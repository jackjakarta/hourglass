// Package jobs is the Jobs view: the job list and the detail pane.
package jobs

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	svc "github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/tui/keys"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// Requests the view returns from Update for the app, which owns the
// service, to act on right away.
type (
	ToggleMsg     struct{ Job svc.Job }
	DeleteMsg     struct{ Job svc.Job }
	AdoptMsg      struct{ Job svc.Job }
	NeedAdoptMsg  struct{ Job svc.Job } // an action that needs a managed job
	ComingSoonMsg struct{ What string }
)

// Layout is how the view uses its width (spec §6.2).
type Layout int

const (
	LayoutSplit   Layout = iota // ≥ 120 columns: list | detail
	LayoutList                  // 80–119: list, enter opens the detail
	LayoutCompact               // < 80: compact rows
)

// LayoutFor picks the layout for a width.
func LayoutFor(width int) Layout {
	switch {
	case width >= 120:
		return LayoutSplit
	case width >= 80:
		return LayoutList
	}
	return LayoutCompact
}

// row is one list entry with its next run precomputed.
type row struct {
	job  svc.Job
	next time.Time // zero when paused, @reboot, or never
}

// key identifies a row across reloads.
func (r row) key() string {
	if r.job.Managed {
		return "id:" + r.job.ID
	}
	return "line:" + strconv.Itoa(r.job.Line) + ":" + r.job.Command
}

// Model is the Jobs view.
type Model struct {
	th   *theme.Theme
	keys keys.Map

	snap svc.Snapshot
	now  time.Time

	width, height int
	managed       []row // sorted by next run
	unmanaged     []row // crontab order
	selected      string
	offset        int // first visible list line

	filter    string
	filtering bool
	detail    bool // detail open full-width (list and compact layouts)
}

// New returns an empty Jobs view.
func New(th *theme.Theme, km keys.Map) Model {
	return Model{th: th, keys: km}
}

// SetTheme swaps the theme.
func (m *Model) SetTheme(th *theme.Theme) { m.th = th }

// SetSize sets the area the view draws into.
func (m *Model) SetSize(w, h int) {
	m.width, m.height = w, h
	if LayoutFor(w) == LayoutSplit {
		m.detail = false
	}
	m.clampOffset()
}

// SetSnapshot replaces the jobs, keeping the selection where possible.
func (m *Model) SetSnapshot(s svc.Snapshot, now time.Time) {
	m.snap = s
	m.now = now
	m.rebuild()
}

// SetNow advances the clock: countdowns and the next-run order change.
func (m *Model) SetNow(now time.Time) {
	m.now = now
	m.rebuild()
}

// SelectJob selects the managed job id, if it's listed.
func (m *Model) SelectJob(id string) {
	if k := "id:" + id; m.has(m.rows(), k) {
		m.selected = k
		m.clampOffset()
	}
}

// Capturing reports whether the view wants every key (filter input), so
// global keys like q don't fire.
func (m Model) Capturing() bool { return m.filtering }

// DetailOpen reports whether the full-width detail is showing.
func (m Model) DetailOpen() bool { return m.detail }

// Selected returns the selected job.
func (m Model) Selected() (svc.Job, bool) {
	rows := m.rows()
	i := m.index(rows)
	if i < 0 {
		return svc.Job{}, false
	}
	return rows[i].job, true
}

// rows returns the visible rows: managed first, then unmanaged.
func (m Model) rows() []row {
	return append(append([]row(nil), m.managed...), m.unmanaged...)
}

func (m Model) index(rows []row) int {
	for i, r := range rows {
		if r.key() == m.selected {
			return i
		}
	}
	if len(rows) > 0 {
		return 0
	}
	return -1
}

func (m *Model) rebuild() {
	prev := max(m.index(m.rows()), 0)
	m.managed, m.unmanaged = nil, nil
	for _, j := range m.snap.Jobs {
		if !m.matches(j) {
			continue
		}
		r := row{job: j}
		if j.Enabled && j.Expr != nil && !j.Expr.Reboot {
			if next := j.NextRuns(m.now, 1); len(next) > 0 {
				r.next = next[0]
			}
		}
		if j.Managed {
			m.managed = append(m.managed, r)
		} else {
			m.unmanaged = append(m.unmanaged, r)
		}
	}
	sort.SliceStable(m.managed, func(a, b int) bool {
		ra, rb := m.managed[a], m.managed[b]
		if ra.next.IsZero() != rb.next.IsZero() {
			return !ra.next.IsZero()
		}
		return ra.next.Before(rb.next)
	})
	// Keep the selected job; if it's gone, stay at the same position.
	rows := m.rows()
	switch {
	case len(rows) == 0:
		m.selected = ""
	case !m.has(rows, m.selected):
		m.selected = rows[min(prev, len(rows)-1)].key()
	}
	m.clampOffset()
}

func (m Model) has(rows []row, key string) bool {
	for _, r := range rows {
		if r.key() == key {
			return true
		}
	}
	return false
}

// matches applies the filter: every filter word must be a subsequence of
// the job's name, command or tags.
func (m Model) matches(j svc.Job) bool {
	if m.filter == "" {
		return true
	}
	hay := strings.ToLower(j.Name + " " + j.Command + " " + strings.Join(j.Tags, " ") + " " + j.ID)
	for _, word := range strings.Fields(strings.ToLower(m.filter)) {
		if !subsequence(hay, word) {
			return false
		}
	}
	return true
}

func subsequence(s, sub string) bool {
	for _, r := range sub {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+len(string(r)):]
	}
	return true
}

func (m *Model) move(delta int) {
	rows := m.rows()
	if len(rows) == 0 {
		return
	}
	i := min(max(m.index(rows)+delta, 0), len(rows)-1)
	m.selected = rows[i].key()
	m.clampOffset()
}

// Update handles keys for the view. It returns a request (ToggleMsg, …)
// when a key asks the app to act, or nil.
func (m Model) Update(msg tea.Msg) (Model, tea.Msg) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if m.filtering {
		return m.updateFilter(k), nil
	}
	km := m.keys
	switch {
	case key.Matches(k, km.Up):
		m.move(-1)
	case key.Matches(k, km.Down):
		m.move(1)
	case key.Matches(k, km.Top):
		m.move(-len(m.snap.Jobs))
	case key.Matches(k, km.Bottom):
		m.move(len(m.snap.Jobs))
	case key.Matches(k, km.PageUp):
		m.move(-max(m.listRows()-1, 1))
	case key.Matches(k, km.PageDown):
		m.move(max(m.listRows()-1, 1))
	case key.Matches(k, km.Open):
		if _, ok := m.Selected(); ok && LayoutFor(m.width) != LayoutSplit {
			m.detail = true
		}
	case key.Matches(k, km.Back):
		switch {
		case m.detail:
			m.detail = false
		case m.filter != "":
			m.filter = ""
			m.rebuild()
		}
	case key.Matches(k, km.Filter):
		m.filtering, m.detail = true, false
	case key.Matches(k, km.Toggle):
		return m, m.request(func(j svc.Job) tea.Msg { return ToggleMsg{j} })
	case key.Matches(k, km.Delete):
		return m, m.request(func(j svc.Job) tea.Msg { return DeleteMsg{j} })
	case key.Matches(k, km.Adopt):
		if j, ok := m.Selected(); ok && !j.Managed {
			return m, AdoptMsg{j}
		}
	case key.Matches(k, km.New):
		return m, ComingSoonMsg{"Creating jobs"}
	case key.Matches(k, km.Edit):
		return m, ComingSoonMsg{"Editing jobs"}
	case key.Matches(k, km.Run):
		return m, ComingSoonMsg{"Run now"}
	case key.Matches(k, km.Test):
		return m, ComingSoonMsg{"Test runs"}
	}
	return m, nil
}

// request returns build(selected) for a managed job, or NeedAdoptMsg for
// an unmanaged one.
func (m Model) request(build func(svc.Job) tea.Msg) tea.Msg {
	j, ok := m.Selected()
	switch {
	case !ok:
		return nil
	case !j.Managed:
		return NeedAdoptMsg{j}
	}
	return build(j)
}

func (m Model) updateFilter(k tea.KeyPressMsg) Model {
	switch k.String() {
	case "esc":
		m.filtering, m.filter = false, ""
	case "enter", "down", "up":
		m.filtering = false
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
		}
	case "ctrl+u":
		m.filter = ""
	default:
		if k.Text != "" {
			m.filter += k.Text
		}
	}
	m.rebuild()
	return m
}
