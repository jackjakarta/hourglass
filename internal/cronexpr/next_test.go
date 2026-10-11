package cronexpr

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

var fuzzFrom = time.Date(2026, 3, 8, 1, 0, 0, 0, time.UTC)

func mustLoad(t testing.TB, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

// fmtTimes renders instants with their zone abbreviation so DST cases
// read unambiguously ("01:30 EDT" vs "01:30 EST").
func fmtTimes(ts []time.Time) string {
	var s []string
	for _, t := range ts {
		s = append(s, t.Format("2006-01-02 15:04 MST"))
	}
	return strings.Join(s, ", ")
}

func TestNext(t *testing.T) {
	utc := time.UTC
	ny := mustLoad(t, "America/New_York")
	berlin := mustLoad(t, "Europe/Berlin")
	at := func(loc *time.Location, y int, mo time.Month, d, h, mi int) time.Time {
		return time.Date(y, mo, d, h, mi, 0, 0, loc)
	}
	// atUTC builds an instant from UTC fields, for instants inside a
	// fall-back overlap where wall-clock fields are ambiguous.
	atUTC := func(loc *time.Location, y int, mo time.Month, d, h, mi int) time.Time {
		return time.Date(y, mo, d, h, mi, 0, 0, utc).In(loc)
	}

	tests := []struct {
		name string
		expr string
		from time.Time
		n    int
		want string
	}{
		{"every minute", "* * * * *", at(utc, 2026, 6, 1, 14, 29), 3,
			"2026-06-01 14:30 UTC, 2026-06-01 14:31 UTC, 2026-06-01 14:32 UTC"},
		{"seconds in from are truncated", "30 14 * * *", time.Date(2026, 6, 1, 14, 29, 59, 0, utc), 1,
			"2026-06-01 14:30 UTC"},
		{"strictly after from", "30 14 * * *", time.Date(2026, 6, 1, 14, 30, 0, 500, utc), 1,
			"2026-06-02 14:30 UTC"},
		{"month rollover", "0 0 1 * *", at(utc, 2026, 12, 15, 0, 0), 2,
			"2027-01-01 00:00 UTC, 2027-02-01 00:00 UTC"},
		{"dom/dow OR", "0 0 1 * 1", at(utc, 2026, 5, 31, 0, 0), 7,
			"2026-06-01 00:00 UTC, 2026-06-08 00:00 UTC, 2026-06-15 00:00 UTC, 2026-06-22 00:00 UTC, 2026-06-29 00:00 UTC, 2026-07-01 00:00 UTC, 2026-07-06 00:00 UTC"},
		{"stepped star dom ANDs with dow", "0 0 */2 * 1", at(utc, 2026, 5, 31, 0, 0), 4,
			"2026-06-01 00:00 UTC, 2026-06-15 00:00 UTC, 2026-06-29 00:00 UTC, 2026-07-13 00:00 UTC"},
		{"dow 7 is Sunday", "0 12 * * 7", at(utc, 2026, 6, 1, 0, 0), 2,
			"2026-06-07 12:00 UTC, 2026-06-14 12:00 UTC"},
		{"Feb 29", "0 0 29 2 *", at(utc, 2026, 1, 1, 0, 0), 2,
			"2028-02-29 00:00 UTC, 2032-02-29 00:00 UTC"},
		{"Feb 29 across 2100", "0 0 29 2 *", at(utc, 2097, 1, 1, 0, 0), 1,
			"2104-02-29 00:00 UTC"},
		{"never fires", "30 2 31 2 *", at(utc, 2026, 1, 1, 0, 0), 3, ""},
		{"reboot", "@reboot", at(utc, 2026, 1, 1, 0, 0), 3, ""},
		{"CRON_TZ evaluates in its zone", "CRON_TZ=Asia/Tokyo 0 9 * * *", at(utc, 2026, 6, 1, 1, 0), 2,
			"2026-06-02 09:00 JST, 2026-06-03 09:00 JST"},

		// Spring forward, America/New_York: 2026-03-08 02:00 EST → 03:00 EDT.
		{"NY gap: fixed job runs right after the jump", "30 2 * * *", at(ny, 2026, 3, 7, 12, 0), 3,
			"2026-03-08 03:00 EDT, 2026-03-09 02:30 EDT, 2026-03-10 02:30 EDT"},
		{"NY gap: several fixed times collapse to one run", "0,30 2 * * *", at(ny, 2026, 3, 8, 0, 0), 3,
			"2026-03-08 03:00 EDT, 2026-03-09 02:00 EDT, 2026-03-09 02:30 EDT"},
		{"NY gap: wildcard job skips missing minutes", "*/30 * * * *", at(ny, 2026, 3, 8, 1, 0), 3,
			"2026-03-08 01:30 EST, 2026-03-08 03:00 EDT, 2026-03-08 03:30 EDT"},
		{"NY gap: wildcard hour skips missing hour", "30 * * * *", at(ny, 2026, 3, 8, 0, 45), 3,
			"2026-03-08 01:30 EST, 2026-03-08 03:30 EDT, 2026-03-08 04:30 EDT"},

		// Fall back, America/New_York: 2026-11-01 02:00 EDT → 01:00 EST.
		{"NY overlap: fixed job runs once", "30 1 * * *", at(ny, 2026, 10, 31, 12, 0), 2,
			"2026-11-01 01:30 EDT, 2026-11-02 01:30 EST"},
		{"NY overlap: wildcard job runs in both passes", "*/30 * * * *", at(ny, 2026, 11, 1, 0, 45), 5,
			"2026-11-01 01:00 EDT, 2026-11-01 01:30 EDT, 2026-11-01 01:00 EST, 2026-11-01 01:30 EST, 2026-11-01 02:00 EST"},
		{"NY overlap: wildcard hour runs in both passes", "30 * * * *", at(ny, 2026, 11, 1, 0, 45), 3,
			"2026-11-01 01:30 EDT, 2026-11-01 01:30 EST, 2026-11-01 02:30 EST"},
		{"NY overlap: from late in first pass", "*/30 * * * *", atUTC(ny, 2026, 11, 1, 5, 40), 3,
			"2026-11-01 01:00 EST, 2026-11-01 01:30 EST, 2026-11-01 02:00 EST"},
		{"NY overlap: from inside second pass", "*/30 * * * *", atUTC(ny, 2026, 11, 1, 6, 10), 2,
			"2026-11-01 01:30 EST, 2026-11-01 02:00 EST"},
		{"NY overlap: fixed job already ran in first pass", "30 1 * * *", atUTC(ny, 2026, 11, 1, 6, 10), 1,
			"2026-11-02 01:30 EST"},

		// Europe/Berlin: 2026-03-29 02:00 CET → 03:00 CEST; 2026-10-25 03:00 CEST → 02:00 CET.
		{"Berlin gap", "30 2 * * *", at(berlin, 2026, 3, 28, 12, 0), 2,
			"2026-03-29 03:00 CEST, 2026-03-30 02:30 CEST"},
		{"Berlin overlap: fixed", "30 2 * * *", at(berlin, 2026, 10, 24, 12, 0), 2,
			"2026-10-25 02:30 CEST, 2026-10-26 02:30 CET"},
		{"Berlin overlap: hourly", "@hourly", atUTC(berlin, 2026, 10, 24, 23, 30), 4,
			"2026-10-25 02:00 CEST, 2026-10-25 02:00 CET, 2026-10-25 03:00 CET, 2026-10-25 04:00 CET"},
		{"CRON_TZ gap from a UTC caller", "CRON_TZ=Europe/Berlin 30 2 * * *", at(utc, 2026, 3, 28, 12, 0), 1,
			"2026-03-29 03:00 CEST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Next(tt.expr, tt.from, tt.n)
			if err != nil {
				t.Fatal(err)
			}
			if s := fmtTimes(got); s != tt.want {
				t.Errorf("Next(%q, %s, %d)\n got: %s\nwant: %s", tt.expr, tt.from.Format(time.RFC3339), tt.n, s, tt.want)
			}
		})
	}
}

