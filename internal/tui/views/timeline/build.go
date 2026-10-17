// Package timeline is the Timeline view: when each job fires over the next
// day or week, how busy each moment is, and which minutes are congested.
package timeline

import (
	"sort"
	"time"

	"github.com/jackjakarta/hourglass/internal/jobs"
	"github.com/jackjakarta/hourglass/internal/store"
)

// Lane is one job's row in the timeline.
type Lane struct {
	Job    jobs.Job
	Cells  []int // fires per column
	Fires  int   // fires in the window
	Band   bool  // more fires than columns: drawn as a continuous band
	Next   time.Time
	Reboot bool       // @reboot: no calendar schedule
	Past   []PastMark // runs from history before now
}

// PastMark is a recorded run drawn left of now.
type PastMark struct {
	Col    int
	Status string
}

// Congestion is a minute when threshold or more jobs fire together.
type Congestion struct {
	At   time.Time
	Jobs int
}

// Grid is a laid-out timeline.
type Grid struct {
	From, To, Now time.Time
	Cols          int
	Lanes         []Lane
	Density       []int        // distinct jobs firing per column
	Congestion    []Congestion // soonest first
}

// Col returns the column of t, or -1 when it's outside the window.
func (g Grid) Col(t time.Time) int {
	if t.Before(g.From) || !t.Before(g.To) || g.Cols <= 0 {
		return -1
	}
	return int(int64(t.Sub(g.From)) * int64(g.Cols) / int64(g.To.Sub(g.From)))
}

// Build lays out a lane for each enabled job across cols columns spanning
// [from, to). Upcoming fires are drawn from max(from, now); runs recorded
// in stats are marked before now. Minutes when threshold or more jobs fire
// are reported as congestion.
func Build(js []jobs.Job, stats map[string]store.JobStats, from, to, now time.Time, cols, threshold int) Grid {
	g := Grid{From: from, To: to, Now: now, Cols: cols}
	if cols <= 0 || !from.Before(to) {
		return g
	}
	g.Density = make([]int, cols)
	start := from
	if now.After(start) {
		start = now
	}
	perMinute := map[int64]int{}
	for _, j := range js {
		if !j.Enabled || j.Expr == nil {
			continue
		}
		l := Lane{Job: j, Cells: make([]int, cols), Reboot: j.Expr.Reboot}
		if !l.Reboot {
			for t := range j.Between(start, to) {
				if c := g.Col(t); c >= 0 {
					l.Cells[c]++
					l.Fires++
				}
				perMinute[t.Unix()/60]++
			}
			if l.Fires == 0 {
				if next := j.NextRuns(to.Add(-time.Second), 1); len(next) > 0 {
					l.Next = next[0]
				}
			}
		}
		l.Band = l.Fires > cols
		for c, n := range l.Cells {
			if n > 0 {
				g.Density[c]++
			}
		}
		if j.Managed {
			st := stats[j.ID]
			runs := append([]store.Run(nil), st.Recent...)
			if st.Running != nil {
				runs = append(runs, *st.Running)
			}
			for _, r := range runs {
				if r.StartedAt.Before(now) {
					if c := g.Col(r.StartedAt); c >= 0 {
						l.Past = append(l.Past, PastMark{Col: c, Status: r.Status})
					}
				}
			}
		}
		g.Lanes = append(g.Lanes, l)
	}
	if threshold > 0 {
		for m, n := range perMinute {
			if n >= threshold {
				g.Congestion = append(g.Congestion, Congestion{At: time.Unix(m*60, 0).In(now.Location()), Jobs: n})
			}
		}
		sort.Slice(g.Congestion, func(a, b int) bool { return g.Congestion[a].At.Before(g.Congestion[b].At) })
	}
	return g
}
