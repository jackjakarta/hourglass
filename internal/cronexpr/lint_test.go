package cronexpr

import (
	"strings"
	"testing"
)

func TestLint(t *testing.T) {
	tests := []struct {
		expr string
		want []string // "severity code: message substring"
	}{
		{"30 2 * * *", nil},
		{"@reboot", nil},
		{"61 * * * *", []string{"error unparsable: out of range"}},
		{"* * * * *", []string{"info every-minute: every minute"}},
		{"* 9 * * *", nil},
		{"30 2 31 2 *", []string{"warning dom-never: February never has a 31st"}},
		{"30 2 30 2 *", []string{"warning dom-never: February never has a 30th"}},
		{"0 0 30,31 2 *", []string{"warning dom-never: February never has a 30th"}},
		{"0 0 31 4,6 *", []string{"warning dom-never: April or June never has a 31st"}},
		{"0 0 29 2 *", []string{"warning dom-rare: leap years"}},
		{"0 0 31 * *", []string{"warning dom-rare: (February, April, June, September, November)"}},
		{"0 0 30 * *", []string{"warning dom-rare: without a 30th (February)"}},
		{"0 0 29 * *", nil},
		{"0 0 1,31 * *", nil},
		{"0 0 1 * 1", []string{"info dom-dow-or: either one (on the 1st and on Mondays)"}},
		{"0 0 31 * 1", []string{"info dom-dow-or: on the 31st and on Mondays"}}, // OR adds Mondays, so no dom-rare
		{"0 0 1-31 * 1", nil}, // OR with a full field is just every day
		{"0 0 31 2 */2", []string{"warning dom-never: never has a 31st"}},
	}
	for _, tt := range tests {
		got := Lint(tt.expr)
		if len(got) != len(tt.want) {
			t.Errorf("Lint(%q) = %v, want %d issue(s) %v", tt.expr, got, len(tt.want), tt.want)
			continue
		}
		for i, w := range tt.want {
			head, msg, _ := strings.Cut(w, ": ")
			if s := got[i].Severity.String() + " " + got[i].Code; s != head || !strings.Contains(got[i].Message, msg) {
				t.Errorf("Lint(%q)[%d] = %s %q, want %s containing %q", tt.expr, i, s, got[i].Message, head, msg)
			}
		}
	}
}
