package humanize

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                              "0s",
		42 * time.Second:               "42s",
		7*time.Minute + 53*time.Second: "7m 53s",
		5 * time.Minute:                "5m",
		11*time.Hour + 58*time.Minute + 59*time.Second: "11h 58m",
		37 * time.Hour:    "1d 13h",
		48 * time.Hour:    "2d",
		-90 * time.Second: "1m 30s",
	} {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestNext(t *testing.T) {
	time.Local = time.UTC
	now := time.Date(2026, 10, 11, 14, 32, 7, 0, time.UTC)
	tests := []struct {
		t    time.Time
		want string
	}{
		{now.Add(11*time.Hour + 58*time.Minute), "in 11h 58m"},
		{now, "now"},
		{now.Add(7 * 24 * time.Hour), "in 7d"},
		{time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC), "Mon 2 Nov 09:00"},
		{time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC), "Tue 29 Feb 2028 00:00"},
	}
	for _, tt := range tests {
		if got := Next(tt.t, now); got != tt.want {
			t.Errorf("Next(%v) = %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KiB", 1 << 20: "1.0 MiB", 5 << 30: "5.0 GiB"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}
