package history

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/hourglass/internal/humanize"
	"github.com/jackjakarta/hourglass/internal/tui/components"
	"github.com/jackjakarta/hourglass/internal/tui/keys"
	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

// logView shows one run's output, with search.
type logView struct {
	entry Entry
	lines []string
	err   error

	top       int // first line shown
	query     string
	searching bool
	matches   []int // lines that match the query
	cur       int   // the current match
}

func newLogView(e Entry, content []byte, err error) *logView {
	v := &logView{entry: e, err: err}
	text := strings.TrimSuffix(string(content), "\n")
	if text != "" {
		for _, l := range strings.Split(text, "\n") {
			v.lines = append(v.lines, components.Sanitize(l))
		}
	}
	return v
}

// update handles a key; closed means go back to the feed.
func (v logView) update(k tea.KeyPressMsg, km keys.Map, rows int) (logView, tea.Msg, bool) {
	if v.searching {
		switch k.String() {
		case "esc":
			v.searching, v.query, v.matches = false, "", nil
		case "enter":
			v.searching = false
		case "backspace":
			if r := []rune(v.query); len(r) > 0 {
				v.query = string(r[:len(r)-1])
			}
			v.search(rows)
		case "ctrl+u":
			v.query = ""
			v.search(rows)
		default:
			v.query += k.Text
			v.search(rows)
		}
		return v, nil, false
	}
	switch {
	case key.Matches(k, km.Back):
		if v.query != "" {
			v.query, v.matches = "", nil
			return v, nil, false
		}
		return v, nil, true
	case key.Matches(k, km.Up):
		v.top--
	case key.Matches(k, km.Down):
		v.top++
	case key.Matches(k, km.PageUp):
		v.top -= rows - 1
	case key.Matches(k, km.PageDown), k.String() == "space":
		v.top += rows - 1
	case key.Matches(k, km.Top):
		v.top = 0
	case key.Matches(k, km.Bottom):
		v.top = len(v.lines)
	case key.Matches(k, km.Filter):
		v.searching, v.query = true, ""
		v.search(rows)
	case key.Matches(k, km.NextMatch):
		v.jump(1, rows)
	case key.Matches(k, km.PrevMatch):
		v.jump(-1, rows)
	case key.Matches(k, km.Pager):
		return v, PagerMsg{v.entry.Run.LogPath}, false
	}
	v.top = min(max(v.top, 0), max(len(v.lines)-rows, 0))
	return v, nil, false
}

// search finds the query's lines and shows the first match at or below
// the current position.
func (v *logView) search(rows int) {
	v.matches, v.cur = nil, 0
	if v.query == "" {
		return
	}
	q := strings.ToLower(v.query)
	for i, l := range v.lines {
		if strings.Contains(strings.ToLower(l), q) {
			v.matches = append(v.matches, i)
		}
	}
	for i, line := range v.matches {
		if line >= v.top {
			v.cur = i
			break
		}
	}
	v.show(rows)
}

func (v *logView) jump(delta, rows int) {
	if len(v.matches) == 0 {
		return
	}
	v.cur = (v.cur + delta + len(v.matches)) % len(v.matches)
	v.show(rows)
}

// show scrolls the current match into view, a few lines from the top.
func (v *logView) show(rows int) {
	if len(v.matches) == 0 {
		return
	}
	line := v.matches[v.cur]
	if line < v.top || line >= v.top+rows {
		v.top = max(line-rows/3, 0)
	}
	v.top = min(v.top, max(len(v.lines)-rows, 0))
}

func (v logView) view(th *theme.Theme, now time.Time, w, h int) string {
	iw := w - 4
	r := v.entry.Run
	title := fmt.Sprintf("%s · run %d", v.entry.Title, r.ID)

	glyph, st := components.StatusGlyph(th, r.Status)
	meta := []string{r.Trigger, humanize.Absolute(r.StartedAt, now)}
	if r.EndedAt != nil || r.Status == "running" {
		meta = append(meta, "took "+humanize.Duration(r.Duration(now)))
	}
	if r.ExitCode != nil {
		meta = append(meta, fmt.Sprintf("exit %d", *r.ExitCode))
	}
	meta = append(meta, humanize.Bytes(r.LogBytes))
	lines := []string{st.Render(glyph+" "+r.Status) + th.Muted.Render(" · "+strings.Join(meta, " · "))}
	if r.Truncated {
		lines = append(lines, th.Warn.Render(components.Fit(theme.GlyphWarn+" The output was cut at runs.max_log_bytes; the end of it is missing.", iw)))
	}
	lines = append(lines, "")

	rows := h - 2 - len(lines) - 1 // the search line at the bottom
	switch {
	case v.err != nil:
		lines = append(lines, th.Muted.Render("No saved output: "+v.err.Error()))
	case len(v.lines) == 0:
		lines = append(lines, th.Muted.Render("(no output)"))
	default:
		hl := th.AccentText.Reverse(true)
		current := -1
		if len(v.matches) > 0 {
			current = v.matches[v.cur]
		}
		end := min(v.top+rows, len(v.lines))
		for i := v.top; i < end; i++ {
			l := components.Fit(v.lines[i], iw)
			if v.query != "" {
				l = highlight(l, v.query, th.Text, hl)
				if i == current {
					l = th.SelectionBar.Render(theme.GlyphBar) + components.Fit(highlight(v.lines[i], v.query, th.Text, hl), iw-1)
				}
			} else {
				l = th.Text.Render(l)
			}
			lines = append(lines, l)
		}
	}
	for len(lines) < h-3 {
		lines = append(lines, "")
	}
	lines = append(lines, v.searchLine(th, iw))
	return components.Panel(th, title, lines, w, h, true)
}

func (v logView) searchLine(th *theme.Theme, iw int) string {
	pos := ""
	if n := len(v.lines); n > 0 {
		pos = th.Muted.Render(fmt.Sprintf("line %d of %d", v.top+1, n))
	}
	var left string
	switch {
	case v.searching:
		left = th.AccentText.Render("/ ") + th.Text.Render(v.query) + th.AccentText.Render("█")
	case v.query != "":
		left = th.AccentText.Render("/ ") + th.Text.Render(v.query)
	}
	if v.query != "" {
		if len(v.matches) == 0 {
			left += th.Muted.Render("  no matches")
		} else {
			left += th.Muted.Render(fmt.Sprintf("  %d/%d · n/N", v.cur+1, len(v.matches)))
		}
	}
	if gap := iw - lipgloss.Width(left) - lipgloss.Width(pos); gap > 0 {
		return left + strings.Repeat(" ", gap) + pos
	}
	return left
}

// highlight styles each case-insensitive occurrence of q in s with hl.
func highlight(s, q string, base, hl lipgloss.Style) string {
	if q == "" {
		return base.Render(s)
	}
	var b strings.Builder
	lower, lq := strings.ToLower(s), strings.ToLower(q)
	for {
		i := strings.Index(lower, lq)
		if i < 0 || len(lower) != len(s) { // case folding changed lengths: don't guess
			b.WriteString(base.Render(s))
			return b.String()
		}
		b.WriteString(base.Render(s[:i]) + hl.Render(s[i:i+len(q)]))
		s, lower = s[i+len(q):], lower[i+len(q):]
	}
}
