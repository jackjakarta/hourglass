package crontab

import (
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/jackjakarta/gocron/internal/cronexpr"
)

// Job is a cron job as gocron presents it: a managed job (meta + cron
// line) or an unmanaged entry.
type Job struct {
	ID        string // "" for unmanaged entries
	Name      string
	Desc      string
	Tags      []string
	Schedule  string // as written, whitespace collapsed: "30 2 * * *", "@daily"
	Command   string // the user's plain command (unquoted, unescaped)
	Enabled   bool
	Managed   bool
	Wrapped   bool // runs via gocron exec
	Timeout   time.Duration
	NoOverlap bool
	Line      int    // 1-based line number of the cron line
	Bin       string // gocron binary path in a wrapped line
	TZ        string // CRON_TZ in effect for this line, if any
	Raw       string `json:"-"` // the cron line as written (unmanaged entries)

	Expr *cronexpr.Expr `json:"-"`
}

// Title is the name, or the command when there is none.
func (j Job) Title() string {
	if j.Name != "" {
		return j.Name
	}
	return j.Command
}

// Describe returns the schedule in English, naming the zone of a CRON_TZ
// line that applies to it.
func (j Job) Describe() string {
	if j.Expr == nil {
		return ""
	}
	s := j.Expr.Describe()
	if j.TZ != "" && j.Expr.TZ == nil {
		s += " (" + j.TZ + ")"
	}
	return s
}

// NextRuns returns up to n fire times after from, evaluated in the job's
// CRON_TZ when one is set and returned in from's location.
func (j Job) NextRuns(from time.Time, n int) []time.Time {
	if j.Expr == nil {
		return nil
	}
	out := j.Expr.Next(j.evalTime(from), n)
	for i := range out {
		out[i] = out[i].In(from.Location())
	}
	return out
}

// Between yields the fire times t with from <= t < to, evaluated like
// NextRuns and returned in from's location.
func (j Job) Between(from, to time.Time) iter.Seq[time.Time] {
	return func(yield func(time.Time) bool) {
		if j.Expr == nil {
			return
		}
		for t := range j.Expr.Between(j.evalTime(from), to) {
			if !yield(t.In(from.Location())) {
				return
			}
		}
	}
}

// evalTime is from in the zone of a CRON_TZ line that applies to the job.
func (j Job) evalTime(from time.Time) time.Time {
	if j.TZ != "" && j.Expr.TZ == nil {
		if loc, err := loadLocation(j.TZ); err == nil {
			return from.In(loc)
		}
	}
	return from
}

var locations sync.Map // zone name → *time.Location or error

// loadLocation is time.LoadLocation, cached: it reads the zoneinfo
// database on every call, and the timeline asks for every job each minute.
func loadLocation(name string) (*time.Location, error) {
	if v, ok := locations.Load(name); ok {
		if loc, ok := v.(*time.Location); ok {
			return loc, nil
		}
		return nil, v.(error)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		locations.Store(name, err)
		return nil, err
	}
	locations.Store(name, loc)
	return loc, nil
}

// Jobs returns managed jobs and unmanaged entries in crontab order.
func (c *Crontab) Jobs() []Job {
	var out []Job
	tz := ""
	for i, l := range c.Lines {
		switch l.Kind {
		case Env:
			if l.Env.Name == "CRON_TZ" {
				tz = l.Env.Value
			}
		case Entry:
			out = append(out, Job{
				Schedule: normalizeSpace(l.Entry.Schedule),
				Command:  strings.TrimSpace(l.Entry.Command),
				Enabled:  true,
				Line:     i + 1,
				TZ:       tz,
				Raw:      l.Raw,
				Expr:     l.Entry.Expr,
			})
		case JobMeta:
			out = append(out, managedJob(l.Meta, c.Lines[i+1].Entry, i+2, tz))
		}
	}
	return out
}

func managedJob(m *Meta, e *CronEntry, line int, tz string) Job {
	j := Job{
		ID:       m.Get("id"),
		Name:     m.Get("name"),
		Desc:     m.Get("desc"),
		Tags:     m.Tags(),
		Schedule: normalizeSpace(e.Schedule),
		Enabled:  !e.Disabled,
		Managed:  true,
		Line:     line,
		TZ:       tz,
		Expr:     e.Expr,
	}
	cmd := UnescapePercent(strings.TrimRight(e.Command, " \t"))
	if w, ok := DecodeWrapped(cmd); ok {
		j.Wrapped, j.Command, j.Timeout, j.NoOverlap, j.Bin = true, w.Command, w.Timeout, w.NoOverlap, w.Bin
	} else {
		j.Command = cmd
	}
	return j
}

// Job returns the managed job with id.
func (c *Crontab) Job(id string) (Job, bool) {
	for _, j := range c.Jobs() {
		if j.Managed && j.ID == id {
			return j, true
		}
	}
	return Job{}, false
}

// Env returns the environment lines in order.
func (c *Crontab) Env() []EnvVar {
	var out []EnvVar
	for _, l := range c.Lines {
		if l.Kind == Env {
			out = append(out, *l.Env)
		}
	}
	return out
}

func normalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// Problem is something in the crontab a human should look at.
type Problem struct {
	Line    int    // 1-based
	Code    string // invalid-line | orphan-meta | bad-meta | duplicate-id
	Message string
}

func (p Problem) String() string { return fmt.Sprintf("line %d: %s", p.Line, p.Message) }

// Problems lists invalid lines, malformed or orphaned meta lines, and
// duplicate job IDs.
func (c *Crontab) Problems() []Problem {
	var out []Problem
	seen := map[string]int{}
	for i, l := range c.Lines {
		n := i + 1
		switch {
		case l.Kind == Invalid:
			out = append(out, Problem{n, "invalid-line", "not a valid cron line: " + l.Err.Error()})
		case l.Kind == Comment && l.Err != nil:
			out = append(out, Problem{n, "bad-meta", l.Err.Error()})
		case l.Orphan:
			out = append(out, Problem{n, "orphan-meta", fmt.Sprintf(
				"gocron job %s has no cron line below it", l.Meta.Get("id"))})
		case l.Kind == JobMeta:
			id := l.Meta.Get("id")
			if first, dup := seen[id]; dup {
				out = append(out, Problem{n, "duplicate-id", fmt.Sprintf(
					"job id %s is also used on line %d", id, first)})
			} else {
				seen[id] = n
			}
		}
	}
	return out
}
