package components

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

// Confirm is a modal question with a safe and an action button, and
// optionally an extra one to their left (d chooses it). The safe button
// has focus first.
type Confirm struct {
	Title   string
	Body    []string
	Safe    string  // e.g. "Cancel"
	Action  string  // e.g. "Delete"
	Danger  bool    // style the action button as destructive
	OnSafe  tea.Cmd // run when the safe choice is made; may be nil
	OnOK    tea.Cmd // run when the action is chosen
	Extra   string  // e.g. "View diff"; "" for none
	OnExtra tea.Cmd

	focus int // offset from the safe button: -1 extra, 0 safe, 1 action
}

// Update handles a key. done reports that the dialog closed; cmd is the
// chosen button's command.
func (c *Confirm) Update(msg tea.KeyPressMsg) (done bool, cmd tea.Cmd) {
	lo := 0
	if c.Extra != "" {
		lo = -1
	}
	switch msg.String() {
	case "right", "tab", "l":
		if c.focus++; c.focus > 1 {
			c.focus = lo
		}
	case "left", "shift+tab", "h":
		if c.focus--; c.focus < lo {
			c.focus = 1
		}
	case "enter", "space":
		switch c.focus {
		case 1:
			return true, c.OnOK
		case -1:
			return true, c.OnExtra
		}
		return true, c.OnSafe
	case "y":
		return true, c.OnOK
	case "d":
		if c.Extra != "" {
			return true, c.OnExtra
		}
	case "esc", "n", "q":
		return true, c.OnSafe
	}
	return false, nil
}

// View renders the dialog box.
func (c *Confirm) View(th *theme.Theme) string {
	safe, action, extra := th.Button, th.Button, th.Button
	switch c.focus {
	case 1:
		action = th.ButtonFocused
		if c.Danger {
			action = th.ButtonDanger
		}
	case -1:
		extra = th.ButtonFocused
	default:
		safe = th.ButtonFocused
	}
	buttons := safe.Render(c.Safe) + "  " + action.Render(c.Action)
	if c.Extra != "" {
		buttons = extra.Render(c.Extra) + "  " + buttons
	}

	var body []string
	for _, l := range c.Body {
		body = append(body, th.Text.Render(l))
	}
	hintText := "←→ choose · ↵ confirm · esc cancel"
	if c.Extra != "" {
		hintText = "←→ choose · ↵ confirm · d " + strings.ToLower(c.Extra) + " · esc cancel"
	}
	hint := th.Muted.Render(hintText)
	w := max(lipgloss.Width(buttons), lipgloss.Width(hint))
	for _, l := range append([]string{c.Title}, c.Body...) {
		w = max(w, lipgloss.Width(l))
	}
	content := th.Title.Render(c.Title) + "\n\n" + strings.Join(body, "\n") + "\n\n" +
		lipgloss.PlaceHorizontal(w, lipgloss.Right, buttons) + "\n" +
		lipgloss.PlaceHorizontal(w, lipgloss.Right, hint)
	border := th.Accent
	if c.Danger {
		border = th.FailColor
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(1, 3).Render(content)
}

// Click handles a click at x, y relative to the dialog's top-left corner:
// a click on a button chooses it.
func (c *Confirm) Click(th *theme.Theme, x, y int) (done bool, cmd tea.Cmd) {
	box := c.View(th)
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	if y != bh-4 { // above the hint line, the padding and the border
		return false, nil
	}
	button := func(label string) int { return lipgloss.Width(th.Button.Render(label)) }
	right := bw - 4 // the border and padding on the right
	if x < right && x >= right-button(c.Action) {
		return true, c.OnOK
	}
	right -= button(c.Action) + 2
	if x < right && x >= right-button(c.Safe) {
		return true, c.OnSafe
	}
	right -= button(c.Safe) + 2
	if c.Extra != "" && x < right && x >= right-button(c.Extra) {
		return true, c.OnExtra
	}
	return false, nil
}
