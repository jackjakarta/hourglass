package cronexpr

import "testing"

func TestShort(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"@reboot", "At boot"},
		{"30 2 * * *", "At 02:30"},
		{"@daily", "At 00:00"},
		{"0 9,17 * * *", "At 09:00 and 17:00"},
		{"*/15 * * * *", "Every 15 minutes"},
		{"0 * * * *", "Every hour"},
		{"@hourly", "Every hour"},
		{"* * * * *", "Every minute"},
		{"0 4 * * 0", "Sundays at 04:00"},
		{"0 4 * * 7", "Sundays at 04:00"},
		{"0 9 * * 1", "Mondays at 09:00"},
		{"@weekly", "Sundays at 00:00"},
		{"0 9 * * 1-5", "Weekdays at 09:00"},
		{"0 10 * * sat,sun", "Weekends at 10:00"},
		{"0 0 * * 1,3,5", "Mon, Wed, Fri at 00:00"},
		{"0 0 * * 1-3,5", "Mon–Wed, Fri at 00:00"},
		{"0 0 * * 5-7", "Fri–Sun at 00:00"},
		{"*/15 9-17 * * 1-5", "Every 15 minutes, between 09:00 and 17:59 on weekdays"},
		{"0 * * * 1", "Every hour on Mondays"},
		{"0 3 1 * *", "Monthly on the 1st at 03:00"},
		{"@monthly", "Monthly on the 1st at 00:00"},
		{"0 0 1,15 * *", "Monthly on the 1st and 15th at 00:00"},
		{"0 0 1 1 *", "Yearly on Jan 1 at 00:00"},
		{"@yearly", "Yearly on Jan 1 at 00:00"},
		{"0 0 1 * 1", "At 00:00, on the 1st and on Mondays"},
		{"0 12 * 1-3 *", "At 12:00, every day, January through March"},
		{"*/10 * 1 * *", "Every 10 minutes, on the 1st of every month"},
		{"CRON_TZ=Europe/Berlin 30 2 * * *", "At 02:30"},
		{"CRON_TZ=Europe/Berlin 0 0 1 * 1", "At 00:00, on the 1st and on Mondays"},
	}
	for _, tt := range tests {
		if got := MustParse(tt.expr).Short(); got != tt.want {
			t.Errorf("Short(%q) = %q, want %q", tt.expr, got, tt.want)
		}
	}
}
