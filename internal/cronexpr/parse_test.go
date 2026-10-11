package cronexpr

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseValues(t *testing.T) {
	tests := []struct {
		expr                          string
		minute, hour, dom, month, dow []int
	}{
		{"5 4 3 2 1", []int{5}, []int{4}, []int{3}, []int{2}, []int{1}},
		{"*/20 9-11 1,15 jan-mar mon-fri", []int{0, 20, 40}, []int{9, 10, 11}, []int{1, 15}, []int{1, 2, 3}, []int{1, 2, 3, 4, 5}},
		{"5/20 * * * *", []int{5, 25, 45}, nil, nil, nil, nil},
		{"0-30/10 * * * *", []int{0, 10, 20, 30}, nil, nil, nil, nil},
		{"0 0 * * 7", []int{0}, []int{0}, nil, nil, []int{0}},
		{"0 0 * * 5-7", []int{0}, []int{0}, nil, nil, []int{0, 5, 6}},
		{"0 0 * * SUN,Sat", []int{0}, []int{0}, nil, nil, []int{0, 6}},
		{"0 0 * DEC *", []int{0}, []int{0}, nil, []int{12}, nil},
		{"0 0 * * */2", []int{0}, []int{0}, nil, nil, []int{0, 2, 4, 6}},
		{"0 0 * * 1/2", []int{0}, []int{0}, nil, nil, []int{0, 1, 3, 5}}, // 1,3,5,7 with 7 = Sunday
		{"  0\t0  *  *  *  ", []int{0}, []int{0}, nil, nil, nil},
	}
	for _, tt := range tests {
		e, err := Parse(tt.expr)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.expr, err)
		}
		check := func(name string, f Field, want []int) {
			if want == nil {
				return
			}
			if got := f.Values(); !reflect.DeepEqual(got, want) {
				t.Errorf("Parse(%q).%s = %v, want %v", tt.expr, name, got, want)
			}
		}
		check("Minute", e.Minute, tt.minute)
		check("Hour", e.Hour, tt.hour)
		check("Dom", e.Dom, tt.dom)
		check("Month", e.Month, tt.month)
		check("Dow", e.Dow, tt.dow)
	}
}

func TestParseStarFlags(t *testing.T) {
	tests := []struct {
		expr             string
		domStar, dowStar bool
	}{
		{"0 0 * * *", true, true},
		{"0 0 1 * *", false, true},
		{"0 0 * * 1", true, false},
		{"0 0 */2 * 1", true, false}, // Vixie: a leading "*" is a star even with a step
		{"0 0 1-31 * 0-6", false, false},
	}
	for _, tt := range tests {
		e := MustParse(tt.expr)
		if e.Dom.Star != tt.domStar || e.Dow.Star != tt.dowStar {
			t.Errorf("%q: Dom.Star=%v Dow.Star=%v, want %v %v", tt.expr, e.Dom.Star, e.Dow.Star, tt.domStar, tt.dowStar)
		}
	}
}

func TestParseDescriptorsAndTZ(t *testing.T) {
	for d, exp := range Descriptors {
		e, err := Parse(strings.ToUpper(d))
		if err != nil {
			t.Fatalf("Parse(%q): %v", d, err)
		}
		if d == "@reboot" {
			if !e.Reboot {
				t.Errorf("@reboot: Reboot = false")
			}
			continue
		}
		if e.Descriptor != d {
			t.Errorf("%s: Descriptor = %q", d, e.Descriptor)
		}
		if want := MustParse(exp); e.Minute.Bits != want.Minute.Bits || e.Dow.Bits != want.Dow.Bits || e.Dom.Bits != want.Dom.Bits {
			t.Errorf("%s doesn't match its expansion %q", d, exp)
		}
	}

	e := MustParse("CRON_TZ=Europe/Berlin 30 2 * * *")
	if e.TZ == nil || e.TZName != "Europe/Berlin" || !e.Hour.Has(2) {
		t.Errorf("CRON_TZ prefix: TZ=%v name=%q", e.TZ, e.TZName)
	}
	e = MustParse("TZ=UTC\t@daily")
	if e.TZName != "UTC" || e.Descriptor != "@daily" {
		t.Errorf("TZ prefix with descriptor: %+v", e)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"", "empty schedule"},
		{"* * * *", "expected 5 fields"},
		{"0 * * * * *", "no seconds field"},
		{"60 * * * *", `minute: "60" is out of range (0-59)`},
		{"0 24 * * *", `hour: "24" is out of range (0-23)`},
		{"0 0 0 * *", `day-of-month: "0" is out of range (1-31)`},
		{"0 0 * 13 *", "month:"},
		{"0 0 * * 8", "day-of-week:"},
		{"0 0 ? * *", "uses ?"},
		{"*/0 * * * *", "invalid step"},
		{"5-1 * * * *", "reversed range"},
		{"1,,2 * * * *", "empty list element"},
		{"a * * * *", "is not a number"},
		{"0 0 * foo *", "3-letter name"},
		{"@every 5m", "@every isn't supported"},
		{"@fortnightly", "unknown descriptor"},
		{"@daily extra", "unexpected text"},
		{"CRON_TZ=Mars/Olympus 0 0 * * *", "not a known time zone"},
		{"-5 * * * *", "missing a value"},
	}
	for _, tt := range tests {
		_, err := Parse(tt.expr)
		if err == nil {
			t.Errorf("Parse(%q) succeeded, want error containing %q", tt.expr, tt.want)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Parse(%q) error = %q, want it to contain %q", tt.expr, err, tt.want)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"* * * * *", "*/15 9-17 * * 1-5", "0 0 1 * 1", "@reboot", "CRON_TZ=UTC 0 0 * * 7", "5/7 1-23/3 */2 jan-jun sun-sat"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		e, err := Parse(s)
		if err != nil {
			return
		}
		_ = e.Describe()
		_ = Lint(s)
		e.Next(fuzzFrom, 3)
	})
}
