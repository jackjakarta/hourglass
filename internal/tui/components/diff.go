package components

import (
	"strings"

	"github.com/aymanbagabas/go-udiff"

	"github.com/jackjakarta/gocron/internal/tui/theme"
)

// UnifiedDiff is a unified diff from a to b, with a little context. It's
// empty when they're the same.
func UnifiedDiff(aName, bName string, a, b []byte) string {
	if string(a) == string(b) {
		return ""
	}
	edits := udiff.Lines(string(a), string(b))
	d, err := udiff.ToUnified(aName, bName, string(a), edits, 2)
	if err != nil {
		return ""
	}
	return d
}

// DiffLines styles a unified diff line by line: additions, deletions and
// hunk headers. Mono themes keep the +/- prefixes, which carry the meaning.
func DiffLines(th *theme.Theme, unified string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSuffix(unified, "\n"), "\n") {
		l = Sanitize(l)
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
			out = append(out, th.Label.Render(l))
		case strings.HasPrefix(l, "@@"):
			out = append(out, th.DiffHunk.Render(l))
		case strings.HasPrefix(l, "+"):
			out = append(out, th.DiffAdd.Render(l))
		case strings.HasPrefix(l, "-"):
			out = append(out, th.DiffDel.Render(l))
		case strings.HasPrefix(l, `\`):
			out = append(out, th.Muted.Render(l))
		default:
			out = append(out, th.Text.Render(l))
		}
	}
	return out
}
