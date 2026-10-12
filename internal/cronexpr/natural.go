package cronexpr

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Natural is a schedule read from plain English.
type Natural struct {
	Expr string
	// Note points out something the expression does that the phrase may
	// not have meant, e.g. a default time; "" when there is nothing to add.
	Note string
}

// NaturalError says why a phrase couldn't become a schedule, and what to
// do instead.
type NaturalError struct {
	Msg  string
	Hint string // "" when there is no better suggestion
}

func (e *NaturalError) Error() string {
	if e.Hint == "" {
		return e.Msg
	}
	return e.Msg + "; " + e.Hint
}

func naturalErr(msg, hint string, args ...any) *NaturalError {
	return &NaturalError{Msg: fmt.Sprintf(msg, args...), Hint: hint}
}

// ParseNatural turns a phrase like "every weekday at 9am", "every 15
// minutes between 9 and 17" or "on the 1st and 15th at noon" into a cron
// expression. Phrases cron can't express ("first monday of the month",
// "every 90 minutes") fail with a *NaturalError that explains why.
func ParseNatural(phrase string) (Natural, error) {
	toks := tokenize(phrase)
	if len(toks) == 0 {
		return Natural{}, naturalErr("say when, e.g. \"every weekday at 9am\"", "")
	}
	p := &nlParser{toks: toks}
	if err := p.parse(); err != nil {
		return Natural{}, err
	}
	n, err := p.build()
	if err != nil {
		return Natural{}, err
	}
	if _, err := Parse(n.Expr); err != nil {
		return Natural{}, naturalErr("that came out as %q, which isn't a valid schedule: %v", "", n.Expr, err)
	}
	return n, nil
}

