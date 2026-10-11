package cronexpr

import (
	"strconv"
	"strings"
)

// PresetKind is a schedule shape the editor offers friendly inputs for.
type PresetKind int

const (
	EveryNMin PresetKind = iota
	Hourly
	Daily
	Weekdays
	Weekly
	Monthly
	Custom
)

// PresetNames are the presets' labels, indexed by PresetKind.
var PresetNames = []string{"Every N min", "Hourly", "Daily", "Weekdays", "Weekly", "Monthly", "Custom"}

func (k PresetKind) String() string { return PresetNames[k] }

// WeekdayBits is Monday through Friday as a Preset.Days set.
const WeekdayBits uint8 = 0b0111110

// Preset is a schedule in one of the preset shapes. Only the fields its
// Kind uses matter.
type Preset struct {
	Kind   PresetKind
	Every  int   // EveryNMin: 1–59
	Minute int   // Hourly and the fixed-time presets
	Hour   int   // Daily, Weekdays, Weekly, Monthly
	Dom    int   // Monthly: 1–31
	Days   uint8 // Weekly: day-of-week bits, Sunday = bit 0; must not be empty
}

// Expr returns the preset's canonical expression. Custom has none.
func (p Preset) Expr() string {
	m, h := strconv.Itoa(p.Minute), strconv.Itoa(p.Hour)
	switch p.Kind {
	case EveryNMin:
		if p.Every <= 1 {
			return "* * * * *"
		}
		return "*/" + strconv.Itoa(p.Every) + " * * * *"
	case Hourly:
		return m + " * * * *"
	case Daily:
		return m + " " + h + " * * *"
	case Weekdays:
		return m + " " + h + " * * 1-5"
	case Weekly:
		return m + " " + h + " * * " + dowList(p.Days)
	case Monthly:
		return m + " " + h + " " + strconv.Itoa(p.Dom) + " * *"
	}
	return ""
}

// dowList writes day-of-week bits as a cron list in numeric order, with
// runs of three or more days as ranges: "1-3,5".
func dowList(days uint8) string {
	var vals []int
	for d := 0; d < 7; d++ {
		if days&(1<<d) != 0 {
			vals = append(vals, d)
		}
	}
	var parts []string
	for _, r := range runs(vals) {
		if len(r) >= 3 {
			parts = append(parts, itoa(r[0])+"-"+itoa(r[len(r)-1]))
			continue
		}
		for _, v := range r {
			parts = append(parts, itoa(v))
		}
	}
	return strings.Join(parts, ",")
}

// DetectPreset reports which preset, if any, e is an instance of. It
// compares what the schedule matches, not its text, so "0 9 * * mon-fri"
// and "@daily" are found too. Schedules with a time zone prefix, @reboot,
// or restricted months are Custom.
func DetectPreset(e *Expr) (Preset, bool) {
	if e == nil || e.Reboot || e.TZName != "" || !e.monthAll() {
		return Preset{Kind: Custom}, false
	}
	minutes, hours := e.Minute.Values(), e.Hour.Values()
	oneMin, oneHour := len(minutes) == 1, len(hours) == 1
	everyDay := e.domAll() && e.dowAll()

	if everyDay && e.hourAll() {
		if oneMin {
			return Preset{Kind: Hourly, Minute: minutes[0]}, true
		}
		if n := everyN(e.Minute); n > 0 {
			return Preset{Kind: EveryNMin, Every: n}, true
		}
	}
	if !oneMin || !oneHour {
		return Preset{Kind: Custom}, false
	}
	p := Preset{Minute: minutes[0], Hour: hours[0]}
	switch {
	case everyDay:
		p.Kind = Daily
	case e.Dom.Star && e.domAll() && !e.dowAll():
		p.Kind, p.Days = Weekly, uint8(e.Dow.Bits&0x7f)
		if p.Days == WeekdayBits {
			p.Kind, p.Days = Weekdays, 0
		}
	case e.Dow.Star && e.dowAll() && e.Dom.Count() == 1:
		p.Kind, p.Dom = Monthly, e.Dom.Values()[0]
	default:
		return Preset{Kind: Custom}, false
	}
	return p, true
}

// everyN returns N when the minute field matches exactly what "*/N"
// matches (N = 1 for every minute), else 0.
func everyN(f Field) int {
	v := f.Values()
	if len(v) < 2 || v[0] != 0 {
		return 0
	}
	n := v[1]
	var want uint64
	for m := 0; m < 60; m += n {
		want |= 1 << uint(m)
	}
	if f.Bits != want {
		return 0
	}
	return n
}

// Ruler positions: the five fields are 0–4.
const (
	FieldTZ         = 5  // a CRON_TZ= / TZ= prefix
	FieldDescriptor = 6  // an @-descriptor
	FieldExtra      = -1 // past the fifth field
)

// FieldAt returns which part of the schedule raw the cursor (a rune
// offset) is in: 0–4 for minute through day-of-week, FieldTZ,
// FieldDescriptor, or FieldExtra. A cursor in the gap before a word
// belongs to that word; one after trailing space starts the next field.
func FieldAt(raw string, cursor int) int {
	r := []rune(raw)
	cursor = min(max(cursor, 0), len(r))
	type span struct{ start, end int }
	var words []span
	for i := 0; i < len(r); {
		for i < len(r) && (r[i] == ' ' || r[i] == '\t') {
			i++
		}
		if i == len(r) {
			break
		}
		s := i
		for i < len(r) && r[i] != ' ' && r[i] != '\t' {
			i++
		}
		words = append(words, span{s, i})
	}
	idx := len(words) // after trailing space: the next word
	for i, w := range words {
		if cursor <= w.end {
			idx = i
			break
		}
	}
	if idx == len(words) && len(words) > 0 && words[len(words)-1].end == len(r) {
		idx = len(words) - 1
	}
	word := func(i int) string { return string(r[words[i].start:words[i].end]) }
	field := idx
	if len(words) > 0 && (strings.HasPrefix(word(0), "CRON_TZ=") || strings.HasPrefix(word(0), "TZ=")) {
		if idx == 0 {
			return FieldTZ
		}
		field--
		if len(words) > 1 && strings.HasPrefix(word(1), "@") {
			return FieldDescriptor
		}
	} else if len(words) > 0 && strings.HasPrefix(word(0), "@") {
		return FieldDescriptor
	}
	if field > 4 {
		return FieldExtra
	}
	return field
}

// FieldLabels are the ruler's short names for the five fields.
var FieldLabels = []string{"min", "hour", "dom", "mon", "dow"}

// FieldHelp explains what a ruler position accepts.
func FieldHelp(field int) string {
	switch field {
	case 0:
		return "minute · 0–59 · e.g. 0, 30, */15, 0,30"
	case 1:
		return "hour · 0–23 · e.g. 9, 9-17, */2, 8,12,18"
	case 2:
		return "day of month · 1–31 · e.g. 1, 15, 1-7, 1,15"
	case 3:
		return "month · 1–12 or JAN–DEC · e.g. 1, 6-8, */3"
	case 4:
		return "day of week · 0–7 (0 and 7 are Sunday) or SUN–SAT · e.g. 1-5, MON,WED"
	case FieldTZ:
		return "time zone · e.g. CRON_TZ=Europe/Berlin (cronie honors it; Debian cron doesn't)"
	case FieldDescriptor:
		return "descriptor · @hourly @daily @weekly @monthly @yearly @reboot"
	}
	return "cron has only five fields: minute hour day-of-month month day-of-week"
}
