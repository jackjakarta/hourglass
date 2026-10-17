package components

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

// ToastKind decides a toast's glyph color and lifetime.
type ToastKind int

const (
	ToastOK ToastKind = iota
	ToastInfo
	ToastFail
)

const (
	toastDimAfter = 3 * time.Second
	toastLife     = 6 * time.Second
	toastFailLife = 10 * time.Second
	maxToasts     = 3
	toastWidth    = 64
)

// Toast is a short notice, e.g. "✓ Saved · backup created · u undo".
type Toast struct {
	Kind  ToastKind
	Glyph string // defaults by kind: ✓, ·, ✗
	Text  string
	At    time.Time
}

// Toasts is a small stack of recent toasts, newest last.
type Toasts struct {
	items []Toast
}

// Push adds a toast, dropping the oldest beyond the limit.
func (t *Toasts) Push(toast Toast) {
	t.items = append(t.items, toast)
	if len(t.items) > maxToasts {
		t.items = t.items[len(t.items)-maxToasts:]
	}
}

// Expire drops toasts that have outlived their lifetime at now.
func (t *Toasts) Expire(now time.Time) {
	kept := t.items[:0]
	for _, toast := range t.items {
		life := toastLife
		if toast.Kind == ToastFail {
			life = toastFailLife
		}
		if now.Sub(toast.At) < life {
			kept = append(kept, toast)
		}
	}
	t.items = kept
}

// Len returns the number of live toasts.
func (t *Toasts) Len() int { return len(t.items) }

// View renders the stack, newest at the bottom, or "" when empty. Toasts
// older than 3s are dimmed.
func (t *Toasts) View(th *theme.Theme, now time.Time, maxWidth int) string {
	if len(t.items) == 0 {
		return ""
	}
	w := min(toastWidth, maxWidth)
	var boxes []string
	for _, toast := range t.items {
		glyph, style := toast.Glyph, th.OK
		switch toast.Kind {
		case ToastInfo:
			style = th.AccentText
			if glyph == "" {
				glyph = theme.GlyphInfo
			}
		case ToastFail:
			style = th.Fail
			if glyph == "" {
				glyph = theme.GlyphFail
			}
		default:
			if glyph == "" {
				glyph = theme.GlyphOK
			}
		}
		text, border := th.Text, th.BorderColor
		if now.Sub(toast.At) >= toastDimAfter {
			style, text = th.Muted, th.Muted
		} else if toast.Kind == ToastFail {
			border = th.FailColor
		}
		body := lipgloss.NewStyle().Width(min(lipgloss.Width(toast.Text), w-6)).Render(toast.Text)
		lines := strings.Split(body, "\n")
		for i, l := range lines {
			lines[i] = text.Render(l)
		}
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1).
			Render(lipgloss.JoinHorizontal(lipgloss.Top, style.Render(glyph+" "), strings.Join(lines, "\n")))
		boxes = append(boxes, box)
	}
	return lipgloss.JoinVertical(lipgloss.Right, boxes...)
}

// Overlay draws layer over base (a w×h screen) with its top-left corner at
// x, y.
func Overlay(base, layer string, x, y int) string {
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(layer).X(max(x, 0)).Y(max(y, 0)).Z(1),
	).Render()
}

// OverlayCenter draws layer centered over base, a w×h screen.
func OverlayCenter(base, layer string, w, h int) string {
	return Overlay(base, layer, (w-lipgloss.Width(layer))/2, (h-lipgloss.Height(layer))/2)
}
