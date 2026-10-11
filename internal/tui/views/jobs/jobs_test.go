package jobs

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jackjakarta/gocron/internal/crontab"
	svc "github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/tui/keys"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

var now = time.Date(2026, 10, 11, 14, 32, 7, 0, time.UTC)

func model(t *testing.T, tab string) Model {
	t.Helper()
	var snap svc.Snapshot
	for _, j := range crontab.Parse([]byte(tab)).Jobs() {
		snap.Jobs = append(snap.Jobs, svc.Job{Job: j})
	}
	m := New(theme.Mono(), keys.Default())
	m.SetSize(140, 30)
	m.SetSnapshot(snap, now)
	return m
}

func titles(m Model) []string {
	var out []string
	for _, r := range m.rows() {
		out = append(out, r.job.Title())
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const tab = `# gocron:job id=aaaaaa name=Daily tags=ops
0 3 * * * daily
@reboot boot-entry
# gocron:job id=bbbbbb name=Paused
#gocron:off * * * * * paused
# gocron:job id=cccccc name=Hourly
0 * * * * hourly
# gocron:job id=dddddd name=Never
0 0 30 2 * never
# gocron:job id=eeeeee name=Boot
@reboot boot
`

func TestLayoutFor(t *testing.T) {
	for w, want := range map[int]Layout{200: LayoutSplit, 120: LayoutSplit, 119: LayoutList, 80: LayoutList, 79: LayoutCompact, 60: LayoutCompact} {
		if got := LayoutFor(w); got != want {
			t.Errorf("LayoutFor(%d) = %v, want %v", w, got, want)
		}
	}
}

func TestSortByNextRun(t *testing.T) {
	m := model(t, tab)
	// Scheduled jobs by next run, then the rest in crontab order, then
	// unmanaged entries.
	want := []string{"Hourly", "Daily", "Paused", "Never", "Boot", "boot-entry"}
	if got := titles(m); !equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	m.SetNow(now.Add(13 * time.Hour)) // 03:32: Daily's next run is a day away
	if got := titles(m); got[0] != "Hourly" || got[1] != "Daily" {
		t.Errorf("order later = %v", got)
	}
}

func TestFilter(t *testing.T) {
	m := model(t, tab)
	m.filter = "ops"
	m.rebuild()
	if got := titles(m); !equal(got, []string{"Daily"}) {
		t.Errorf("tag filter = %v", got)
	}
	m.filter = "hrly" // subsequence of "hourly"
	m.rebuild()
	if got := titles(m); !equal(got, []string{"Hourly"}) {
		t.Errorf("fuzzy filter = %v", got)
	}
	m.filter = "boot ent"
	m.rebuild()
	if got := titles(m); !equal(got, []string{"boot-entry"}) {
		t.Errorf("multi-word filter = %v", got)
	}
}

func TestSelectionSurvivesReloads(t *testing.T) {
	m := model(t, tab)
	m.move(1) // Daily
	if j, _ := m.Selected(); j.ID != "aaaaaa" {
		t.Fatalf("selected %q", j.Title())
	}
	// Daily moves in the order but stays selected.
	m.SetNow(now.Add(13 * time.Hour))
	if j, _ := m.Selected(); j.ID != "aaaaaa" {
		t.Errorf("selection lost after reorder: %q", j.Title())
	}
	// When the selected job disappears, the selection stays in place.
	m2 := model(t, `# gocron:job id=cccccc name=Hourly
0 * * * * hourly
# gocron:job id=bbbbbb name=Paused
#gocron:off * * * * * paused
`)
	m.SetSnapshot(m2.snap, now)
	if j, _ := m.Selected(); j.ID != "bbbbbb" {
		t.Errorf("after removal selected %q, want Paused", j.Title())
	}
}

func TestRequests(t *testing.T) {
	m := model(t, tab)
	press := func(m Model, k string) (Model, any) {
		return m.Update(keyMsg(k))
	}
	_, req := press(m, "space")
	if r, ok := req.(ToggleMsg); !ok || r.Job.ID != "cccccc" {
		t.Errorf("space = %#v", req)
	}
	if _, req := press(m, "r"); req.(RunMsg).Job.ID != "cccccc" {
		t.Errorf("r = %#v", req)
	}
	if _, req := press(m, "t"); req.(TestMsg).Job.ID != "cccccc" {
		t.Errorf("t = %#v", req)
	}
	if _, req := press(m, "h"); req.(HistoryMsg).Job.ID != "cccccc" {
		t.Errorf("h = %#v", req)
	}
	m, _ = press(m, "G") // the unmanaged entry
	// Unmanaged entries can run (unrecorded) but have no history.
	if _, req := press(m, "r"); req.(RunMsg).Job.Managed {
		t.Errorf("r on unmanaged = %#v", req)
	}
	if _, req := press(m, "h"); req == nil {
		t.Error("h on an unmanaged entry sent nothing")
	} else if _, ok := req.(NeedAdoptMsg); !ok {
		t.Errorf("h on unmanaged = %#v", req)
	}
	if _, req := press(m, "x"); req == nil {
		t.Error("x on an unmanaged entry sent nothing")
	} else if _, ok := req.(NeedAdoptMsg); !ok {
		t.Errorf("x on unmanaged = %#v", req)
	}
	if _, req := press(m, "a"); req == nil {
		t.Error("a sent nothing")
	} else if r, ok := req.(AdoptMsg); !ok || r.Job.Raw != "@reboot boot-entry" {
		t.Errorf("a = %#v", req)
	}
}

func keyMsg(k string) tea.KeyPressMsg {
	if k == "space" {
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	}
	return tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
}