var (
	reSpaces   = regexp.MustCompile(`\s+`)
	reNumRange = regexp.MustCompile(`(\d)\s*-\s*(\d)`)
	reAMPM     = regexp.MustCompile(`\b([ap])\.?m\.?(\s|$)`)
	reTime     = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?(am|pm)?$`)
	reColonMin = regexp.MustCompile(`^:(\d{2})$`)
	reOrdinal  = regexp.MustCompile(`^(\d{1,2})(st|nd|rd|th)$`)
	reNumber   = regexp.MustCompile(`^\d+$`)
)

func tokenize(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("’", "'", "&", " and ", ",", " , ", ";", " ", "+", " and ").Replace(s)
	s = strings.TrimRight(s, ".!?")
	s = strings.NewReplacer("start-up", "startup", "forty-five", "fortyfive", "every-other", "every other").Replace(s)
	s = reAMPM.ReplaceAllString(s, "${1}m$2")
	s = reNumRange.ReplaceAllString(s, "$1 to $2")
	// "mon-fri", "9am-5pm"
	s = strings.ReplaceAll(s, "-", " - ")
	var out []string
	for _, t := range strings.Fields(reSpaces.ReplaceAllString(s, " ")) {
		switch t {
		case ",":
			out = append(out, "and")
		case "-":
			out = append(out, "to")
		default:
			out = append(out, t)
		}
	}
	return out
}

type clock struct{ h, m int }

type nlParser struct {
	toks []string
	i    int

	reboot      bool
	everyMin    int // every N minutes
	everyHour   int // every N hours
	everyMonth  int // every N months
	minuteAt    int // a minute past the hour, -1 when unset
	minuteSet   bool
	times       []clock
	hourFrom    *clock // between … and …
	hourTo      *clock
	doms        []int
	months      []int
	dows        []int
	daily       bool
	weekly      bool
	monthly     bool
	yearly      bool
	hourly      bool
	notes       []string
	understood  bool // something besides filler was read
	timesPerDay int
}

var fillers = map[string]bool{
	"every": true, "each": true, "at": true, "on": true, "in": true, "the": true, "of": true, "a": true,
	"an": true, "and": true, "run": true, "runs": true, "it": true, "job": true, "please": true, "starting": true,
	"o'clock": true, "oclock": true, "morning": true, "only": true, "also": true, "then": true, "cron": true, "schedule": true,
}

func (p *nlParser) peek(k int) string {
	if p.i+k < len(p.toks) {
		return p.toks[p.i+k]
	}
	return ""
}

func (p *nlParser) parse() error {
	for p.i < len(p.toks) {
		n, err := p.step()
		if err != nil {
			return err
		}
		if n == 0 {
			t := p.toks[p.i]
			if fillers[t] {
				p.i++
				continue
			}
			return naturalErr("gocron doesn't understand %q", "try phrases like \"every 15 minutes\", \"weekdays at 9am\" or \"on the 1st at noon\"", t)
		}
		p.understood = true
		p.i += n
	}
	if !p.understood {
		return naturalErr("say when, e.g. \"every weekday at 9am\"", "")
	}
	return nil
}

// step reads one phrase at the cursor and returns how many tokens it
// used; 0 means nothing matched there.
func (p *nlParser) step() (int, error) {
	for _, f := range []func() (int, error){
		p.unsupported, p.boot, p.interval, p.timesA, p.offset, p.hourRange, p.dayOfWeek, p.dayOfMonth, p.month, p.frequency, p.timeList,
	} {
		if n, err := f(); err != nil || n > 0 {
			return n, err
		}
	}
	return 0, nil
}

var ordinalWords = map[string]int{"first": 1, "second": 2, "third": 3, "fourth": 4, "fifth": 5, "last": -1}

func (p *nlParser) unsupported() (int, error) {
	t, next := p.peek(0), p.peek(1)
	if _, ok := ordinalWords[t]; ok || reOrdinal.MatchString(t) {
		if _, isDay := nlWeekdays[strings.TrimSuffix(next, "s")]; isDay || next == "weekday" {
			return 0, naturalErr("cron can't run a job on the %s %s of the month", "it ORs day-of-month and weekday; use \"0 9 1-7 * *\" and start the command with [ \"$(date +\\%%u)\" = 1 ] && (1 = Monday)", t, next)
		}
	}
	switch {
	case t == "last" && (next == "day" || next == "days"):
		return 0, naturalErr("cron has no \"last day of the month\"", "use \"0 0 28-31 * *\" and start the command with [ \"$(date -d tomorrow +\\%%d)\" = 01 ] &&")
	case t == "every" && next == "other" && (p.peek(2) == "day" || p.peek(2) == "week"):
		return 0, naturalErr("cron can't count every other %s", "it restarts counting each month; a day-of-month step like \"0 0 */2 * *\" is the closest", p.peek(2))
	case t == "biweekly" || t == "fortnightly":
		return 0, naturalErr("cron can't count every other week", "run it weekly and check the week number in the command")
	case strings.HasPrefix(t, "sec"):
		return 0, naturalErr("cron can't run more often than once a minute", "")
	}
	return 0, nil
}

func (p *nlParser) boot() (int, error) {
	switch p.peek(0) {
	case "reboot", "boot", "startup", "@reboot":
		p.reboot = true
		return 1, nil
	}
	return 0, nil
}

var numberWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	"eleven": 11, "twelve": 12, "fifteen": 15, "twenty": 20, "thirty": 30, "forty": 40, "fortyfive": 45, "sixty": 60,
	"a": 1, "an": 1, "other": 2,
}

func number(t string) (int, bool) {
	if reNumber.MatchString(t) {
		n, err := strconv.Atoi(t)
		return n, err == nil
	}
	n, ok := numberWords[t]
	return n, ok
}

func unit(t string) string {
	switch t {
	case "minute", "minutes", "min", "mins", "m":
		return "minute"
	case "hour", "hours", "hr", "hrs", "h":
		return "hour"
	case "day", "days":
		return "day"
	case "week", "weeks":
		return "week"
	case "month", "months":
		return "month"
	case "year", "years":
		return "year"
	case "quarter", "quarters":
		return "quarter"
	}
	if strings.HasPrefix(t, "sec") {
		return "second"
	}
	return ""
}

// interval reads "every 15 minutes", "every 2 hours", "every half hour",
// "every 3 months".
func (p *nlParser) interval() (int, error) {
	if t := p.peek(0); t != "every" && t != "each" {
		return 0, nil
	}
	k := 1
	n := 1
	switch p.peek(1) {
	case "half":
		k, n = 2, 30
		if p.peek(2) == "an" || p.peek(2) == "a" {
			k++
		}
		if unit(p.peek(k)) != "hour" {
			return 0, nil
		}
		return p.setEvery(k+1, n, "minute")
	case "quarter":
		if p.peek(2) == "hour" || (p.peek(2) == "of" && p.peek(4) == "hour") {
			end := 3
			if p.peek(2) == "of" {
				end = 5
			}
			return p.setEvery(end, 15, "minute")
		}
		return p.setEvery(2, 3, "month") // every quarter
	}
	if v, ok := number(p.peek(1)); ok && unit(p.peek(2)) != "" {
		if p.peek(1) == "a" || p.peek(1) == "an" {
			return 0, nil
		}
		k, n = 2, v
	}
	u := unit(p.peek(k))
	switch u {
	case "minute", "hour", "second":
		return p.setEvery(k+1, n, u)
	case "day", "week", "month", "year", "quarter":
		if n == 1 {
			return 0, nil // "every day" etc.: frequency handles it
		}
		return p.setEvery(k+1, n, u)
	}
	return 0, nil
}

func (p *nlParser) setEvery(used, n int, u string) (int, error) {
	if n < 1 {
		return 0, naturalErr("every 0 %ss isn't a schedule", "", u)
	}
	switch u {
	case "second":
		return 0, naturalErr("cron can't run more often than once a minute", "")
	case "minute":
		switch {
		case n >= 60 && n%60 == 0:
			return p.setEvery(used, n/60, "hour")
		case n >= 60:
			return 0, naturalErr("cron can't run a job every %d minutes", "its steps restart every hour; pick a divisor of 60 (like 30) or of 24 hours, or list the times", n)
		case 60%n != 0:
			p.notes = append(p.notes, fmt.Sprintf("the count restarts each hour, so :%02d is followed by :00", (59/n)*n))
		}
		p.everyMin = n
	case "hour":
		switch {
		case n >= 24 && n%24 == 0:
			return p.setEvery(used, n/24, "day")
		case n >= 24:
			return 0, naturalErr("cron can't run a job every %d hours", "its steps restart every day; pick a divisor of 24 (like 6 or 12)", n)
		case 24%n != 0:
			p.notes = append(p.notes, fmt.Sprintf("the count restarts each day, so %02d:00 is followed by 00:00", (23/n)*n))
		}
		p.everyHour = n
	case "day":
		if n == 1 {
			p.daily = true
			return used, nil
		}
		return 0, naturalErr("cron can't run a job every %d days", fmt.Sprintf("a day-of-month step like \"0 0 */%d * *\" is close, but it restarts on the 1st of each month", n), n)
	case "week":
		if n == 1 {
			p.weekly = true
			return used, nil
		}
		return 0, naturalErr("cron can't run a job every %d weeks", "run it weekly and check the week number in the command", n)
	case "month":
		if n == 1 {
			p.monthly = true
			return used, nil
		}
		if n > 12 || 12%n != 0 {
			return 0, naturalErr("cron can't run a job every %d months", "pick 2, 3, 4 or 6, or list the months", n)
		}
		p.everyMonth = n
	case "quarter":
		p.everyMonth = 3 * n
		if p.everyMonth > 12 || 12%p.everyMonth != 0 {
			return 0, naturalErr("cron can't run a job every %d quarters", "", n)
		}
	case "year":
		return 0, naturalErr("cron can't run a job every %d years", "", n)
	}
	return used, nil
}

// timesA reads "twice a day", "3 times an hour", "once a week".
func (p *nlParser) timesA() (int, error) {
	n, k := 0, 0
	switch t := p.peek(0); t {
	case "once":
		n, k = 1, 1
	case "twice":
		n, k = 2, 1
	default:
		v, ok := number(t)
		if !ok || (p.peek(1) != "times" && p.peek(1) != "time") {
			return 0, nil
		}
		n, k = v, 2
	}
	if a := p.peek(k); a != "a" && a != "an" && a != "per" && a != "every" && a != "each" {
		return 0, nil
	}
	switch unit(p.peek(k + 1)) {
	case "hour":
		if n < 1 || 60%n != 0 {
			return 0, naturalErr("cron can't spread %d runs evenly over an hour", "pick 1, 2, 3, 4, 6 or 12", n)
		}
		p.everyMin = 60 / n
		if n == 1 {
			p.everyMin, p.everyHour = 0, 1
		}
	case "day":
		if n < 1 || 24%n != 0 {
			return 0, naturalErr("cron can't spread %d runs evenly over a day", "pick 1, 2, 3, 4, 6, 8 or 12, or list the times", n)
		}
		p.timesPerDay = n
	case "week":
		if n != 1 {
			return 0, naturalErr("name the days instead, e.g. \"mondays and thursdays at 9am\"", "")
		}
		p.weekly = true
	case "month":
		if n != 1 {
			return 0, naturalErr("name the days instead, e.g. \"on the 1st and 15th\"", "")
		}
		p.monthly = true
	default:
		return 0, nil
	}
	return k + 2, nil
}

// offset reads a minute past the hour: "at :15", "15 minutes past the
// hour", "on the half hour", "quarter past every hour".
func (p *nlParser) offset() (int, error) {
	set := func(m, used int) (int, error) {
		if m > 59 {
			return 0, naturalErr("there's no minute %d in an hour", "", m)
		}
		p.minuteAt, p.minuteSet = m, true
		return used, nil
	}
	if m := reColonMin.FindStringSubmatch(p.peek(0)); m != nil {
		v, _ := strconv.Atoi(m[1])
		return set(v, 1)
	}
	if p.peek(0) == "half" && p.peek(1) == "hour" {
		return set(30, 2)
	}
	if p.peek(0) == "top" && p.peek(1) == "of" {
		k := 2
		if p.peek(k) == "the" || p.peek(k) == "every" {
			k++
		}
		if unit(p.peek(k)) == "hour" {
			return set(0, k+1)
		}
	}
	m, k := 0, 0
	switch {
	case p.peek(0) == "half":
		m, k = 30, 1
	case p.peek(0) == "quarter":
		m, k = 15, 1
	default:
		v, ok := number(p.peek(0))
		if !ok || p.peek(0) == "a" || p.peek(0) == "an" {
			return 0, nil
		}
		m, k = v, 1
		if unit(p.peek(1)) == "minute" {
			k = 2
		}
	}
	if p.peek(k) != "past" && p.peek(k) != "after" {
		return 0, nil
	}
	k++
	for p.peek(k) == "the" || p.peek(k) == "every" || p.peek(k) == "each" || p.peek(k) == "an" {
		k++
	}
	if unit(p.peek(k)) == "hour" {
		if p.everyHour == 0 && p.everyMin == 0 {
			p.everyHour = 1
		}
		return set(m, k+1)
	}
	// "half past 9", "quarter past 10am"
	if c, n, ok := p.clockAt(k, true); ok {
		p.times = append(p.times, clock{c.h, m})
		return k + n, nil
	}
	return 0, nil
}

// clockAt reads a time of day at cursor+k: "9am", "9 am", "14:30",
// "noon"; bare numbers like "9" only when bare is set.
func (p *nlParser) clockAt(k int, bare bool) (clock, int, bool) {
	t := p.peek(k)
	switch t {
	case "noon", "midday", "lunchtime":
		return clock{12, 0}, 1, true
	case "midnight":
		return clock{0, 0}, 1, true
	}
	m := reTime.FindStringSubmatch(t)
	if m == nil {
		return clock{}, 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mi := 0
	if m[2] != "" {
		mi, _ = strconv.Atoi(m[2])
	}
	suffix, n := m[3], 1
	if suffix == "" && (p.peek(k+1) == "am" || p.peek(k+1) == "pm") {
		suffix, n = p.peek(k+1), 2
	}
	switch {
	case suffix != "":
		if h < 1 || h > 12 {
			return clock{}, 0, false
		}
		if h == 12 {
			h = 0
		}
		if suffix == "pm" {
			h += 12
		}
	case m[2] == "" && !bare:
		return clock{}, 0, false
	}
	if h > 23 || mi > 59 {
		return clock{}, 0, false
	}
	if p.peek(k+n) == "o'clock" || p.peek(k+n) == "oclock" {
		n++
	}
	return clock{h, mi}, n, true
}

// timeList reads "at 9am", "9:30 and 17:00", "at 9 and 5pm".
func (p *nlParser) timeList() (int, error) {
	k := 0
	bare := false
	if p.peek(0) == "at" {
		k, bare = 1, true
	}
	c, n, ok := p.clockAt(k, bare)
	if !ok {
		return 0, nil
	}
	if next := p.peek(k + n); unit(next) != "" || next == "past" || next == "after" || next == "times" {
		return 0, nil // "at 15 minutes past the hour"
	}
	got := []clock{c}
	k += n
	for p.peek(k) == "and" {
		c, n, ok := p.clockAt(k+1, true)
		if !ok {
			break
		}
		got = append(got, c)
		k += 1 + n
	}
	p.times = append(p.times, got...)
	return k, nil
}

// hourRange reads "between 9am and 5pm", "from 9 to 17", "9am to 5pm",
// "during business hours".
func (p *nlParser) hourRange() (int, error) {
	if p.peek(0) == "during" || p.peek(0) == "in" || p.peek(0) == "within" {
		k := 1
		if p.peek(k) == "the" {
			k++
		}
		switch p.peek(k) {
		case "business", "work", "working", "office":
			if p.peek(k+1) == "hours" {
				p.hourFrom, p.hourTo = &clock{9, 0}, &clock{17, 0}
				return k + 2, nil
			}
		}
		return 0, nil
	}
	k := 0
	sep := map[string]bool{"to": true, "until": true, "till": true, "til": true, "through": true, "thru": true}
	switch p.peek(0) {
	case "between":
		k = 1
		sep = map[string]bool{"and": true}
	case "from":
		k = 1
	}
	from, n, ok := p.clockAt(k, k > 0)
	if !ok {
		return 0, nil
	}
	if !sep[p.peek(k+n)] {
		return 0, nil
	}
	to, m, ok := p.clockAt(k+n+1, true)
	if !ok {
		return 0, nil
	}
	p.hourFrom, p.hourTo = &from, &to
	if from.m != 0 || to.m != 0 {
		p.notes = append(p.notes, "cron ranges are whole hours, so the minutes in the range were dropped")
	}
	return k + n + 1 + m, nil
}

var nlWeekdays = map[string]int{
	"sun": 0, "sunday": 0, "mon": 1, "monday": 1, "tue": 2, "tues": 2, "tuesday": 2, "wed": 3, "weds": 3, "wednesday": 3,
	"thu": 4, "thur": 4, "thurs": 4, "thursday": 4, "fri": 5, "friday": 5, "sat": 6, "saturday": 6,
}

func weekdayAt(t string) (int, bool) {
	if d, ok := nlWeekdays[t]; ok {
		return d, true
	}
	d, ok := nlWeekdays[strings.TrimSuffix(t, "s")]
	return d, ok
}

// dayOfWeek reads "monday", "mondays and fridays", "mon to fri",
// "weekdays", "weekends".
func (p *nlParser) dayOfWeek() (int, error) {
	switch p.peek(0) {
	case "weekday", "weekdays", "workday", "workdays", "weeknight", "weeknights":
		p.dows = append(p.dows, 1, 2, 3, 4, 5)
		return 1, nil
	case "weekend", "weekends":
		p.dows = append(p.dows, 0, 6)
		return 1, nil
	case "business", "working", "work":
		if p.peek(1) == "day" || p.peek(1) == "days" {
			p.dows = append(p.dows, 1, 2, 3, 4, 5)
			return 2, nil
		}
		return 0, nil
	}
	k := 0
	if p.peek(0) == "from" {
		k = 1
	}
	d, ok := weekdayAt(p.peek(k))
	if !ok {
		return 0, nil
	}
	switch p.peek(k + 1) {
	case "to", "through", "thru", "until", "till":
		e, ok := weekdayAt(p.peek(k + 2))
		if !ok {
			return 0, naturalErr("%q needs a day after it", "", p.peek(k+1))
		}
		for x := d; ; x = (x + 1) % 7 {
			p.dows = append(p.dows, x)
			if x == e {
				break
			}
		}
		return k + 3, nil
	}
	if k > 0 {
		return 0, nil
	}
	p.dows = append(p.dows, d)
	return 1, nil
}

// dayOfMonth reads "the 1st", "1st and 15th", "day 5", "first day",
// "the 15th of the month".
func (p *nlParser) dayOfMonth() (int, error) {
	add := func(d, used int) (int, error) {
		if d < 1 || d > 31 {
			return 0, naturalErr("there's no day %d in a month", "", d)
		}
		p.doms = append(p.doms, d)
		return used, nil
	}
	t := p.peek(0)
	if m := reOrdinal.FindStringSubmatch(t); m != nil {
		d, _ := strconv.Atoi(m[1])
		used := 1
		if p.peek(1) == "day" {
			used = 2
		}
		return add(d, used)
	}
	if d, ok := ordinalWords[t]; ok && d > 0 && (p.peek(1) == "day" || p.peek(1) == "of") {
		used := 1
		if p.peek(1) == "day" {
			used = 2
		}
		return add(d, used)
	}
	if t == "day" {
		if d, ok := number(p.peek(1)); ok && reNumber.MatchString(p.peek(1)) {
			return add(d, 2)
		}
	}
	// "1 january": a bare number before a month name.
	if reNumber.MatchString(t) {
		if _, ok := nlMonths[p.peek(1)]; ok {
			d, _ := strconv.Atoi(t)
			return add(d, 1)
		}
	}
	return 0, nil
}

var nlMonths = map[string]int{
	"jan": 1, "january": 1, "feb": 2, "february": 2, "mar": 3, "march": 3, "apr": 4, "april": 4, "may": 5,
	"jun": 6, "june": 6, "jul": 7, "july": 7, "aug": 8, "august": 8, "sep": 9, "sept": 9, "september": 9,
	"oct": 10, "october": 10, "nov": 11, "november": 11, "dec": 12, "december": 12,
}

// month reads "january", "jan 1", "january 1st", "jan to mar".
func (p *nlParser) month() (int, error) {
	m, ok := nlMonths[p.peek(0)]
	if !ok {
		return 0, nil
	}
	switch p.peek(1) {
	case "to", "through", "thru", "until":
		e, ok := nlMonths[p.peek(2)]
		if !ok {
			return 0, nil
		}
		for x := m; ; x = x%12 + 1 {
			p.months = append(p.months, x)
			if x == e {
				break
			}
		}
		return 3, nil
	}
	p.months = append(p.months, m)
	next := p.peek(1)
	if o := reOrdinal.FindStringSubmatch(next); o != nil {
		next = o[1]
	}
	if reNumber.MatchString(next) {
		// "jan 1", but not "jan 9am".
		if _, _, isTime := p.clockAt(1, false); !isTime || reOrdinal.MatchString(p.peek(1)) {
			if d, _ := strconv.Atoi(next); d >= 1 && d <= 31 && p.peek(2) != "am" && p.peek(2) != "pm" {
				p.doms = append(p.doms, d)
				return 2, nil
			}
		}
	}
	return 1, nil
}

// frequency reads "daily", "every day", "weekly", "monthly", "hourly",
// "yearly", "nightly", "every minute".
func (p *nlParser) frequency() (int, error) {
	switch t := p.peek(0); t {
	case "daily", "nightly", "everyday", "day", "days", "night", "nights":
		p.daily = true
	case "weekly", "week":
		p.weekly = true
	case "monthly", "month":
		p.monthly = true
	case "yearly", "annually", "year", "annual":
		p.yearly = true
	case "hourly", "hour":
		p.hourly = true
	case "minute":
		p.everyMin = 1
	default:
		return 0, nil
	}
	return 1, nil
}

func (p *nlParser) build() (Natural, error) {
	if p.reboot {
		if len(p.times) > 0 || p.everyMin > 0 || p.everyHour > 0 || len(p.dows) > 0 || len(p.doms) > 0 {
			return Natural{}, naturalErr("a job that runs at boot can't have a schedule too", "make it two jobs")
		}
		return Natural{Expr: "@reboot"}, nil
	}

	var minute, hour string
	hours, err := p.rangeHours()
	if err != nil {
		return Natural{}, err
	}
	if p.timesPerDay > 0 {
		step := 24 / p.timesPerDay
		for h := 0; h < 24; h += step {
			p.times = append(p.times, clock{h, 0})
		}
	}
	switch {
	case p.everyMin > 0:
		minute = "*"
		if p.everyMin > 1 {
			minute = "*/" + strconv.Itoa(p.everyMin)
		}
		if len(p.times) > 0 {
			hs := []int{}
			for _, c := range p.times {
				hs = append(hs, c.h)
			}
			hours = hs
		}
		hour = fieldList(hours, "*")
	case p.everyHour > 0:
		minute = "0"
		if p.minuteSet {
			minute = strconv.Itoa(p.minuteAt)
		}
		hour = stepHours(hours, p.everyHour)
	case len(p.times) > 0:
		if p.minuteSet {
			return Natural{}, naturalErr("give either times of day or a minute past the hour, not both", "")
		}
		var err error
		minute, hour, err = timesFields(p.times)
		if err != nil {
			return Natural{}, err
		}
		if hours != nil {
			return Natural{}, naturalErr("give either times of day or a range of hours, not both", "")
		}
	case p.minuteSet || p.hourly:
		minute = "0"
		if p.minuteSet {
			minute = strconv.Itoa(p.minuteAt)
		}
		hour = fieldList(hours, "*")
	case hours != nil:
		minute, hour = "0", fieldList(hours, "*")
		p.notes = append(p.notes, "no frequency given, so it runs at the top of each hour")
	case len(p.dows) > 0 || len(p.doms) > 0 || len(p.months) > 0 || p.daily || p.weekly || p.monthly || p.yearly || p.everyMonth > 0:
		minute, hour = "0", "0"
		p.notes = append(p.notes, "no time given, so it runs at midnight")
	default:
		return Natural{}, naturalErr("say when, e.g. \"every weekday at 9am\"", "")
	}

	dom := fieldList(uniq(p.doms), "*")
	month := fieldList(uniq(p.months), "*")
	if p.everyMonth > 0 {
		if len(p.months) > 0 {
			return Natural{}, naturalErr("give either months or a step of months, not both", "")
		}
		month = "*/" + strconv.Itoa(p.everyMonth)
	}
	dow := fieldList(uniq(p.dows), "*")

	switch {
	case p.yearly && len(p.doms) == 0 && len(p.dows) == 0:
		dom = "1"
		if len(p.months) == 0 {
			month = "1"
		}
	case (p.monthly || p.everyMonth > 0) && len(p.doms) == 0 && len(p.dows) == 0:
		dom = "1"
	case p.weekly && len(p.dows) == 0 && len(p.doms) == 0:
		dow = "0"
	}
	if len(p.doms) > 0 && len(p.dows) > 0 {
		p.notes = append(p.notes, "cron runs it when either the day of the month or the weekday matches")
	}
	return Natural{Expr: strings.Join([]string{minute, hour, dom, month, dow}, " "), Note: strings.Join(uniqStrings(p.notes), "; ")}, nil
}

// rangeHours returns the hours a "between" range covers, nil when there
// is none.
func (p *nlParser) rangeHours() ([]int, error) {
	if p.hourFrom == nil {
		return nil, nil
	}
	from, to := p.hourFrom.h, p.hourTo.h
	if from == to {
		return nil, naturalErr("that range starts and ends in the same hour", "")
	}
	var hs []int
	for h := from; ; h = (h + 1) % 24 {
		hs = append(hs, h)
		if h == to {
			break
		}
	}
	return hs, nil
}

func stepHours(hours []int, step int) string {
	if hours == nil {
		if step == 1 {
			return "*"
		}
		return "*/" + strconv.Itoa(step)
	}
	if step == 1 {
		return fieldList(hours, "*")
	}
	contiguous := hours[len(hours)-1] > hours[0]
	if contiguous {
		return fmt.Sprintf("%d-%d/%d", hours[0], hours[len(hours)-1], step)
	}
	var picked []int
	for i := 0; i < len(hours); i += step {
		picked = append(picked, hours[i])
	}
	return fieldList(picked, "*")
}

func timesFields(times []clock) (minute, hour string, err error) {
	var hs, ms []int
	for _, c := range times {
		hs, ms = append(hs, c.h), append(ms, c.m)
	}
	hs, ms = uniq(hs), uniq(ms)
	switch {
	case len(ms) == 1:
		return strconv.Itoa(ms[0]), fieldList(hs, "*"), nil
	case len(hs) == 1:
		return fieldList(ms, "*"), strconv.Itoa(hs[0]), nil
	}
	return "", "", naturalErr("cron can't run one job at times with different minutes and hours", "make one job per time, or use the same minute for each")
}

// fieldList renders values as a cron list, folding runs of three or more
// into ranges: [1 2 3 4 5] is "1-5", [0 6] is "0,6". Empty gives def.
func fieldList(vals []int, def string) string {
	if len(vals) == 0 {
		return def
	}
	vals = uniq(vals)
	var parts []string
	for i := 0; i < len(vals); {
		j := i
		for j+1 < len(vals) && vals[j+1] == vals[j]+1 {
			j++
		}
		switch {
		case j-i >= 2:
			parts = append(parts, fmt.Sprintf("%d-%d", vals[i], vals[j]))
		case j-i == 1:
			parts = append(parts, strconv.Itoa(vals[i]), strconv.Itoa(vals[j]))
		default:
			parts = append(parts, strconv.Itoa(vals[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

func uniq(vals []int) []int {
	out := slices.Clone(vals)
	slices.Sort(out)
	return slices.Compact(out)
}

func uniqStrings(vals []string) []string {
	var out []string
	for _, v := range vals {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
