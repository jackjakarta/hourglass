package components

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jackjakarta/gocron/internal/store"
	"github.com/jackjakarta/gocron/internal/tui/theme"
)

func TestRunStripMono(t *testing.T) {
	var runs []store.Run
	for _, s := range []string{store.StatusSuccess, store.StatusFailed, store.StatusTimeout, store.StatusSkipped, store.StatusLost, store.StatusRunning} {
		runs = append(runs, store.Run{Status: s})
	}
	if got := ansi.Strip(RunStrip(theme.Mono(), runs)); got != "■×!·?◐" {
		t.Errorf("mono strip = %q", got)
	}
	if got := ansi.Strip(RunStrip(theme.Nightshift(true), runs)); got != "■■■■■■" {
		t.Errorf("color strip = %q", got)
	}
}

func TestSparkline(t *testing.T) {
	got := ansi.Strip(Sparkline(theme.Mono(), []time.Duration{0, time.Second, 3 * time.Second, 6 * time.Second}))
	if got != "▁▂▄▇" {
		t.Errorf("sparkline = %q", got)
	}
	if got := ansi.Strip(Sparkline(theme.Mono(), []time.Duration{0, 0})); got != "▁▁" {
		t.Errorf("all-zero sparkline = %q", got)
	}
}

func TestElapsed(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0:00", 41 * time.Second: "0:41", 12*time.Minute + 5*time.Second: "12:05",
		time.Hour + 2*time.Minute + 3*time.Second: "1:02:03", -time.Second: "0:00",
	} {
		if got := Elapsed(d); got != want {
			t.Errorf("Elapsed(%v) = %q, want %q", d, got, want)
		}
	}
}
