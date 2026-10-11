package tui

import (
	"strings"
	"testing"
)

func TestGoldenTimelineDay(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	hs.keys("2")
	hs.waitFor("Timeline · next 24h")
	requireGolden(t, hs.screen())
}

func TestGoldenTimelineWeek(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	hs.keys("2", "w")
	hs.waitFor("Timeline · next 7 days")
	requireGolden(t, hs.screen())
}

func TestGoldenTimelineEmpty(t *testing.T) {
	hs := start(t, "inline:# gocron:job id=aaaaaa name=Paused\n#gocron:off 0 3 * * * x\n", 100, 20)
	hs.waitFor("Paused")
	hs.keys("2")
	hs.waitFor("No enabled jobs")
	requireGolden(t, hs.screen())
}

func TestGoldenTimelineRuns(t *testing.T) {
	hs := startRuns(t, 140, 32, nil)
	hs.waitFor("Nightly backup")
	hs.keys("2")
	hs.waitFor("Timeline · next 24h")
	requireGolden(t, hs.screen())
}

func TestTimelineScrollAndOpen(t *testing.T) {
	hs := start(t, "realistic_mixed", 100, 32)
	hs.waitFor("Nightly backup")
	hs.keys("2", "right")
	hs.waitFor("Timeline · Sun 20:32 – Mon 20:32")
	hs.keys(".")
	hs.waitFor("Timeline · next 24h")
	hs.keys("down", "enter")
	hs.waitFor("COMMAND") // the job's detail, in the narrow layout
	if s := hs.screen(); !strings.Contains(s, "Jobs") {
		t.Errorf("screen:\n%s", s)
	}
}
