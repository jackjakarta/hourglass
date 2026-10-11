package cronexpr

import (
	"fmt"
	"strings"
)

// Severity orders lint issues. Errors block saving; the rest are advice.
type Severity int

const (
	Info Severity = iota
	Warn
	Error
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warn:
		return "warning"
	}
	return "info"
}

// Issue is one lint finding.
type Issue struct {
	Severity Severity
	Code     string
	Message  string
}

// Lint codes.
const (
	CodeUnparsable  = "unparsable"
	CodeEveryMinute = "every-minute"
	CodeDomNever    = "dom-never"
	CodeDomRare     = "dom-rare"
	CodeDomDowOr    = "dom-dow-or"
)

// Lint checks a schedule for errors and surprising behavior.
func Lint(expr string) []Issue {
	e, err := Parse(expr)
	if err != nil {
		return []Issue{{Error, CodeUnparsable, err.Error()}}
	}
	if e.Reboot {
		return nil
	}
	var out []Issue
	if e.minuteAll() && e.hourAll() {
		out = append(out, Issue{Info, CodeEveryMinute, "Runs every minute. Make sure that's intended."})
	}
	if e.usesDomDowOr() && !e.domAll() && !e.dowAll() {
		out = append(out, Issue{Info, CodeDomDowOr, fmt.Sprintf(
			"Both day-of-month and day-of-week are set, so cron runs on days matching either one (%s).",
			e.describeDays())})
	}
	// Under the OR rule day-of-week adds days, so a rare day-of-month
	// doesn't make the job rare.
	if !e.usesDomDowOr() {
		out = append(out, e.lintDom()...)
	}
	return out
}

var (
	maxDays = [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	minDays = [13]int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
)

func (e *Expr) lintDom() []Issue {
	first := e.Dom.Values()[0]
	var missing []string // selected months that never have the first selected day
	never, leapOnly := true, true
	for _, m := range e.Month.Values() {
		if first <= maxDays[m] {
			never = false
		} else {
			missing = append(missing, monthNamesFull[m])
		}
		if first <= minDays[m] {
			leapOnly = false
		}
	}
	switch {
	case never:
		return []Issue{{Warn, CodeDomNever, fmt.Sprintf(
			"Never runs: %s never has a %s.", join(missing, "or"), ordinal(first))}}
	case leapOnly:
		return []Issue{{Warn, CodeDomRare, "Only runs on February 29, so only in leap years."}}
	case len(missing) > 0:
		return []Issue{{Warn, CodeDomRare, fmt.Sprintf(
			"Skips months without a %s (%s).", ordinal(first), strings.Join(missing, ", "))}}
	}
	return nil
}
