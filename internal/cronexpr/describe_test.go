package cronexpr

import "testing"

func TestDescribe(t *testing.T) {
	tests := []struct{ expr, want string }{
		// Minutes and hours
		{"* * * * *", "Every minute"},
		{"*/5 * * * *", "Every 5 minutes"},
		{"*/15 * * * *", "Every 15 minutes"},
		{"0,30 * * * *", "Every 30 minutes"},
		{"0,15,30,45 * * * *", "Every 15 minutes"},
		{"0 * * * *", "Every hour"},
		{"30 * * * *", "Every hour at :30"},
		{"5,35 * * * *", "At :05 and :35 past every hour"},
		{"1,7,13 * * * *", "At :01, :07 and :13 past every hour"},
		{"5/15 * * * *", "At :05, :20, :35 and :50 past every hour"},
		{"5/10 * * * *", "Every 10 minutes from :05 to :55"},
		{"0-29 * * * *", "Every minute from :00 to :29"},
		{"0 */2 * * *", "Every 2 hours"},
		{"0 */6 * * *", "Every 6 hours"},
		{"15 */3 * * *", "Every 3 hours at :15"},
		{"0 9-17 * * *", "Every hour from 09:00 to 17:00"},
		{"30 8-18/2 * * *", "Every 2 hours from 08:30 to 18:30"},
		{"0 1,3,5,7,9,11,13,15,17 * * *", "Every 2 hours from 01:00 to 17:00"},
		{"*/10 9 * * *", "Every 10 minutes from 09:00 to 09:50"},
		{"0-10 9 * * *", "Every minute from 09:00 to 09:10"},
		{"* 9 * * *", "Every minute from 09:00 to 09:59"},
		{"* 9-17 * * *", "Every minute, between 09:00 and 17:59"},
		{"*/15 */2 * * *", "Every 15 minutes, during every other hour"},
		{"0,30 9,17 * * *", "At 09:00, 09:30, 17:00 and 17:30, every day"},
		{"*/30 9,12,15 * * *", "Every 30 minutes, in the 09:00, 12:00 and 15:00 hours"},

		// Fixed times
		{"30 2 * * *", "At 02:30, every day"},
		{"0 9,17 * * *", "At 09:00 and 17:00, every day"},
		{"0 22 * * 1-5", "At 22:00, Monday through Friday"},

		// Days of the week
		{"*/15 9-17 * * 1-5", "Every 15 minutes, between 09:00 and 17:59, Monday through Friday"},
		{"0 9 * * mon-fri", "At 09:00, Monday through Friday"},
		{"0 4 * * 0", "At 04:00, on Sundays"},
		{"0 4 * * 7", "At 04:00, on Sundays"},
		{"0 0 * * 1,3,5", "At 00:00, on Mondays, Wednesdays and Fridays"},
		{"0 0 * * 5-7", "At 00:00, Friday through Sunday"},
		{"0 0 * * sat,sun", "At 00:00, on Saturdays and Sundays"},
		{"0 0 * * 1-3,5", "At 00:00, on Monday through Wednesday and Fridays"},
		{"0 * * * 1", "Every hour, on Mondays"},

		// Days of the month and months
		{"0 0 1 * *", "At 00:00, on the 1st of every month"},
		{"0 0 1,15 * *", "At 00:00, on the 1st and 15th of every month"},
		{"0 0 2,3,22,23 * *", "At 00:00, on the 2nd, 3rd, 22nd and 23rd of every month"},
		{"0 0 11,12,13,31 * *", "At 00:00, on the 11th, 12th, 13th and 31st of every month"},
		{"0 0 1-7 * *", "At 00:00, on the 1st through 7th of every month"},
		{"0 0 */2 * *", "At 00:00, on every other day of the month"},
		{"0 0 1 1 *", "At 00:00, on January 1"},
		{"0 0 1,15 6 *", "At 00:00, on June 1 and 15"},
		{"0 0 1 1,7 *", "At 00:00, on the 1st in January and July"},
		{"0 12 * 1-3 *", "At 12:00, every day, January through March"},
		{"0 0 * 6 *", "At 00:00, every day in June"},
		{"*/30 * * 12 *", "Every 30 minutes in December"},
		{"0 0 * */3 *", "At 00:00, every day in January, April, July and October"},
		{"0 0 * * 1 ", "At 00:00, on Mondays"},
		{"0 0 29 2 *", "At 00:00, on February 29"},

		// Day-of-month / day-of-week interplay (Vixie semantics)
		{"0 0 1 * 1", "At 00:00, on the 1st and on Mondays"},
		{"0 0 1,15 * 1-5", "At 00:00, on the 1st and 15th and on Monday through Friday"},
		{"0 0 1 6 1", "At 00:00, on the 1st and on Mondays in June"},
		{"0 0 */2 * 1", "At 00:00, on every other day of the month, if it's a Monday"},
		{"0 0 1-31 * 1", "At 00:00, every day"},
		{"0 0 * * 0-6", "At 00:00, every day"},

		// Descriptors
		{"@reboot", "At boot"},
		{"@hourly", "Every hour"},
		{"@daily", "At 00:00, every day"},
		{"@midnight", "At 00:00, every day"},
		{"@weekly", "At 00:00, on Sundays"},
		{"@monthly", "At 00:00, on the 1st of every month"},
		{"@yearly", "At 00:00, on January 1"},
		{"@annually", "At 00:00, on January 1"},

		// Time zones
		{"CRON_TZ=Europe/Berlin 30 2 * * *", "At 02:30, every day (Europe/Berlin)"},
		{"TZ=America/New_York @hourly", "Every hour (America/New_York)"},
	}
	for _, tt := range tests {
		got, err := Describe(tt.expr)
		if err != nil {
			t.Errorf("Describe(%q): %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Describe(%q)\n got: %s\nwant: %s", tt.expr, got, tt.want)
		}
	}
	if len(tests) < 40 {
		t.Fatalf("spec requires ≥ 40 describe cases, have %d", len(tests))
	}
}

func TestOrdinal(t *testing.T) {
	want := map[int]string{1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 11: "11th", 12: "12th", 13: "13th", 21: "21st", 22: "22nd", 23: "23rd", 31: "31st", 111: "111th"}
	for n, w := range want {
		if got := ordinal(n); got != w {
			t.Errorf("ordinal(%d) = %q, want %q", n, got, w)
		}
	}
}
