package timeline

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/store"
	"github.com/jackjakarta/gocron/internal/tui/keys"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// Sun 11 Oct 2026 14:32:07 UTC
var now = time.Date(2026, 10, 11, 14, 32, 7, 0, time.UTC)

func parse(t *testing.T, tab string) []jobs.Job {
	t.Helper()
	var out []jobs.Job
	for _, j := range crontab.Parse([]byte(tab)).Jobs() {
		out = append(out, jobs.Job{Job: j})
	}
	return out
}

func day(js []jobs.Job, cols, threshold int) Grid {
	from := now.Truncate(time.Minute)
	return Build(js, nil, from, from.Add(24*time.Hour), now, cols, threshold)
}

func TestBuildBandsAndMarks(t *testing.T) {
	js := parse(t, "* * * * * every-minute\n*/15 * * * * quarter\n")
	g := day(js, 80, 0)
	if l := g.Lanes[0]; !l.Band || l.Fires != 1439 {
		t.Errorf("every minute: band %v fires %d", l.Band, l.Fires)
	}
	if l := g.Lanes[1]; !l.Band || l.Fires != 96 {
		t.Errorf("*/15 at 80 columns: band %v fires %d", l.Band, l.Fires)
	}
	if l := day(js, 200, 0).Lanes[1]; l.Band {
		t.Errorf("*/15 at 200 columns is a band (%d fires)", l.Fires)
	}
}

func TestBuildSparseRebootAndPaused(t *testing.T) {
	js := parse(t, "0 4 * * 0 weekly\n@reboot boot\n#gocron:off 0 * * * * paused\n# gocron:job id=aaaaaa name=Off\n#gocron:off 0 * * * * off\n0 0 30 2 * never\n")
	g := day(js, 80, 0)
	if len(g.Lanes) != 3 {
		t.Fatalf("lanes = %d, want weekly, boot and never (paused left out)", len(g.Lanes))
	}
	if l := g.Lanes[0]; l.Fires != 0 || !l.Next.Equal(time.Date(2026, 10, 18, 4, 0, 0, 0, time.UTC)) {
		t.Errorf("weekly: fires %d next %v", l.Fires, l.Next)
	}
	if l := g.Lanes[1]; !l.Reboot || l.Fires != 0 {
		t.Errorf("boot: reboot %v fires %d", l.Reboot, l.Fires)
	}
	if l := g.Lanes[2]; l.Fires != 0 || !l.Next.IsZero() {
		t.Errorf("never: fires %d next %v", l.Fires, l.Next)
	}
}

func TestBuildEmpty(t *testing.T) {
	g := day(nil, 80, 3)
	if len(g.Lanes) != 0 || len(g.Congestion) != 0 || len(g.Density) != 80 {
		t.Errorf("empty grid = %+v", g)
	}
}

func TestBuildCongestion(t *testing.T) {
	js := parse(t, "0 0 * * * a\n0 0 * * * b\n0 0 * * * c\n30 0 * * * d\n30 0 * * * e\n")
	g := day(js, 80, 3)
	if len(g.Congestion) != 1 || g.Congestion[0].Jobs != 3 || !g.Congestion[0].At.Equal(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("threshold 3: %+v", g.Congestion)
	}
	if g := day(js, 80, 4); len(g.Congestion) != 0 {
		t.Errorf("threshold 4: %+v", g.Congestion)
	}
	if g := day(js, 80, 2); len(g.Congestion) != 2 {
		t.Errorf("threshold 2: %+v", g.Congestion)
	}
	c := g.Col(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC))
	if g.Density[c] != 3 {
		t.Errorf("density at 00:00 = %d", g.Density[c])
	}
}

func TestBuildCronTZ(t *testing.T) {
	js := parse(t, "CRON_TZ=Asia/Tokyo\n0 0 * * * tokyo\n")
	g := day(js, 24*60, 0)                                 // a column per minute
	want := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC) // 00:00 JST
	if c := g.Col(want); g.Lanes[0].Cells[c] != 1 {
		t.Errorf("the Tokyo job doesn't fire at 15:00 UTC: cells %v", g.Lanes[0].Fires)
	}
}

func TestBuildFallBack(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 1 Nov 2026: 01:00–01:59 happens twice in New York. An hourly job
	// fires at 01:00 in both passes: 25 fires in 25 hours.
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	to := from.Add(25 * time.Hour)
	js := parse(t, "0 * * * * hourly\n")
	g := Build(js, nil, from, to, from.Add(-time.Hour), 25, 0)
	for c, n := range g.Lanes[0].Cells {
		if n != 1 {
			t.Errorf("column %d has %d fires, want one an hour", c, n)
		}
	}
}

func TestBuildWeekColumnsAndPast(t *testing.T) {
	js := parse(t, "# gocron:job id=aaaaaa name=Daily\n0 3 * * * daily\n")
	from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	stats := map[string]store.JobStats{"aaaaaa": {Recent: []store.Run{
		{StartedAt: time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC), Status: store.StatusFailed},
		{StartedAt: time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC), Status: store.StatusSuccess}, // before the window
	}}}
	g := Build(js, stats, from, from.AddDate(0, 0, 7), now, 7, 0)
	l := g.Lanes[0]
	// Today's 03:00 has passed; the other six days each fire once.
	if want := []int{0, 1, 1, 1, 1, 1, 1}; !equalInts(l.Cells, want) {
		t.Errorf("cells = %v, want %v", l.Cells, want)
	}
	if len(l.Past) != 1 || l.Past[0] != (PastMark{0, store.StatusFailed}) {
		t.Errorf("past = %+v", l.Past)
	}
}

func equalInts(a, b []int) bool {
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

func TestViewKeys(t *testing.T) {
	m := New(theme.Mono(), keys.Default(), 3)
	m.SetNow(now)
	m.SetSize(120, 30)
	m.SetSnapshot(jobs.Snapshot{Jobs: parse(t, "0 3 * * * a\n0 4 * * * b\n")})
	press := func(k tea.KeyPressMsg) tea.Msg {
		var req tea.Msg
		m, req = m.Update(k)
		return req
	}
	press(tea.KeyPressMsg{Code: tea.KeyDown})
	if req, ok := press(tea.KeyPressMsg{Code: tea.KeyEnter}).(OpenMsg); !ok || req.Job.Command != "b" {
		t.Errorf("enter = %#v", req)
	}
	press(tea.KeyPressMsg{Code: 'w', Text: "w"})
	if m.Mode() != ModeWeek || !strings.Contains(ansi.Strip(m.View()), "next 7 days") {
		t.Error("w didn't switch to the week")
	}
	press(tea.KeyPressMsg{Code: tea.KeyLeft}) // can't scroll before now
	press(tea.KeyPressMsg{Code: tea.KeyRight})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Mon 12 Oct – Sun 18 Oct") || strings.Contains(v, "▼ now") {
		t.Errorf("after scrolling a day:\n%s", v)
	}
	press(tea.KeyPressMsg{Code: '.', Text: "."})
	if !strings.Contains(ansi.Strip(m.View()), "▼ now") {
		t.Error(". didn't go back to now")
	}
}
