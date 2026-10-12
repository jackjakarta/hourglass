package components

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// PaletteItem is one choice in a Palette.
type PaletteItem struct {
	Title string
	Hint  string // the item's key binding, shown on the right
}

// Palette is a fuzzy-filtered list of items: the command palette and the
// tag picker.
type Palette struct {
	Title       string
	Placeholder string
	Items       []PaletteItem

	query   string
	matches []int // indexes into Items, best first
	sel     int
	offset  int
}

// NewPalette returns a palette listing every item.
func NewPalette(title, placeholder string, items []PaletteItem) *Palette {
	p := &Palette{Title: title, Placeholder: placeholder, Items: items}
	p.refilter()
	return p
}

// Query is what's been typed.
func (p *Palette) Query() string { return p.query }

// Matches returns the items matching the query, best first.
func (p *Palette) Matches() []PaletteItem {
	out := make([]PaletteItem, len(p.matches))
	for i, idx := range p.matches {
		out[i] = p.Items[idx]
	}
	return out
}

// Update handles a key. When done, chosen is the index of the chosen item
// in Items, or -1 when the palette was dismissed.
func (p *Palette) Update(k tea.KeyPressMsg) (chosen int, done bool) {
	switch k.String() {
	case "esc", "ctrl+k":
		return -1, true
	case "enter":
		if p.sel < len(p.matches) {
			return p.matches[p.sel], true
		}
		return -1, true
	case "up", "ctrl+p":
		p.move(-1)
	case "down", "ctrl+n", "tab":
		p.move(1)
	case "pgup":
		p.move(-paletteRows)
	case "pgdown":
		p.move(paletteRows)
	case "backspace":
		if r := []rune(p.query); len(r) > 0 {
			p.query = string(r[:len(r)-1])
			p.refilter()
		}
	case "ctrl+u":
		p.query = ""
		p.refilter()
	default:
		if k.Text != "" {
			p.query += k.Text
			p.refilter()
		}
	}
	return -1, false
}

func (p *Palette) move(d int) {
	p.sel = min(max(p.sel+d, 0), max(len(p.matches)-1, 0))
	if p.sel < p.offset {
		p.offset = p.sel
	}
	if p.sel >= p.offset+paletteRows {
		p.offset = p.sel - paletteRows + 1
	}
}

func (p *Palette) refilter() {
	type scored struct{ idx, score int }
	var s []scored
	for i, it := range p.Items {
		if score, ok := FuzzyMatch(p.query, it.Title); ok {
			s = append(s, scored{i, score})
		}
	}
	sort.SliceStable(s, func(a, b int) bool { return s[a].score > s[b].score })
	p.matches = p.matches[:0]
	for _, x := range s {
		p.matches = append(p.matches, x.idx)
	}
	p.sel, p.offset = 0, 0
}

// paletteRows is how many items the palette shows at once.
const paletteRows = 12

// View renders the palette box, at most w wide.
func (p *Palette) View(th *theme.Theme, w int) string {
	w = min(w, 76)
	iw := w - 4
	lines := []string{th.AccentText.Render("› ") + th.Text.Render(p.query) + th.AccentText.Render("█")}
	if p.query == "" && p.Placeholder != "" {
		lines[0] = th.AccentText.Render("› ") + th.AccentText.Render("█") + th.Muted.Render(p.Placeholder)
	}
	lines = append(lines, "")
	if len(p.matches) == 0 {
		lines = append(lines, th.Muted.Render("  Nothing matches."))
	}
	end := min(p.offset+paletteRows, len(p.matches))
	for i := p.offset; i < end; i++ {
		it := p.Items[p.matches[i]]
		hint := th.Key.Render(it.Hint)
		title := Fit(it.Title, iw-2-lipgloss.Width(it.Hint)-2)
		if i == p.sel {
			lines = append(lines, th.SelectionBar.Render(theme.GlyphBar)+" "+th.Selected.Render(title)+"  "+hint)
		} else {
			lines = append(lines, "  "+th.Text.Render(title)+"  "+hint)
		}
	}
	if n := len(p.matches); n > paletteRows {
		lines = append(lines, th.Muted.Render("  "+strconv.Itoa(p.sel+1)+"/"+strconv.Itoa(n)))
	}
	return Panel(th, p.Title, lines, w, len(lines)+2, true)
}

// FuzzyMatch scores text against query. Every word of the query must match
// text as a case-insensitive subsequence; matches at word starts, at the
// very start, and in runs of adjacent characters score higher.
func FuzzyMatch(query, text string) (score int, ok bool) {
	hay := []rune(strings.ToLower(text))
	for _, word := range strings.Fields(strings.ToLower(query)) {
		best, found := -1, false
		w := []rune(word)
		for start := range hay {
			if hay[start] != w[0] {
				continue
			}
			if s, ok := scoreFrom(hay, w, start); ok && s > best {
				best, found = s, true
			}
		}
		if !found {
			return 0, false
		}
		score += best
	}
	return score, true
}

// scoreFrom matches w greedily in hay from start, preferring an adjacent
// character, then the next word start, then any later occurrence.
func scoreFrom(hay, w []rune, start int) (int, bool) {
	score, pos := 0, start
	for i, r := range w {
		if i > 0 {
			next := -1
			switch {
			case pos+1 < len(hay) && hay[pos+1] == r:
				next = pos + 1
			default:
				for j := pos + 1; j < len(hay); j++ {
					if hay[j] == r && wordStart(hay, j) {
						next = j
						break
					}
				}
				if next < 0 {
					for j := pos + 1; j < len(hay); j++ {
						if hay[j] == r {
							next = j
							break
						}
					}
				}
			}
			if next < 0 {
				return 0, false
			}
			if next == pos+1 {
				score += 5
			} else {
				score -= min(next-pos-1, 3)
			}
			pos = next
		}
		score++
		if wordStart(hay, pos) {
			score += 8
		}
		if pos == 0 {
			score += 10
		}
	}
	if pos+1 == len(hay) || !wordRune(hay[pos+1]) {
		score += 3 // a whole word
	}
	return score, true
}

func wordStart(hay []rune, i int) bool { return i == 0 || !wordRune(hay[i-1]) }

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// Click handles a click at x, y relative to the palette's top-left
// corner. Clicking an item chooses it; chosen is its index in Items, or
// -1 when the click missed every item.
func (p *Palette) Click(x, y int) (chosen int, done bool) {
	r := y - 3 // below the border, the query line and a gap
	if x <= 0 || r < 0 || r >= paletteRows {
		return -1, false
	}
	i := p.offset + r
	if i >= len(p.matches) {
		return -1, false
	}
	return p.matches[i], true
}

// Wheel moves the selection by delta items.
func (p *Palette) Wheel(delta int) { p.move(delta) }
