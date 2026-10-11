package jobs

import (
	"fmt"
	"strings"

	"github.com/jackjakarta/gocron/internal/crontab"
)

// Summary counts what changed between two versions of a crontab.
type Summary struct {
	Added, Removed, Edited int // jobs and entries
	Paused, Resumed        int // managed jobs
	Lines                  int // other lines added or removed
}

// Summarize compares two crontabs. Managed jobs are matched by id;
// unmanaged entries and other lines by their text.
func Summarize(before, after []byte) Summary {
	var s Summary
	a, b := crontab.Parse(before), crontab.Parse(after)
	oldJobs := map[string]crontab.Job{}
	oldEntries := map[string]int{}
	for _, j := range a.Jobs() {
		if j.Managed {
			oldJobs[j.ID] = j
		} else {
			oldEntries[j.Raw]++
		}
	}
	for _, j := range b.Jobs() {
		if !j.Managed {
			if oldEntries[j.Raw] > 0 {
				oldEntries[j.Raw]--
			} else {
				s.Added++
			}
			continue
		}
		old, ok := oldJobs[j.ID]
		delete(oldJobs, j.ID)
		switch {
		case !ok:
			s.Added++
		case old.Enabled && !j.Enabled:
			s.Paused++
		case !old.Enabled && j.Enabled:
			s.Resumed++
		}
		if ok && !sameJob(old, j) {
			s.Edited++
		}
	}
	s.Removed += len(oldJobs)
	for _, n := range oldEntries {
		s.Removed += n
	}
	// Lines that aren't jobs: blank lines, comments, env and invalid lines.
	other := func(c *crontab.Crontab) map[string]int {
		out := map[string]int{}
		for _, l := range c.Lines {
			switch l.Kind {
			case crontab.Comment, crontab.Env, crontab.Invalid:
				out[l.Raw]++
			}
		}
		return out
	}
	oa, ob := other(a), other(b)
	for raw, n := range ob {
		s.Lines += max(n-oa[raw], 0)
	}
	for raw, n := range oa {
		s.Lines += max(n-ob[raw], 0)
	}
	return s
}

// sameJob compares what a job does and says, ignoring its paused state
// and position.
func sameJob(a, b crontab.Job) bool {
	return sameDefinition(a, b) && a.Desc == b.Desc && a.Bin == b.Bin
}

// Empty reports no changes.
func (s Summary) Empty() bool { return s == Summary{} }

// String reads like "+1 job, ~2 edited, 1 paused".
func (s Summary) String() string {
	var parts []string
	add := func(n int, f string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf(f, n))
		}
	}
	if s.Added > 0 {
		parts = append(parts, fmt.Sprintf("+%d %s", s.Added, plural(s.Added, "job")))
	}
	if s.Removed > 0 {
		parts = append(parts, fmt.Sprintf("−%d %s", s.Removed, plural(s.Removed, "job")))
	}
	add(s.Edited, "~%d edited")
	add(s.Paused, "%d paused")
	add(s.Resumed, "%d resumed")
	if s.Lines > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Lines, plural(s.Lines, "other line")))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
