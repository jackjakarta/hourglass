package cronexpr

import (
	"errors"
	"strings"
	"testing"
)

func TestParseNatural(t *testing.T) {
	for _, tt := range []struct {
		phrase, want string
		note         string // a substring of the note; "" means no note
	}{
		{"every minute", "* * * * *", ""},
		{"every 5 minutes", "*/5 * * * *", ""},
		{"every 15 mins", "*/15 * * * *", ""},
		{"every half hour", "*/30 * * * *", ""},
		{"every half an hour", "*/30 * * * *", ""},
		{"every quarter hour", "*/15 * * * *", ""},
		{"twice an hour", "*/30 * * * *", ""},
		{"every 7 minutes", "*/7 * * * *", ":56 is followed by :00"},
		{"every hour", "0 * * * *", ""},
		{"hourly", "0 * * * *", ""},
		{"every other hour", "0 */2 * * *", ""},
		{"every 6 hours", "0 */6 * * *", ""},
		{"every 120 minutes", "0 */2 * * *", ""},
		{"every hour at :30", "30 * * * *", ""},
		{"15 minutes past the hour", "15 * * * *", ""},
		{"at 15 minutes past every hour", "15 * * * *", ""},
		{"every hour on the half hour", "30 * * * *", ""},
		{"every day at 9am", "0 9 * * *", ""},
		{"daily at 14:30", "30 14 * * *", ""},
		{"at noon", "0 12 * * *", ""},
		{"at midnight", "0 0 * * *", ""},
		{"every night at 11pm", "0 23 * * *", ""},
		{"at 9 o'clock", "0 9 * * *", ""},
		{"half past 9", "30 9 * * *", ""},
		{"at 9 a.m.", "0 9 * * *", ""},
		{"at 12am", "0 0 * * *", ""},
		{"at 12:30pm", "30 12 * * *", ""},
		{"at 9am and 5pm", "0 9,17 * * *", ""},
		{"at 9:15 and 9:45", "15,45 9 * * *", ""},
		{"twice a day", "0 0,12 * * *", ""},
		{"4 times a day", "0 0,6,12,18 * * *", ""},
		{"every weekday at 9am", "0 9 * * 1-5", ""},
		{"Weekdays at 9:30.", "30 9 * * 1-5", ""},
		{"monday to friday at 8", "0 8 * * 1-5", ""},
		{"mon-fri at 6pm", "0 18 * * 1-5", ""},
		{"from monday through friday at noon", "0 12 * * 1-5", ""},
		{"every weekend at 10am", "0 10 * * 0,6", ""},
		{"every sunday at 4am", "0 4 * * 0", ""},
		{"mondays and fridays at 5pm", "0 17 * * 1,5", ""},
		{"every mon, wed & fri at 6:30 pm", "30 18 * * 1,3,5", ""},
		{"every monday", "0 0 * * 1", "midnight"},
		{"every 15 minutes between 9am and 5pm on weekdays", "*/15 9-17 * * 1-5", ""},
		{"every 5 minutes during business hours", "*/5 9-17 * * *", ""},
		{"every hour from 9 to 17", "0 9-17 * * *", ""},
		{"every 2 hours between 8am and 6pm", "0 8-18/2 * * *", ""},
		{"every hour between 10pm and 2am", "0 0-2,22,23 * * *", ""},
		{"weekly", "0 0 * * 0", "midnight"},
		{"monthly", "0 0 1 * *", "midnight"},
		{"yearly", "0 0 1 1 *", "midnight"},
		{"on the 1st and 15th at noon", "0 12 1,15 * *", ""},
		{"every month on the 15th at 10:30", "30 10 15 * *", ""},
		{"first of every month at 9am", "0 9 1 * *", ""},
		{"on the first day of the month", "0 0 1 * *", "midnight"},
		{"every year on jan 1", "0 0 1 1 *", "midnight"},
		{"every december 25th at 8am", "0 8 25 12 *", ""},
		{"on 1 january at 9am", "0 9 1 1 *", ""},
		{"every quarter", "0 0 1 */3 *", "midnight"},
		{"every 3 months on the 1st at 6am", "0 6 1 */3 *", ""},
		{"in january at 9am", "0 9 * 1 *", ""},
		{"on the 1st and on mondays at 9am", "0 9 1 * 1", "either"},
		{"at reboot", "@reboot", ""},
		{"on startup", "@reboot", ""},
	} {
		t.Run(tt.phrase, func(t *testing.T) {
			got, err := ParseNatural(tt.phrase)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got.Expr != tt.want {
				t.Errorf("expr = %q, want %q", got.Expr, tt.want)
			}
			if (tt.note == "") != (got.Note == "") || !strings.Contains(got.Note, tt.note) {
				t.Errorf("note = %q, want one with %q", got.Note, tt.note)
			}
			if _, err := Describe(got.Expr); err != nil {
				t.Errorf("Describe: %v", err)
			}
		})
	}
}

func TestParseNaturalUnsupported(t *testing.T) {
	for _, tt := range []struct {
		phrase string
		msg    string // a substring of the message
		hint   bool   // there should be a hint
	}{
		{"", "say when", false},
		{"every", "say when", false},
		{"first monday of the month", "first monday", true},
		{"on the 2nd tuesday at 9am", "2nd tuesday", true},
		{"last friday of the month", "last friday", true},
		{"last day of the month", "last day", true},
		{"every 90 minutes", "every 90 minutes", true},
		{"every 30 seconds", "once a minute", false},
		{"every 2 days", "every 2 days", true},
		{"every other day", "every other day", true},
		{"every 2 weeks", "every 2 weeks", true},
		{"biweekly", "every other week", true},
		{"every 5 months", "every 5 months", true},
		{"every 7 hours and 30 minutes", "doesn't understand", true},
		{"at 9:15 and 17:30", "different minutes and hours", true},
		{"at 9am between 9 and 5", "not both", false},
		{"at boot every day at 9", "at boot", true},
		{"on the 32nd", "no day 32", false},
		{"whenever you like", "doesn't understand \"whenever\"", true},
	} {
		t.Run(tt.phrase, func(t *testing.T) {
			got, err := ParseNatural(tt.phrase)
			var ne *NaturalError
			if !errors.As(err, &ne) {
				t.Fatalf("ParseNatural = %+v, %v; want a NaturalError", got, err)
			}
			if !strings.Contains(ne.Msg, tt.msg) {
				t.Errorf("msg = %q, want %q in it", ne.Msg, tt.msg)
			}
			if (ne.Hint != "") != tt.hint {
				t.Errorf("hint = %q", ne.Hint)
			}
		})
	}
}
