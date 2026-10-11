// Package humanize formats times and durations for people.
package humanize

import (
	"fmt"
	"time"
)

// Duration renders d with its two largest units: "42s", "7m 53s",
// "11h 58m", "1d 13h".
func Duration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	d = d.Truncate(time.Second)
	days, d := d/(24*time.Hour), d%(24*time.Hour)
	hours, d := d/time.Hour, d%time.Hour
	mins, secs := d/time.Minute, (d%time.Minute)/time.Second
	switch {
	case days > 0:
		return pair(int(days), "d", int(hours), "h")
	case hours > 0:
		return pair(int(hours), "h", int(mins), "m")
	case mins > 0:
		return pair(int(mins), "m", int(secs), "s")
	}
	return fmt.Sprintf("%ds", secs)
}

func pair(a int, au string, b int, bu string) string {
	if b == 0 {
		return fmt.Sprintf("%d%s", a, au)
	}
	return fmt.Sprintf("%d%s %d%s", a, au, b, bu)
}

// Next labels a future time relative to now ("in 11h 58m"), or absolutely
// ("Mon 2 Nov 09:00") when it's more than 7 days away.
func Next(t, now time.Time) string {
	d := t.Sub(now)
	if d > 7*24*time.Hour {
		return Absolute(t, now)
	}
	if d < time.Second {
		return "now"
	}
	return "in " + Duration(d)
}

// Absolute renders t in local time as "Sun 12 Oct 02:30", adding the year
// when it differs from now's.
func Absolute(t, now time.Time) string {
	t = t.Local()
	if t.Year() != now.Local().Year() {
		return t.Format("Mon 2 Jan 2006 15:04")
	}
	return t.Format("Mon 2 Jan 15:04")
}
