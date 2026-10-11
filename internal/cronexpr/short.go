package cronexpr

import "strings"

var dayAbbr = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// Short returns a compact description for list rows: "At 02:30",
// "Sundays at 04:00", "Every 15 minutes on weekdays", "Monthly on the 1st
// at 03:00". Schedules without a compact form fall back to Describe
// (without the time zone).
func (e *Expr) Short() string {
	if e.Reboot {
		return "At boot"
	}
	timePart, fixed := e.describeTime()
	at := strings.TrimPrefix(timePart, "At ") // "02:30" when fixed
	domAll, dowAll, monthAll := e.domAll(), e.dowAll(), e.monthAll()

	switch {
	case domAll && dowAll && monthAll:
		return timePart
	case domAll && !dowAll && monthAll:
		days := shortDays(e.Dow)
		if fixed {
			return days + " at " + at
		}
		return timePart + " on " + lowerFirst(days)
	case dowAll && !domAll && fixed:
		d := classify(e.Dom, 1, 31)
		if d.kind != shapeList || len(d.values) > 3 {
			break
		}
		days := join(mapStrings(d.values, ordinal), "and")
		if monthAll {
			return "Monthly on the " + days + " at " + at
		}
		if mv := e.Month.Values(); len(mv) == 1 && len(d.values) == 1 {
			return "Yearly on " + monthNamesFull[mv[0]][:3] + " " + itoa(d.values[0]) + " at " + at
		}
	}
	return strings.TrimSuffix(e.Describe(), " ("+e.TZName+")")
}

// shortDays names a day-of-week set compactly: "Weekdays", "Weekends",
// "Sundays", "Mon–Wed, Fri".
func shortDays(f Field) string {
	switch f.Bits {
	case 0b0111110:
		return "Weekdays"
	case 0b1000001:
		return "Weekends"
	}
	if vals := f.Values(); len(vals) == 1 {
		return dayNames[vals[0]] + "s"
	}
	var ordered []int
	for _, d := range weekOrder {
		if f.Has(d) {
			ordered = append(ordered, d)
		}
	}
	var pieces []string
	for i := 0; i < len(ordered); {
		j := i
		for j+1 < len(ordered) && pos(ordered[j+1]) == pos(ordered[j])+1 {
			j++
		}
		switch {
		case j-i >= 2:
			pieces = append(pieces, dayAbbr[ordered[i]]+"–"+dayAbbr[ordered[j]])
		default:
			for k := i; k <= j; k++ {
				pieces = append(pieces, dayAbbr[ordered[k]])
			}
		}
		i = j + 1
	}
	return strings.Join(pieces, ", ")
}

// lowerFirst lowercases "Weekdays"/"Weekends"; day names keep their capital.
func lowerFirst(s string) string {
	if s == "Weekdays" || s == "Weekends" {
		return strings.ToLower(s)
	}
	return s
}
