package components

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// Confirm is a modal question with a safe and an action button. The safe
// button has focus first.
type Confirm struct {
	Title  string
	Body   []string
	Safe   string  // e.g. "Cancel"
	Action string  // e.g. "Delete"
	Danger bool    // style the action button as destructive
	OnSafe tea.Cmd // run when the safe choice is made; may be nil
	OnOK   tea.Cmd // run when the action is chosen

	actionFocused bool
}

// Update handles a key. done reports that the dialog closed; cmd is the
// chosen button's command.
func (c *Confirm) Update(msg tea.KeyPressMsg) (done bool, cmd tea.Cmd) {
	switch msg.String() {
	case "left", "right", "tab", "shift+tab", "h", "l":
		c.actionFocused = !c.actionFocused
	case "enter", "space":
		if c.actionFocused {
			return true, c.OnOK
		}
		return true, c.OnSafe
	case "y":
		return true, c.OnOK
	case "esc", "n", "q":
		return true, c.OnSafe
	}
	return false, nil
}

// View renders the dialog box.
func (c *Confirm) View(th *theme.Theme) string {
	safe, action := th.Button, th.Button
	if c.actionFocused {
		action = th.ButtonFocused
		if c.Danger {
			action = th.ButtonDanger
		}
	} else {
		safe = th.ButtonFocused
	}
	buttons := safe.Render(c.Safe) + "  " + action.Render(c.Action)

	var body []string
	for _, l := range c.Body {
		body = append(body, th.Text.Render(l))
	}
	hint := th.Muted.Render("←→ choose · ↵ confirm · esc cancel")
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
