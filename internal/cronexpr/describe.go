package cronexpr

import (
	"fmt"
	"strings"
)

// Describe parses expr and returns its English description.
func Describe(expr string) (string, error) {
	e, err := Parse(expr)
	if err != nil {
		return "", err
	}
	return e.Describe(), nil
}

// Describe returns a plain-English description of the schedule, using
// 24-hour times, e.g. "Every 15 minutes, between 09:00 and 17:59, Monday
// through Friday".
func (e *Expr) Describe() string {
	if e.Reboot {
		return "At boot"
	}
	timePart, fixedTimes := e.describeTime()
	dayPart := e.describeDays()
	s := timePart
	switch {
	case !fixedTimes && strings.HasPrefix(dayPart, "every day"):
		// "Every hour in January", not "Every hour, every day in January".
		s += strings.TrimPrefix(dayPart, "every day")
	case dayPart != "":
		s += ", " + dayPart
	}
	if e.TZName != "" {
		s += " (" + e.TZName + ")"
	}
	return s
}

// shape classifies a field's value set.
type shape struct {
	kind              shapeKind
	step, first, last int
	values            []int
}

type shapeKind int

const (
	shapeAll   shapeKind = iota // every value
	shapeEvery                  // every step-th value from the field minimum, covering the field
	shapeRange                  // first..last by step (≥ 3 values, ≥ 4 when stepped), not covering
	shapeList                   // explicit values
)

func classify(f Field, min, max int) shape {
	v := f.Values()
	s := shape{kind: shapeList, values: v, step: 1, first: v[0], last: v[len(v)-1]}
	if len(v) == max-min+1 {
		s.kind = shapeAll
		return s
	}
	if len(v) < 2 {
		return s
	}
	step := v[1] - v[0]
	for i := 2; i < len(v); i++ {
		if v[i]-v[i-1] != step {
			return s
		}
	}
	s.step = step
	covering := s.first-step < min && s.last+step > max
	switch {
	case covering && s.first == min:
		s.kind = shapeEvery
	case (step == 1 && len(v) >= 3 || len(v) >= 4) && !(covering && len(v) <= 4):
		s.kind = shapeRange
	}
	return s
}

func (e *Expr) describeTime() (string, bool) {
	m := classify(e.Minute, 0, 59)
	h := classify(e.Hour, 0, 23)
	combos := len(m.values) * len(h.values)

	switch {
	case m.kind == shapeAll && h.kind == shapeAll:
		return "Every minute", false
	case h.kind == shapeList && m.kind != shapeAll && (combos <= 4 || m.kind == shapeList && combos <= 8):
		var times []string
		for _, hv := range h.values {
			for _, mv := range m.values {
				times = append(times, hhmm(hv, mv))
			}
		}
		return "At " + join(times, "and"), true
	case m.kind == shapeList && len(m.values) == 1:
		mv := m.values[0]
		switch h.kind {
		case shapeAll:
			if mv == 0 {
				return "Every hour", false
			}
			return fmt.Sprintf("Every hour at :%02d", mv), false
		case shapeEvery:
			if mv == 0 {
				return fmt.Sprintf("Every %d hours", h.step), false
			}
			return fmt.Sprintf("Every %d hours at :%02d", h.step, mv), false
		case shapeRange:
			return fmt.Sprintf("Every %s from %s to %s", plural(h.step, "hour"), hhmm(h.first, mv), hhmm(h.last, mv)), false
		}
	case m.kind == shapeList && h.kind == shapeAll:
		return "At " + join(mapStrings(m.values, mm), "and") + " past every hour", false
	case h.kind == shapeList && len(h.values) == 1 && m.kind != shapeList:
		hv := h.values[0]
		return fmt.Sprintf("Every %s from %s to %s", plural(m.step, "minute"), hhmm(hv, m.first), hhmm(hv, m.last)), false
	}

	s := capitalize(minuteClause(m))
	if c := hourClause(h); c != "" {
		s += ", " + c
	}
	return s, false
}

func minuteClause(m shape) string {
	switch m.kind {
	case shapeAll:
		return "every minute"
	case shapeEvery:
		return fmt.Sprintf("every %d minutes", m.step)
	case shapeRange:
		return fmt.Sprintf("every %s from :%02d to :%02d", plural(m.step, "minute"), m.first, m.last)
	}
	return "at " + join(mapStrings(m.values, mm), "and") + " past the hour"
}

func hourClause(h shape) string {
	switch h.kind {
	case shapeAll:
		return ""
	case shapeEvery:
		return "during every " + ordinalEvery(h.step) + " hour"
	case shapeRange:
		if h.step == 1 {
			return fmt.Sprintf("between %s and %s", hhmm(h.first, 0), hhmm(h.last, 59))
		}
		return fmt.Sprintf("during every %s hour from %s to %s", ordinalEvery(h.step), hhmm(h.first, 0), hhmm(h.last, 59))
	}
	return "in the " + join(mapStrings(h.values, func(v int) string { return hhmm(v, 0) }), "and") + " " + pluralWord(len(h.values), "hour")
}

