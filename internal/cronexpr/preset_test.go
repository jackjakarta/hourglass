package cronexpr

import "testing"

func TestPresetExpr(t *testing.T) {
	tests := []struct {
		p    Preset
		want string
	}{
		{Preset{Kind: EveryNMin, Every: 15}, "*/15 * * * *"},
		{Preset{Kind: EveryNMin, Every: 1}, "* * * * *"},
		{Preset{Kind: EveryNMin, Every: 7}, "*/7 * * * *"},
		{Preset{Kind: Hourly, Minute: 5}, "5 * * * *"},
		{Preset{Kind: Daily, Minute: 30, Hour: 2}, "30 2 * * *"},
		{Preset{Kind: Weekdays, Minute: 0, Hour: 9}, "0 9 * * 1-5"},
		{Preset{Kind: Weekly, Minute: 0, Hour: 4, Days: 1}, "0 4 * * 0"},
		{Preset{Kind: Weekly, Minute: 0, Hour: 4, Days: 0b0101110}, "0 4 * * 1-3,5"},
		{Preset{Kind: Weekly, Minute: 0, Hour: 4, Days: 0b1000001}, "0 4 * * 0,6"},
		{Preset{Kind: Weekly, Minute: 0, Hour: 4, Days: 0b1111111}, "0 4 * * 0-6"},
		{Preset{Kind: Monthly, Minute: 0, Hour: 3, Dom: 1}, "0 3 1 * *"},
		{Preset{Kind: Monthly, Minute: 15, Hour: 23, Dom: 31}, "15 23 31 * *"},
		{Preset{Kind: Custom}, ""},
	}
	for _, tt := range tests {
		if got := tt.p.Expr(); got != tt.want {
			t.Errorf("%+v.Expr() = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestDetectPreset(t *testing.T) {
	tests := []struct {
		expr string
		want Preset // Kind Custom means no match
	}{
		{"*/15 * * * *", Preset{Kind: EveryNMin, Every: 15}},
		{"0-59/15 * * * *", Preset{Kind: EveryNMin, Every: 15}},
		{"0,15,30,45 * * * *", Preset{Kind: EveryNMin, Every: 15}},
		{"* * * * *", Preset{Kind: EveryNMin, Every: 1}},
		{"*/7 * * * *", Preset{Kind: EveryNMin, Every: 7}},
		{"5 * * * *", Preset{Kind: Hourly, Minute: 5}},
		{"@hourly", Preset{Kind: Hourly, Minute: 0}},
		{"30 2 * * *", Preset{Kind: Daily, Minute: 30, Hour: 2}},
		{"@daily", Preset{Kind: Daily}},
		{"@midnight", Preset{Kind: Daily}},
		{"0 9 * * 1-5", Preset{Kind: Weekdays, Hour: 9}},
		{"0 9 * * mon-fri", Preset{Kind: Weekdays, Hour: 9}},
		{"0 9 * * 1,2,3,4,5", Preset{Kind: Weekdays, Hour: 9}},
		{"0 4 * * 0", Preset{Kind: Weekly, Hour: 4, Days: 1}},
		{"0 4 * * 7", Preset{Kind: Weekly, Hour: 4, Days: 1}},
		{"0 4 * * sun", Preset{Kind: Weekly, Hour: 4, Days: 1}},
		{"@weekly", Preset{Kind: Weekly, Days: 1}},
		{"0 4 * * 1,3,5", Preset{Kind: Weekly, Hour: 4, Days: 0b0101010}},
		{"0 3 1 * *", Preset{Kind: Monthly, Hour: 3, Dom: 1}},
		{"@monthly", Preset{Kind: Monthly, Dom: 1}},

		// Near misses.
		{"0 9 1 * 1", Preset{Kind: Custom}},    // dom/dow OR
		{"0 9 1-31 * 1", Preset{Kind: Custom}}, // OR with a full dom: every day, but not written as a preset
		{"*/15 9-17 * * *", Preset{Kind: Custom}},
		{"0 9,17 * * *", Preset{Kind: Custom}},
		{"0 0 1 1 *", Preset{Kind: Custom}},
		{"@yearly", Preset{Kind: Custom}},
		{"@reboot", Preset{Kind: Custom}},
		{"CRON_TZ=UTC 0 9 * * *", Preset{Kind: Custom}},
		{"0 0 * 6 *", Preset{Kind: Custom}},
		{"5,10 * * * *", Preset{Kind: Custom}},
		{"0,40 * * * *", Preset{Kind: EveryNMin, Every: 40}}, // the same minutes as */40
		{"0,20,45 * * * *", Preset{Kind: Custom}},
		{"0 3 1,15 * *", Preset{Kind: Custom}},
	}
	for _, tt := range tests {
		got, ok := DetectPreset(MustParse(tt.expr))
		if got != tt.want || ok != (tt.want.Kind != Custom) {
			t.Errorf("DetectPreset(%q) = %+v, %v; want %+v", tt.expr, got, ok, tt.want)
		}
	}
}

func TestPresetRoundTrip(t *testing.T) {
	var presets []Preset
	for n := 1; n < 60; n++ {
		presets = append(presets, Preset{Kind: EveryNMin, Every: n})
	}
	for _, m := range []int{0, 7, 59} {
		presets = append(presets, Preset{Kind: Hourly, Minute: m})
		for _, h := range []int{0, 13, 23} {
			presets = append(presets,
				Preset{Kind: Daily, Minute: m, Hour: h},
				Preset{Kind: Weekdays, Minute: m, Hour: h},
				Preset{Kind: Monthly, Minute: m, Hour: h, Dom: 1},
				Preset{Kind: Monthly, Minute: m, Hour: h, Dom: 31},
			)
		}
	}
	for d := uint8(1); d < 128; d++ {
		if d != WeekdayBits && d != 0x7f { // those are Weekdays and Daily
			presets = append(presets, Preset{Kind: Weekly, Minute: 30, Hour: 6, Days: d})
		}
	}
	for _, p := range presets {
		e, err := Parse(p.Expr())
		if err != nil {
			t.Fatalf("%+v: %v", p, err)
		}
		got, ok := DetectPreset(e)
		if !ok || got != p {
			t.Errorf("DetectPreset(%q) = %+v, want %+v", p.Expr(), got, p)
		}
	}
}

func TestFieldAt(t *testing.T) {
	tests := []struct {
		raw    string
		cursor int
		want   int
	}{
		{"", 0, 0},
		{"30 2 * * *", 0, 0},
		{"30 2 * * *", 2, 0},
		{"30 2 * * *", 3, 1},
		{"30 2 * * *", 4, 1},
		{"30 2 * * *", 10, 4},
		{"30 2 ", 5, 2},
		{"30  2", 3, 1}, // in the gap before "2"
		{"30 2 * * * ", 11, FieldExtra},
		{"@daily", 3, FieldDescriptor},
		{"CRON_TZ=UTC 30 2 * * *", 4, FieldTZ},
		{"CRON_TZ=UTC 30 2 * * *", 13, 0},
		{"CRON_TZ=UTC 30 2 * * *", 22, 4},
		{"TZ=UTC @daily", 9, FieldDescriptor},
	}
	for _, tt := range tests {
		if got := FieldAt(tt.raw, tt.cursor); got != tt.want {
			t.Errorf("FieldAt(%q, %d) = %d, want %d", tt.raw, tt.cursor, got, tt.want)
		}
	}
}
