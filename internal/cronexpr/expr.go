// Package cronexpr parses, describes, and evaluates 5-field cron schedules
// with Vixie cron / cronie semantics.
package cronexpr

import (
	"math/bits"
	"time"
)

// Item is one comma-separated element of a field: a value, a range, or a
// stepped range. A single value has Start == End and Step == 1.
type Item struct {
	Start, End, Step int
	Star             bool // written as "*" or "*/N"
}

// Field is one parsed schedule field.
type Field struct {
	Items []Item
	Bits  uint64 // bit v set => value v matches
	// Star follows Vixie cron: true when the field text begins with "*".
	// It decides the day-of-month / day-of-week OR (see Expr.dayMatches),
	// so "*/2" counts as a star even though it restricts values.
	Star bool
}

// Has reports whether v matches the field.
func (f Field) Has(v int) bool { return v >= 0 && v < 64 && f.Bits&(1<<uint(v)) != 0 }

// Values returns the matching values in ascending order.
func (f Field) Values() []int {
	out := make([]int, 0, bits.OnesCount64(f.Bits))
	for b := f.Bits; b != 0; b &= b - 1 {
		out = append(out, bits.TrailingZeros64(b))
	}
	return out
}

// Count returns the number of matching values.
func (f Field) Count() int { return bits.OnesCount64(f.Bits) }

// Expr is a parsed cron schedule.
type Expr struct {
	Raw        string         // the input, trimmed
	TZ         *time.Location // from a CRON_TZ= / TZ= prefix; nil = caller's location
	TZName     string
	Descriptor string // "@daily" etc. when written as a descriptor, else ""
	Reboot     bool   // @reboot: no calendar schedule

	Minute, Hour, Dom, Month, Dow Field
}

func (e *Expr) dayMatches(t time.Time) bool {
	dom := e.Dom.Has(t.Day())
	dow := e.Dow.Has(int(t.Weekday()))
	if e.Dom.Star || e.Dow.Star {
		return dom && dow
	}
	return dom || dow
}

// usesDomDowOr reports whether day matching is the Vixie OR of two
// restricted fields.
func (e *Expr) usesDomDowOr() bool {
	return !e.Reboot && !e.Dom.Star && !e.Dow.Star
}

func fullBits(min, max int) uint64 {
	var b uint64
	for v := min; v <= max; v++ {
		b |= 1 << uint(v)
	}
	return b
}

func (e *Expr) minuteAll() bool { return e.Minute.Bits == fullBits(0, 59) }
func (e *Expr) hourAll() bool   { return e.Hour.Bits == fullBits(0, 23) }
func (e *Expr) domAll() bool    { return e.Dom.Bits == fullBits(1, 31) }
func (e *Expr) monthAll() bool  { return e.Month.Bits == fullBits(1, 12) }
func (e *Expr) dowAll() bool    { return e.Dow.Bits == fullBits(0, 6) }