// bruteNext is a deliberately naive reference: step day by day, then minute
// by minute, in UTC.
func bruteNext(e *Expr, from time.Time, n int) []time.Time {
	var out []time.Time
	t := from.UTC().Truncate(time.Minute).Add(time.Minute)
	end := bruteEnd(from)
	for len(out) < n && t.Before(end) {
		if !e.Month.Has(int(t.Month())) || !e.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if e.Hour.Has(t.Hour()) && e.Minute.Has(t.Minute()) {
			out = append(out, t)
		}
		t = t.Add(time.Minute)
	}
	return out
}

func bruteEnd(from time.Time) time.Time { return from.UTC().AddDate(8, 0, 0) }

func randomExpr(r *rand.Rand) string {
	field := func(min, max int, names []string) string {
		pick := func() string {
			v := min + r.Intn(max-min+1)
			if names != nil && r.Intn(3) == 0 {
				return names[v-min]
			}
			return itoa(v)
		}
		switch r.Intn(6) {
		case 0, 1:
			return "*"
		case 2:
			return "*/" + itoa(1+r.Intn((max-min)/2+1))
		case 3:
			a, b := min+r.Intn(max-min+1), min+r.Intn(max-min+1)
			if a > b {
				a, b = b, a
			}
			s := itoa(a) + "-" + itoa(b)
			if r.Intn(2) == 0 {
				s += "/" + itoa(1+r.Intn(4))
			}
			return s
		case 4:
			return pick() + "," + pick() + "," + pick()
		}
		return pick()
	}
	months := []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	return strings.Join([]string{
		field(0, 59, nil), field(0, 23, nil), field(1, 28, nil), field(1, 12, months), field(0, 6, days),
	}, " ")
}

func TestNextMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	from := time.Date(2026, 2, 27, 23, 58, 0, 0, time.UTC)
	for i := 0; i < 300; i++ {
		expr := randomExpr(r)
		e := MustParse(expr)
		got, want := e.Next(from, 5), bruteNext(e, from, 5)
		for len(got) > 0 && !got[len(got)-1].Before(bruteEnd(from)) {
			got = got[:len(got)-1]
		}
		if fmtTimes(got) != fmtTimes(want) {
			t.Errorf("Next(%q)\n got: %s\nwant: %s", expr, fmtTimes(got), fmtTimes(want))
		}
	}
}

// TestNextMatchesRobfig cross-checks against robfig/cron on expressions
// where the two agree: UTC, no dow 7, and no stepped "*" in a day field
// alongside a restricted other day field (Vixie treats "*/2" as a star for
// the dom/dow OR; robfig doesn't).
func TestNextMatchesRobfig(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	corpus := []string{
		"* * * * *", "*/7 * * * *", "0 */5 * * *", "30 2 * * *", "0 0 1 * 1", "15 10 1,15 * mon-fri",
		"0 9-17/2 * * 1-5", "0 0 29 2 *", "5 4 * jan,jul sun", "@daily", "@weekly", "@monthly", "@yearly", "@hourly",
	}
	for i := 0; i < 200; i++ {
		expr := randomExpr(r)
		f := strings.Fields(expr)
		if strings.HasPrefix(f[2], "*/") && f[4] != "*" || strings.HasPrefix(f[4], "*/") && f[2] != "*" {
			continue
		}
		corpus = append(corpus, expr)
	}
	from := time.Date(2026, 10, 11, 16, 55, 0, 0, time.UTC)
	for _, expr := range corpus {
		sched, err := cron.ParseStandard(expr)
		if err != nil {
			t.Fatalf("robfig rejected %q: %v", expr, err)
		}
		var want []time.Time
		for t := from; len(want) < 5; {
			if t = sched.Next(t); t.IsZero() {
				break
			}
			want = append(want, t)
		}
		got, err := Next(expr, from, 5)
		if err != nil {
			t.Fatal(err)
		}
		if fmtTimes(got) != fmtTimes(want) {
			t.Errorf("Next(%q)\n   got: %s\nrobfig: %s", expr, fmtTimes(got), fmtTimes(want))
		}
	}
}

func BenchmarkNextEveryMinute(b *testing.B) {
	e := MustParse("* * * * *")
	from := time.Now()
	for i := 0; i < b.N; i++ {
		e.Next(from, 10)
	}
}

func BenchmarkNextDST(b *testing.B) {
	e := MustParse("*/5 * * * *")
	from := time.Date(2026, 11, 1, 0, 30, 0, 0, mustLoad(b, "America/New_York"))
	for i := 0; i < b.N; i++ {
		e.Next(from, 10)
	}
}
