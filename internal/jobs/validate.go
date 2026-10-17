package jobs

import "github.com/jackjakarta/hourglass/internal/crontab"

// ValidateEdit returns the problems in after, a hand-edited crontab, that
// before didn't already have. Problems are matched by kind and line text,
// not line number, so moving an old problem around doesn't count as new,
// and nobody is stuck fixing lines they didn't touch.
func ValidateEdit(before, after []byte) []crontab.Problem {
	type problemKey struct{ code, raw string }
	keyOf := func(c *crontab.Crontab, p crontab.Problem) problemKey {
		raw := ""
		if p.Line >= 1 && p.Line <= len(c.Lines) {
			raw = c.Lines[p.Line-1].Raw
		}
		return problemKey{p.Code, raw}
	}
	old := crontab.Parse(before)
	seen := map[problemKey]int{}
	for _, p := range old.Problems() {
		seen[keyOf(old, p)]++
	}
	edited := crontab.Parse(after)
	var out []crontab.Problem
	for _, p := range edited.Problems() {
		k := keyOf(edited, p)
		if seen[k] > 0 {
			seen[k]--
			continue
		}
		out = append(out, p)
	}
	return out
}