func (e *Expr) describeDays() string {
	domAll, dowAll := e.domAll(), e.dowAll()
	var s string
	switch {
	case e.usesDomDowOr() && (domAll || dowAll), domAll && dowAll:
		s = "every day"
	case e.usesDomDowOr():
		return e.domPhrase(false) + " and on " + join(dowPieces(e.Dow), "and") + e.monthSuffix()
	case dowAll:
		return e.domMonthPhrase()
	case domAll:
		s = dowPhrase(e.Dow)
	default: // Vixie AND: one field is a stepped star, e.g. "*/2"
		var names []string
		for _, d := range weekOrder {
			if e.Dow.Has(d) {
				names = append(names, dayNames[d])
			}
		}
		return e.domPhrase(false) + e.monthSuffix() + ", if it's a " + join(names, "or")
	}
	return s + e.monthSuffix()
}

func (e *Expr) domPhrase(ofEveryMonth bool) string {
	d := classify(e.Dom, 1, 31)
	suffix := ""
	if ofEveryMonth {
		suffix = " of every month"
	}
	switch d.kind {
	case shapeEvery:
		return "on every " + ordinalEvery(d.step) + " day of the month"
	case shapeRange:
		if d.step == 1 {
			return fmt.Sprintf("on the %s through %s%s", ordinal(d.first), ordinal(d.last), suffix)
		}
		return fmt.Sprintf("on every %s day from the %s to the %s%s", ordinalEvery(d.step), ordinal(d.first), ordinal(d.last), suffix)
	}
	return "on the " + join(mapStrings(d.values, ordinal), "and") + suffix
}

func (e *Expr) domMonthPhrase() string {
	if e.monthAll() {
		return e.domPhrase(true)
	}
	d := classify(e.Dom, 1, 31)
	if mv := e.Month.Values(); len(mv) == 1 && d.kind == shapeList {
		return "on " + monthNamesFull[mv[0]] + " " + join(mapStrings(d.values, itoa), "and")
	}
	return e.domPhrase(false) + e.monthSuffix()
}

// monthSuffix returns "" for every month, " in January", or
// ", January through March".
func (e *Expr) monthSuffix() string {
	if e.monthAll() {
		return ""
	}
	var pieces []string
	for _, r := range runs(e.Month.Values()) {
		if len(r) >= 3 {
			pieces = append(pieces, monthNamesFull[r[0]]+" through "+monthNamesFull[r[len(r)-1]])
			continue
		}
		for _, v := range r {
			pieces = append(pieces, monthNamesFull[v])
		}
	}
	if len(pieces) == 1 && strings.Contains(pieces[0], " through ") {
		return ", " + pieces[0]
	}
	return " in " + join(pieces, "and")
}

var (
	weekOrder      = []int{1, 2, 3, 4, 5, 6, 0}
	dayNames       = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	monthNamesFull = []string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
)

// dowPieces groups the days, Monday first, into "Monday through Friday"
// runs and plural singles ("Sundays").
func dowPieces(f Field) []string {
	var vals []int
	for _, d := range weekOrder {
		if f.Has(d) {
			vals = append(vals, d)
		}
	}
	var pieces []string
	var run []int
	flush := func() {
		if len(run) >= 3 {
			pieces = append(pieces, dayNames[run[0]]+" through "+dayNames[run[len(run)-1]])
		} else {
			for _, d := range run {
				pieces = append(pieces, dayNames[d]+"s")
			}
		}
		run = nil
	}
	for i, d := range vals {
		if i > 0 && pos(d) != pos(vals[i-1])+1 {
			flush()
		}
		run = append(run, d)
	}
	flush()
	return pieces
}

func pos(d int) int { return (d + 6) % 7 } // Monday = 0 … Sunday = 6

func dowPhrase(f Field) string {
	p := dowPieces(f)
	if len(p) == 1 && strings.Contains(p[0], " through ") {
		return p[0]
	}
	return "on " + join(p, "and")
}

// runs splits sorted values into consecutive runs.
func runs(v []int) [][]int {
	var out [][]int
	for i, x := range v {
		if i == 0 || x != v[i-1]+1 {
			out = append(out, nil)
		}
		out[len(out)-1] = append(out[len(out)-1], x)
	}
	return out
}

func hhmm(h, m int) string { return fmt.Sprintf("%02d:%02d", h, m) }
func mm(m int) string      { return fmt.Sprintf(":%02d", m) }
func itoa(v int) string    { return fmt.Sprint(v) }

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// ordinalEvery renders the N in "every N hour": "other", "3rd", "4th".
func ordinalEvery(n int) string {
	if n == 2 {
		return "other"
	}
	return ordinal(n)
}

func plural(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func pluralWord(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func join(items []string, conj string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " " + conj + " " + items[len(items)-1]
}

func mapStrings(v []int, f func(int) string) []string {
	out := make([]string, len(v))
	for i, x := range v {
		out[i] = f(x)
	}
	return out
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
