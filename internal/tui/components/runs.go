package components

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/hourglass/internal/store"
	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

// StatusGlyph is a run status's glyph and style: ✓ ✗ ⏱ ↷ ◐ ?.
func StatusGlyph(th *theme.Theme, status string) (string, lipgloss.Style) {
	switch status {
	case store.StatusSuccess:
		return theme.GlyphOK, th.OK
	case store.StatusFailed, store.StatusError:
		return theme.GlyphFail, th.Fail
	case store.StatusTimeout:
		return theme.GlyphTimeout, th.Warn
	case store.StatusSkipped:
		return theme.GlyphSkipped, th.Muted
	case store.StatusRunning:
		return theme.GlyphRunning, th.Warn
	}
	return theme.GlyphUnknown, th.Muted
}

// RunStrip draws runs (oldest first) as one cell each, colored by status.
// Without color, the cells' shapes tell the statuses apart instead.
func RunStrip(th *theme.Theme, runs []store.Run) string {
	var b strings.Builder
	for _, r := range runs {
		cell, st := theme.GlyphCell, th.Muted
		switch r.Status {
		case store.StatusSuccess:
			st = th.OK
		case store.StatusFailed, store.StatusError:
			st = th.Fail
			if th.Mono {
				cell = "×"
			}
		case store.StatusTimeout:
			st = th.Warn
			if th.Mono {
				cell = "!"
			}
		case store.StatusRunning:
			st = th.Warn
			if th.Mono {
				cell = theme.GlyphRunning
			}
		case store.StatusSkipped:
			if th.Mono {
				cell = "·"
			}
		default: // lost
			if th.Mono {
				cell = "?"
			}
		}
		b.WriteString(st.Render(cell))
	}
	return b.String()
}

// Sparkline draws durations as bars scaled to the longest.
func Sparkline(th *theme.Theme, durs []time.Duration) string {
	var longest time.Duration
	for _, d := range durs {
		longest = max(longest, d)
	}
	var b strings.Builder
	for _, d := range durs {
		i := 0
		if longest > 0 {
			i = int(int64(d) * int64(len(theme.Sparks)-1) / int64(longest))
		}
		b.WriteString(theme.Sparks[i])
	}
	return th.Schedule.Render(b.String())
}

// Elapsed renders a running time as a clock: 0:41, 12:05, 1:02:03.
func Elapsed(d time.Duration) string {
	d = max(d, 0).Truncate(time.Second)
	h, m, s := int(d/time.Hour), int(d%time.Hour/time.Minute), int(d%time.Minute/time.Second)
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
