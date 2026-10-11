package tui

import (
	"encoding/base64"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/jackjakarta/gocron/internal/tui/components"
)

// paletteModel builds a Model for realistic_mixed without running it.
func paletteModel(t *testing.T) Model {
	t.Helper()
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	if err := hs.tm.Quit(); err != nil {
		t.Fatal(err)
	}
	return hs.tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(spy).Model
}

func TestPaletteCoversActions(t *testing.T) {
	m := paletteModel(t)
	acts := m.paletteItems()
	hints := map[string]bool{}
	titles := map[string]bool{}
	var items []components.PaletteItem
	for _, a := range acts {
		hints[a.item.Hint] = true
		titles[a.item.Title] = true
		items = append(items, a.item)
	}
	km := m.keys
	for _, b := range []struct {
		name string
		hint string
	}{
		{"edit", hintOf(km.Edit)}, {"toggle", hintOf(km.Toggle)}, {"run", hintOf(km.Run)}, {"test", hintOf(km.Test)},
		{"delete", hintOf(km.Delete)}, {"copy", hintOf(km.Copy)}, {"copy schedule", hintOf(km.CopySchedule)},
		{"new", hintOf(km.New)}, {"undo", hintOf(km.Undo)}, {"filter", hintOf(km.Filter)}, {"tag", hintOf(km.TagFilter)},
		{"sort", hintOf(km.Sort)}, {"failures", hintOf(km.Failures)}, {"trigger", hintOf(km.Trigger)},
		{"tab1", hintOf(km.Tab1)}, {"tab2", hintOf(km.Tab2)}, {"tab3", hintOf(km.Tab3)}, {"tab4", hintOf(km.Tab4)},
		{"help", hintOf(km.Help)}, {"quit", hintOf(km.Quit)},
	} {
		if !hints[b.hint] {
			t.Errorf("no palette item for %s (%s)", b.name, b.hint)
		}
	}
	for _, j := range m.snap.Jobs {
		if !titles["Go to job · "+clip(j.Title(), 50)] {
			t.Errorf("no Go to job item for %q", j.Title())
		}
		if j.Managed && !titles["History · "+clip(j.Title(), 50)] {
			t.Errorf("no History item for %q", j.Title())
		}
	}
	// Typing an item's whole title finds it first.
	for _, it := range items {
		p := components.NewPalette("", "", items)
		for _, r := range it.Title {
			p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
		if got := p.Matches(); len(got) == 0 || got[0].Title != it.Title {
			first := ""
			if len(got) > 0 {
				first = got[0].Title
			}
			t.Errorf("typing %q ranks %q first", it.Title, first)
		}
	}
}

func TestPalettePause(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	hs.keys("ctrl+k")
	hs.waitFor("Type a command or a job")
	hs.keys("pause", "enter")
	hs.waitFor(`Paused "Sync photos"`)
	if !strings.Contains(hs.read(), "#gocron:off */15 * * * *") {
		t.Errorf("Sync photos wasn't paused:\n%s", hs.read())
	}
	hs.screen()
}

func TestPaletteGoToJob(t *testing.T) {
	hs := start(t, "realistic_mixed", 100, 32)
	hs.waitFor("Nightly backup")
	hs.keys(":", "go prune", "enter")
	hs.waitFor("docker system prune -af") // the detail opened
	if s := hs.screen(); strings.Contains(s, "Commands") {
		t.Errorf("the palette is still open:\n%s", s)
	}
}

func TestPaletteHistoryOfJob(t *testing.T) {
	hs := startRuns(t, 140, 32, nil)
	hs.waitFor("Nightly backup")
	hs.keys(":", "history nightly", "enter")
	hs.waitFor("job: Nightly backup")
	hs.screen()
}

func TestPaletteEscAndFilterText(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	orig := hs.read()
	hs.keys(":", "quit", "esc")
	hs.waitFor("/ filter…")
	// While filtering, ":" is text, and ctrl+k still opens the palette.
	hs.keys("/", "a:")
	hs.waitFor("/ a:")
	hs.keys("ctrl+k")
	hs.waitFor("Type a command or a job")
	hs.keys("esc")
	s := hs.screen()
	if strings.Contains(s, "Commands") || !strings.Contains(s, "/ a:") {
		t.Errorf("screen:\n%s", s)
	}
	if hs.read() != orig || hs.backupCount() != 0 {
		t.Error("closing the palette changed the crontab")
	}
}

func TestTagPickerSortAndCopy(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	hs.keys("#")
	hs.waitFor("Filter by tag")
	hs.keys("backup", "enter")
	hs.waitFor("#backup")
	hs.keys("esc", "s")
	hs.waitFor("sort: name")
	hs.keys("y")
	hs.waitFor("Copied the command")
	s := hs.screen()
	if !strings.Contains(s, "Jobs · 6") {
		t.Errorf("screen:\n%s", s)
	}
	out, err := io.ReadAll(hs.tm.FinalOutput(t))
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by name, the first job is Nightly backup.
	want := "]52;c;" + base64.StdEncoding.EncodeToString([]byte("pg_dump app | gzip > /bk/app-$(date +%F).sql.gz"))
	if !strings.Contains(string(out), want) {
		t.Errorf("output has no OSC 52 copy of the command")
	}
}
