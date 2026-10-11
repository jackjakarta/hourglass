// Package theme holds gocron's palettes, styles and glyphs. Views take
// every color and style from a Theme and never hardcode them.
package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Glyphs used everywhere (spec §6.1).
const (
	GlyphEnabled = "●"
	GlyphPaused  = "○"
	GlyphRunning = "◐"
	GlyphOK      = "✓"
	GlyphFail    = "✗"
	GlyphTimeout = "⏱"
	GlyphSkipped = "↷"
	GlyphUnknown = "?"
	GlyphWarn    = "⚠"
	GlyphBar     = "▌"
	GlyphReload  = "↻"
	GlyphInfo    = "·"
	GlyphClock   = "◷"
	GlyphCell    = "■" // one run in a run strip
)

// Spinner is the running glyph's animation, one frame per tick.
var Spinner = []string{"◐", "◓", "◑", "◒"}

// Sparks are the bar heights of a duration sparkline, lowest first.
var Sparks = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇"}

// Theme is a palette and the styles built from it.
type Theme struct {
	Name string
	Mono bool

	Accent, Accent2, TextColor, MutedColor, BorderColor, OKColor, FailColor, WarnColor, SelBg color.Color

	Text, Muted, Heading, Label, Schedule, Key, OK, Fail, Warn, AccentText lipgloss.Style
	Selected, SelectionBar                                                 lipgloss.Style
	TabActive, Tab, Title                                                  lipgloss.Style
	Button, ButtonFocused, ButtonDanger                                    lipgloss.Style
}

// Select returns the theme to use: Mono when NO_COLOR is set, otherwise
// Nightshift for the terminal's background.
func Select(noColor, isDark bool) *Theme {
	if noColor {
		return Mono()
	}
	return Nightshift(isDark)
}

// Nightshift is the default theme: a mint accent on quiet neutrals.
func Nightshift(isDark bool) *Theme {
	ld := lipgloss.LightDark(isDark)
	c := lipgloss.Color
	t := &Theme{
		Name:        "nightshift",
		Accent:      ld(c("#0D9488"), c("#5EEAD4")),
		Accent2:     ld(c("#7C3AED"), c("#C4B5FD")),
		TextColor:   ld(c("#18181B"), c("#E4E4E7")),
		MutedColor:  c("#71717A"),
		BorderColor: ld(c("#D4D4D8"), c("#3F3F46")),
		OKColor:     ld(c("#16A34A"), c("#4ADE80")),
		FailColor:   ld(c("#DC2626"), c("#F87171")),
		WarnColor:   ld(c("#D97706"), c("#FBBF24")),
		SelBg:       ld(c("#F4F4F5"), c("#27272A")),
	}
	s := lipgloss.NewStyle
	t.Text = s().Foreground(t.TextColor)
	t.Muted = s().Foreground(t.MutedColor)
	t.Heading = s().Foreground(t.Accent2).Bold(true)
	t.Label = s().Foreground(t.MutedColor).Bold(true)
	t.Schedule = s().Foreground(t.Accent2)
	t.Key = s().Foreground(t.Accent)
	t.AccentText = s().Foreground(t.Accent)
	t.OK = s().Foreground(t.OKColor)
	t.Fail = s().Foreground(t.FailColor)
	t.Warn = s().Foreground(t.WarnColor)
	t.Selected = s().Background(t.SelBg)
	t.SelectionBar = s().Foreground(t.Accent)
	t.TabActive = s().Foreground(t.Accent).Bold(true).Underline(true)
	t.Tab = s().Foreground(t.MutedColor)
	t.Title = s().Foreground(t.TextColor).Bold(true)
	t.Button = s().Foreground(t.TextColor).Background(t.SelBg).Padding(0, 2)
	t.ButtonFocused = s().Foreground(t.SelBg).Background(t.Accent).Bold(true).Padding(0, 2)
	t.ButtonDanger = s().Foreground(t.SelBg).Background(t.FailColor).Bold(true).Padding(0, 2)
	return t
}

// Mono uses no color at all, only bold, faint, underline and reverse.
func Mono() *Theme {
	none := lipgloss.NoColor{}
	t := &Theme{
		Name: "mono", Mono: true,
		Accent: none, Accent2: none, TextColor: none, MutedColor: none, BorderColor: none,
		OKColor: none, FailColor: none, WarnColor: none, SelBg: none,
	}
	s := lipgloss.NewStyle
	t.Text = s()
	t.Muted = s().Faint(true)
	t.Heading = s().Bold(true)
	t.Label = s().Faint(true)
	t.Schedule = s()
	t.Key = s().Bold(true)
	t.AccentText = s().Bold(true)
	t.OK = s()
	t.Fail = s().Bold(true)
	t.Warn = s().Bold(true)
	t.Selected = s().Bold(true)
	t.SelectionBar = s().Bold(true)
	t.TabActive = s().Bold(true).Underline(true)
	t.Tab = s().Faint(true)
	t.Title = s().Bold(true)
	t.Button = s().Padding(0, 2)
	t.ButtonFocused = s().Reverse(true).Bold(true).Padding(0, 2)
	t.ButtonDanger = t.ButtonFocused
	return t
}
