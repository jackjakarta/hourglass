// Package components holds the TUI's reusable pieces: panels, toasts,
// confirm dialogs, the help overlay and overlay compositing.
package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

// Panel draws a rounded box w×h with title set into its top border:
//
//	╭─ Jobs · 6 ─────╮
//	│ body           │
//	╰────────────────╯
//
// Body lines are cut or padded to fit; the inner width is w-4.
func Panel(th *theme.Theme, title string, body []string, w, h int, focused bool) string {
	if w < 4 || h < 2 {
		return ""
	}
	border := lipgloss.NewStyle().Foreground(th.BorderColor)
	if focused {
		border = border.Foreground(th.Accent)
	}
	iw := w - 4
	var b strings.Builder

	b.WriteString(border.Render("╭─"))
	used := 2
	if title != "" {
		title = ansi.Truncate(title, w-6, "…")
		b.WriteString(" " + th.Heading.Render(title) + " ")
		used += 2 + lipgloss.Width(title)
	}
	b.WriteString(border.Render(strings.Repeat("─", max(w-1-used, 0)) + "╮"))
	b.WriteByte('\n')

	side := border.Render("│")
	for i := range h - 2 {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		b.WriteString(side + " " + Fit(line, iw) + " " + side + "\n")
	}
	b.WriteString(border.Render("╰" + strings.Repeat("─", w-2) + "╯"))
	return b.String()
}

// Fit cuts s to width cells (ending in "…") or pads it with spaces.
func Fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) > width {
		s = ansi.Truncate(s, width, "…")
	}
	if pad := width - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// Center places block in the middle of a w×h area of spaces.
func Center(block string, w, h int) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, block)
}
