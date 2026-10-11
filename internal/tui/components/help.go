package components

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// Footer renders one line of key hints that fits width.
func Footer(th *theme.Theme, bindings []key.Binding, width int) string {
	h := help.New()
	h.SetWidth(width)
	h.ShortSeparator = "  "
	h.Styles.ShortKey = th.Key
	h.Styles.ShortDesc = th.Muted
	h.Styles.ShortSeparator = th.Muted
	h.Styles.Ellipsis = th.Muted
	return h.ShortHelpView(bindings)
}

// HelpOverlay renders the grouped key bindings in a box at most maxWidth
// wide, wrapping the groups onto more rows when they don't fit side by side.
func HelpOverlay(th *theme.Theme, groups [][]key.Binding, titles []string, maxWidth int) string {
	const sep = "     "
	var cols []string
	for i, g := range groups {
		keyW := 0
		for _, b := range g {
			keyW = max(keyW, lipgloss.Width(b.Help().Key))
		}
		lines := []string{th.Heading.Render(titles[i]), ""}
		for _, b := range g {
			if !b.Enabled() {
				continue
			}
			k := b.Help().Key
			lines = append(lines, th.Key.Render(k)+strings.Repeat(" ", keyW-lipgloss.Width(k)+2)+th.Text.Render(b.Help().Desc))
		}
		cols = append(cols, strings.Join(lines, "\n"))
	}
	avail := maxWidth - 8 // border and padding
	var rows, row []string
	width := 0
	for _, c := range cols {
		w := lipgloss.Width(c)
		if len(row) > 0 && width+len(sep)+w > avail {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, intersperse(row, sep)...))
			row, width = nil, 0
		}
		if len(row) > 0 {
			width += len(sep)
		}
		row, width = append(row, c), width+w
	}
	rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, intersperse(row, sep)...))
	body := strings.Join(rows, "\n\n")
	foot := th.Muted.Render("? or esc to close")
	content := th.Title.Render("Keys") + "\n\n" + body + "\n\n" + lipgloss.PlaceHorizontal(lipgloss.Width(body), lipgloss.Right, foot)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(th.Accent).Padding(1, 3).Render(content)
}

func intersperse(items []string, sep string) []string {
	var out []string
	for i, s := range items {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, s)
	}
	return out
}
