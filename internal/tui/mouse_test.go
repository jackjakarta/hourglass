package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// find returns the screen cell where s first appears, after waiting for it.
func (hs *harness) find(s string) (x, y int) {
	hs.t.Helper()
	hs.waitFor(s)
	for y, line := range strings.Split(hs.screenV.Load().(string), "\n") {
		if i := strings.Index(line, s); i >= 0 {
			return lipgloss.Width(line[:i]), y
		}
	}
	hs.t.Fatalf("%q vanished from the screen", s)
	return 0, 0
}

// findOn is find, on the first line that also contains other.
func (hs *harness) findOn(s, other string) (x, y int) {
	hs.t.Helper()
	hs.waitFor(other)
	for y, line := range strings.Split(hs.screenV.Load().(string), "\n") {
		if i := strings.Index(line, s); i >= 0 && strings.Contains(line, other) {
			return lipgloss.Width(line[:i]), y
		}
	}
	hs.t.Fatalf("no line with %q and %q", s, other)
	return 0, 0
}

func (hs *harness) click(x, y int) {
	hs.tm.Send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func (hs *harness) clickOn(s string) {
	hs.t.Helper()
	x, y := hs.find(s)
	hs.click(x+1, y)
}

func (hs *harness) wheel(x, y int, down bool) {
	b := tea.MouseWheelUp
	if down {
		b = tea.MouseWheelDown
	}
	hs.tm.Send(tea.MouseWheelMsg{X: x, Y: y, Button: b})
}

func TestTabAt(t *testing.T) {
	m := New(context.Background(), Options{})
	m.width, m.now = 120, testNow
	header := ansi.Strip(m.header())
	for i, name := range tabNames {
		x := lipgloss.Width(header[:strings.Index(header, name)])
		for _, dx := range []int{0, len(name) - 1} {
			if got, ok := tabAt(x + dx); !ok || got != tab(i) {
				t.Errorf("tabAt(%d) = %v, %v; want %s", x+dx, got, ok, name)
			}
		}
		if _, ok := tabAt(x - 1); ok {
			t.Errorf("the gap before %s is a tab", name)
		}
	}
}

func TestMouseTabsAndRows(t *testing.T) {
	hs := start(t, "realistic_mixed", 100, 32)
	hs.waitFor("Nightly backup")

	hs.clickOn("History")
	hs.waitFor("f failures")
	hs.clickOn("Jobs  ")
	hs.waitFor("UNMANAGED")

	// A click selects; a second click on the selected row opens it.
	hs.clickOn("Nightly backup")
	hs.waitFor("▌● Nightly backup")
	hs.clickOn("Nightly backup")
	hs.waitFor("At 02:30, every day")

	// The wheel moves the selection.
	hs.keys("esc")
	hs.waitFor("UNMANAGED")
	hs.wheel(10, 10, true)
	hs.waitFor("▌● Say hi")
	hs.wheel(10, 10, false)
	hs.wheel(10, 10, false)
	hs.waitFor("▌● Rotate logs")

	// Clicking the filter line starts filtering.
	hs.clickOn("/ filter…")
	hs.keys("dock")
	hs.waitFor("/ dock█")
}

func TestMouseConfirmButtons(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	orig := hs.read()

	hs.keys("x")
	x, y := hs.findOn("Cancel", "Delete")
	hs.click(x+2, y)
	time.Sleep(50 * time.Millisecond)
	if hs.read() != orig {
		t.Fatal("clicking Cancel deleted the job")
	}

	hs.keys("x")
	x, y = hs.findOn("Delete", "Cancel")
	hs.click(x-1, y) // the button's padding counts
	hs.waitFor(`Deleted "Sync photos"`)
	if strings.Contains(hs.read(), "s7nc0p") {
		t.Errorf("job still in the crontab:\n%s", hs.read())
	}
}

func TestMousePalette(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	hs.keys("ctrl+k", "go to timeline")
	hs.clickOn("Go to Timeline")
	hs.waitFor("Timeline · next 24h")

	// A click outside closes it.
	hs.keys("ctrl+k")
	hs.waitFor("Type a command")
	hs.click(1, 30)
	time.Sleep(50 * time.Millisecond)
	if m := hs.final(); m.palette != nil {
		t.Error("the palette is still open")
	}
}

func TestMouseTimelineAndCrontab(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	hs.keys("2")
	hs.waitFor("Timeline · next 24h")
	_, y := hs.find("Rotate logs")
	hs.click(5, y)
	hs.click(5, y) // the selected lane opens its job
	hs.waitFor("▌● Rotate logs")
	if m := hs.final(); m.tab != tabJobs {
		t.Errorf("tab = %v, want Jobs", m.tab)
	}
}

func TestMouseHistoryWheel(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	hs.keys("3")
	hs.waitFor("f failures")
	hs.wheel(10, 10, true) // nothing to scroll: no panic, nothing happens
	hs.keys("4")
	hs.waitFor("Current crontab")
	hs.wheel(10, 10, true) // the preview scrolls
	hs.wheel(100, 10, true)
	if m := hs.final(); m.tab != tabCrontab {
		t.Errorf("tab = %v", m.tab)
	}
}
