package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		query, text string
		ok          bool
	}{
		{"", "anything", true},
		{"pause", "Pause · Nightly backup", true},
		{"PAUSE", "pause · nightly backup", true},
		{"go prune", "Go to job · Prune docker", true},
		{"prune go", "Go to job · Prune docker", true}, // words in any order
		{"hst nb", "History · Nightly backup", true},
		{"xyz", "Pause · Nightly backup", false},
		{"go zzz", "Go to job · Prune docker", false},
	}
	for _, tt := range tests {
		if _, ok := FuzzyMatch(tt.query, tt.text); ok != tt.ok {
			t.Errorf("FuzzyMatch(%q, %q) ok = %v", tt.query, tt.text, ok)
		}
	}
	// Word starts and adjacent runs beat scattered matches.
	better := [][3]string{
		{"week", "Timeline: next 7 days (week)", "Show weekly report details"},
		{"back", "History · Nightly backup", "Show feedback"},
		{"run", "Run now · Sync photos", "Prune docker"},
		{"tl", "Timeline: next 24 hours", "Go to History"},
	}
	for _, c := range better {
		a, _ := FuzzyMatch(c[0], c[1])
		b, okb := FuzzyMatch(c[0], c[2])
		if okb && a <= b {
			t.Errorf("%q: %q scored %d, not above %q (%d)", c[0], c[1], a, c[2], b)
		}
	}
}

func TestPalette(t *testing.T) {
	items := []PaletteItem{{"Go to Jobs", "1"}, {"Pause · Nightly backup", "space"}, {"Go to job · Prune docker", ""}, {"Quit", "q"}}
	p := NewPalette("Commands", "Type a command…", items)
	if got := len(p.Matches()); got != 4 {
		t.Fatalf("empty query lists %d items", got)
	}
	typ := func(s string) {
		for _, r := range s {
			p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	typ("prune")
	if m := p.Matches(); len(m) != 1 || m[0].Title != "Go to job · Prune docker" {
		t.Errorf("prune matches %v", m)
	}
	if !strings.Contains(ansi.Strip(p.View(theme.Mono(), 80)), "Go to job · Prune docker") {
		t.Error("the view doesn't list the match")
	}
	if chosen, done := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); !done || chosen != 2 {
		t.Errorf("enter chose %d (done %v)", chosen, done)
	}
	p = NewPalette("Commands", "", items)
	p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if chosen, _ := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); chosen != 1 {
		t.Errorf("down, enter chose %d", chosen)
	}
	typ("zzz")
	if chosen, done := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); !done || chosen != -1 {
		t.Errorf("enter with no match chose %d", chosen)
	}
	if chosen, done := p.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !done || chosen != -1 {
		t.Errorf("esc chose %d (done %v)", chosen, done)
	}
}
