package cronexpr

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseError describes why a schedule is invalid.
type ParseError struct {
	Field string // "minute", "hour", … or "" for whole-expression errors
	Value string
	Msg   string
}

func (e *ParseError) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	if e.Value == "" {
		return e.Field + ": " + e.Msg
	}
	return fmt.Sprintf("%s: %q %s", e.Field, e.Value, e.Msg)
}

type fieldSpec struct {
	name     string
	min, max int
	names    map[string]int
}

var (
	monthNames = map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}
	dowNames = map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}
	specs = [5]fieldSpec{
		{"minute", 0, 59, nil},
		{"hour", 0, 23, nil},
		{"day-of-month", 1, 31, nil},
		{"month", 1, 12, monthNames},
		{"day-of-week", 0, 7, dowNames}, // 7 is Sunday, folded into 0
	}
)

// Descriptors maps each supported @-descriptor to its 5-field expansion.
// @reboot has no expansion.
var Descriptors = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
	"@reboot":   "",
}

// Parse parses a cron schedule: five fields or an @-descriptor, optionally
// prefixed with "CRON_TZ=<zone> " or "TZ=<zone> ".
func Parse(s string) (*Expr, error) {
	e := &Expr{Raw: strings.TrimSpace(s)}
	rest := e.Raw
	if rest == "" {
		return nil, &ParseError{Msg: "empty schedule"}
	}

	for _, prefix := range []string{"CRON_TZ=", "TZ="} {
		if !strings.HasPrefix(rest, prefix) {
			continue
		}
		zone, after := rest[len(prefix):], ""
		if i := strings.IndexAny(zone, " \t"); i >= 0 {
			zone, after = zone[:i], strings.TrimSpace(zone[i:])
		}
		loc, err := time.LoadLocation(zone)
		if err != nil || zone == "" {
			return nil, &ParseError{Field: "timezone", Value: zone, Msg: "is not a known time zone"}
		}
		e.TZ, e.TZName, rest = loc, zone, after
		break
	}

	if strings.HasPrefix(rest, "@") {
		word := strings.ToLower(rest)
		if strings.HasPrefix(word, "@every") {
			return nil, &ParseError{Msg: "@every isn't supported by cron; use a step like */15"}
		}
		if strings.ContainsAny(word, " \t") {
			return nil, &ParseError{Msg: fmt.Sprintf("unexpected text after %s", strings.Fields(rest)[0])}
		}
		exp, ok := Descriptors[word]
		if !ok {
			return nil, &ParseError{Msg: fmt.Sprintf("unknown descriptor %s (use @yearly, @monthly, @weekly, @daily, @hourly or @reboot)", rest)}
		}
		e.Descriptor = word
		if word == "@reboot" {
			e.Reboot = true
			return e, nil
		}
		rest = exp
	}

	fields := strings.Fields(rest)
	if len(fields) != 5 {
		msg := fmt.Sprintf("expected 5 fields (minute hour day-of-month month day-of-week), got %d", len(fields))
		if len(fields) == 6 {
			msg += "; cron has no seconds field"
		}
		return nil, &ParseError{Msg: msg}
	}
	dst := [5]*Field{&e.Minute, &e.Hour, &e.Dom, &e.Month, &e.Dow}
	for i, text := range fields {
		f, err := parseField(text, specs[i])
		if err != nil {
			return nil, err
		}
		*dst[i] = f
	}
	if e.Dow.Has(7) {
		e.Dow.Bits = e.Dow.Bits&^(1<<7) | 1
	}
	return e, nil
}

// MustParse is Parse that panics on error. For tests and constants.
func MustParse(s string) *Expr {
	e, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return e
}

// Validate reports whether s is a valid schedule.
func Validate(s string) error {
	_, err := Parse(s)
	return err
}

func parseField(text string, spec fieldSpec) (Field, error) {
	f := Field{Star: strings.HasPrefix(text, "*")}
	for _, part := range strings.Split(text, ",") {
		it, err := parseItem(part, spec)
		if err != nil {
			return Field{}, err
		}
		f.Items = append(f.Items, it)
		for v := it.Start; v <= it.End; v += it.Step {
			f.Bits |= 1 << uint(v)
		}
	}
	return f, nil
}

func parseItem(part string, spec fieldSpec) (Item, error) {
	fail := func(msg string) (Item, error) {
		return Item{}, &ParseError{Field: spec.name, Value: part, Msg: msg}
	}
	if part == "" {
		return fail("has an empty list element")
	}
	if strings.Contains(part, "?") {
		return fail("uses ?, which cron doesn't support; use *")
	}
	rng, stepText, hasStep := strings.Cut(part, "/")
	it := Item{Step: 1}
	switch {
	case rng == "*":
		it.Start, it.End, it.Star = spec.min, spec.max, true
	case strings.Contains(rng, "-"):
		lo, hi, _ := strings.Cut(rng, "-")
		var err error
		if it.Start, err = value(lo, spec); err != nil {
			return fail(err.Error())
		}
		if it.End, err = value(hi, spec); err != nil {
			return fail(err.Error())
		}
		if it.Start > it.End {
			return fail(fmt.Sprintf("is a reversed range (%d > %d)", it.Start, it.End))
		}
	default:
		v, err := value(rng, spec)
		if err != nil {
			return fail(err.Error())
		}
		it.Start, it.End = v, v
		if hasStep { // "N/step" means N through max, as in cronie
			it.End = spec.max
		}
	}
	if hasStep {
		n, err := strconv.Atoi(stepText)
		if err != nil || n <= 0 {
			return fail("has an invalid step (must be a positive number)")
		}
		it.Step = n
	}
	return it, nil
}

func value(s string, spec fieldSpec) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("is missing a value")
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		if n, ok := spec.names[strings.ToLower(s)]; ok {
			return n, nil
		}
		if spec.names != nil {
			return 0, fmt.Errorf("is not a number or a 3-letter name")
		}
		return 0, fmt.Errorf("is not a number")
	}
	if v < spec.min || v > spec.max {
		return 0, fmt.Errorf("is out of range (%d-%d)", spec.min, spec.max)
	}
	return v, nil
}
