package components

import (
	"fmt"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                   "plain",
		"\x1b[31mred\x1b[0m":      "red",
		"10%\r50%\r100%":          "100%",
		"line\r":                  "line",
		"a\tb":                    "a       b",
		"bell\a and \x00nul":      "bell and nul",
		"bad \xff utf8":           "bad � utf8",
		"\x1b]0;title\x07visible": "visible",
		"wide 日本 ok":              "wide 日本 ok",
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinesSplitsChunks(t *testing.T) {
	b := NewLines(3)
	whole := "one\ntwo 日本\nthree\nfour"
	for i := 0; i < len(whole); i++ { // a byte at a time, splitting runes
		b.Write([]byte{whole[i]})
		for _, l := range b.Lines() {
			if strings.ContainsRune(l, '�') {
				t.Fatalf("after %d bytes, a split rune showed as %q", i+1, l)
			}
		}
	}
	// max counts complete lines; the one being written comes on top.
	if got := strings.Join(b.Lines(), "|"); got != "one|two 日本|three|four" || b.Len() != 4 {
		t.Errorf("lines = %q", got)
	}
	b.Write([]byte("\n"))
	if got := strings.Join(b.Lines(), "|"); got != "two 日本|three|four" || b.Dropped() != 1 {
		t.Errorf("after newline: %q, dropped %d", got, b.Dropped())
	}
}

func TestLinesManyLines(t *testing.T) {
	b := NewLines(1000)
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&sb, "y %d\n", i)
	}
	b.Write([]byte(sb.String()))
	if l := b.Lines(); len(l) != 1000 || l[999] != "y 4999" || b.Dropped() != 4000 {
		t.Errorf("len %d, last %q, dropped %d", len(l), l[len(l)-1], b.Dropped())
	}
}
