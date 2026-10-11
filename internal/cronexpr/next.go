package cronexpr

import (
	"sort"
	"time"
)

// searchYears bounds the gap between two fire times so schedules that never
// fire (30 2 31 2 *) terminate. Feb 29s can be 8 years apart (2096 → 2104).
const searchYears = 10

// Next parses expr and returns up to n fire times strictly after from.
func Next(expr string, from time.Time, n int) ([]time.Time, error) {
	e, err := Parse(expr)
	if err != nil {
		return nil, err
	}
	return e.Next(from, n), nil
}

// Next returns up to n fire times strictly after from, in ascending order.
// Times are evaluated in e.TZ when set, otherwise in from's location, and
// are returned in that location. @reboot and schedules that never fire
// return an empty slice.
//
// Daylight-saving transitions follow Vixie cron / cronie:
//   - Spring forward: a fixed-time job (neither minute nor hour is "*")
//     whose wall time falls in the skipped interval fires once, at the
//     first instant after the jump. Wildcard jobs simply skip those minutes.
//   - Fall back: a fixed-time job fires only in the first pass through the
//     repeated interval; wildcard jobs fire in both passes.
func (e *Expr) Next(from time.Time, n int) []time.Time {
	if e.Reboot || n <= 0 {
		return nil
	}
	loc := e.TZ
	if loc == nil {
		loc = from.Location()
	}
	fixed := !e.Minute.Star && !e.Hour.Star
	fromUnix := from.Unix()

	// Instants after a fall-back can carry wall times earlier than from's,
	// so start the wall-clock walk early by the size of any nearby shift.
	start := wallUnix(fromUnix, loc)
	start -= maxOffsetSpread(fromUnix, loc)

	var out []time.Time
	w := time.Unix(start, 0).UTC().Truncate(time.Minute)
	for {
		var ok bool
		if w, ok = e.nextWall(w, w.AddDate(searchYears, 0, 0)); !ok {
			break
		}
		insts, gapEnd := instants(w.Unix(), loc)
		earliest := gapEnd
		switch {
		case len(insts) == 0 && fixed:
			insts = []int64{gapEnd}
		case len(insts) > 1:
			earliest = insts[0]
			if fixed {
				insts = insts[:1]
			}
		case len(insts) == 1:
			earliest = insts[0]
		}
		for _, t := range insts {
			if t > fromUnix {
				out = append(out, time.Unix(t, 0).In(loc))
			}
		}
		if len(out) < n {
			continue
		}
		out = sortDedupe(out)
		// The earliest instant of each successive wall time never decreases,
		// so once it passes the n-th result nothing later can displace it.
		if len(out) >= n && earliest >= out[n-1].Unix() {
			return out[:n]
		}
	}
	out = sortDedupe(out)
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// nextWall returns the first matching wall-clock minute strictly after w.
// Wall times are carried as UTC times, which have no DST.
func (e *Expr) nextWall(w, limit time.Time) (time.Time, bool) {
	t := w.Add(time.Minute)
wrap:
	if t.After(limit) {
		return time.Time{}, false
	}
	for !e.Month.Has(int(t.Month())) {
		t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		if t.Month() == time.January {
			goto wrap
		}
	}
	for !e.dayMatches(t) {
		t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
		if t.Day() == 1 {
			goto wrap
		}
	}
	for !e.Hour.Has(t.Hour()) {
		t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, time.UTC)
		if t.Hour() == 0 {
			goto wrap
		}
	}
	for !e.Minute.Has(t.Minute()) {
		t = t.Add(time.Minute)
		if t.Minute() == 0 {
			goto wrap
		}
	}
	return t, true
}

func offset(unix int64, loc *time.Location) int64 {
	_, off := time.Unix(unix, 0).In(loc).Zone()
	return int64(off)
}

// wallUnix returns the wall-clock time of the instant as seconds since the
// epoch, as if the wall clock were UTC.
func wallUnix(unix int64, loc *time.Location) int64 { return unix + offset(unix, loc) }

const window = 15 * 3600 // wider than any UTC offset change

func maxOffsetSpread(unix int64, loc *time.Location) int64 {
	lo, hi := offset(unix, loc), offset(unix, loc)
	for _, d := range []int64{-window, window} {
		o := offset(unix+d, loc)
		lo, hi = min(lo, o), max(hi, o)
	}
	return hi - lo
}

// instants returns every instant whose wall-clock time in loc is wall
// (ascending: two during a fall-back, none during a spring-forward gap).
// For a gap, gapEnd is the first instant after the jump.
func instants(wall int64, loc *time.Location) (out []int64, gapEnd int64) {
	before, after := offset(wall-window, loc), offset(wall+window, loc)
	for _, off := range []int64{before, offset(wall, loc), after} {
		t := wall - off
		if wallUnix(t, loc) != wall {
			continue
		}
		dup := false
		for _, x := range out {
			dup = dup || x == t
		}
		if !dup {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) > 0 {
		return out, 0
	}
	// Gap: binary-search the first instant whose wall time is past wall.
	lo, hi := wall-after, wall-before
	for lo < hi {
		mid := lo + (hi-lo)/2
		if wallUnix(mid, loc) > wall {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return nil, lo
}

func sortDedupe(ts []time.Time) []time.Time {
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	out := ts[:0]
	for i, t := range ts {
		if i == 0 || !t.Equal(out[len(out)-1]) {
			out = append(out, t)
		}
	}
	return out
}
