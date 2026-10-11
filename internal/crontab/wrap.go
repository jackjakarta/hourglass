package crontab

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Wrapped is a command that runs through "gocron exec".
type Wrapped struct {
	Bin       string // path to the gocron binary as written
	ID        string
	Timeout   time.Duration // 0 = none
	NoOverlap bool
	Command   string // the user's plain command
}

// Encode renders the wrapped command (before %-escaping):
//
//	<bin> exec [--timeout=D] [--no-overlap] <id> -- '<command>'
func (w Wrapped) Encode() string {
	var b strings.Builder
	b.WriteString(ShellQuoteIfNeeded(w.Bin))
	b.WriteString(" exec")
	if w.Timeout > 0 {
		b.WriteString(" --timeout=" + FormatDuration(w.Timeout))
	}
	if w.NoOverlap {
		b.WriteString(" --no-overlap")
	}
	b.WriteString(" " + ShellQuoteIfNeeded(w.ID) + " -- " + ShellQuote(w.Command))
	return b.String()
}

// DecodeWrapped recognizes a command whose first word's basename is
// "gocron" and whose second word is "exec". cmd must already be
// %-unescaped. Flags gocron didn't write make it unrecognized, so the
// command is shown and preserved as-is.
func DecodeWrapped(cmd string) (Wrapped, bool) {
	words, err := shellWords(cmd)
	if err != nil || len(words) < 4 || path.Base(words[0].text) != "gocron" || words[1].text != "exec" {
		return Wrapped{}, false
	}
	w := Wrapped{Bin: words[0].text}
	i := 2
	for ; i < len(words) && strings.HasPrefix(words[i].text, "--") && words[i].text != "--"; i++ {
		switch flag := words[i].text; {
		case flag == "--no-overlap":
			w.NoOverlap = true
		case strings.HasPrefix(flag, "--timeout="):
			d, err := time.ParseDuration(strings.TrimPrefix(flag, "--timeout="))
			if err != nil || d < 0 {
				return Wrapped{}, false
			}
			w.Timeout = d
		default:
			return Wrapped{}, false
		}
	}
	if i+1 >= len(words) || words[i+1].text != "--" || strings.HasPrefix(words[i].text, "-") {
		return Wrapped{}, false
	}
	w.ID = words[i].text
	rest := words[i+2:]
	switch len(rest) {
	case 0:
	case 1:
		w.Command = rest[0].text
	default:
		// Hand-edited into several words: exec joins its arguments, so the
		// raw text is the closest faithful command.
		w.Command = strings.TrimRight(cmd[rest[0].start:], " \t")
	}
	return w, true
}

// ShellQuote returns s as one single-quoted POSIX shell word.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellQuoteIfNeeded leaves words made of safe characters bare.
func ShellQuoteIfNeeded(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./+:,@") == "" {
		return s
	}
	return ShellQuote(s)
}

// FormatDuration renders d compactly: 30m, 1h30m, 45s.
func FormatDuration(d time.Duration) string {
	if d%time.Second != 0 {
		return d.String()
	}
	var b strings.Builder
	if h := d / time.Hour; h > 0 {
		fmt.Fprintf(&b, "%dh", h)
		d -= h * time.Hour
	}
	if m := d / time.Minute; m > 0 {
		fmt.Fprintf(&b, "%dm", m)
		d -= m * time.Minute
	}
	if d > 0 || b.Len() == 0 {
		fmt.Fprintf(&b, "%ds", d/time.Second)
	}
	return b.String()
}

// ShellWords splits s into words like a POSIX shell, without expansions.
// It fails on an unterminated quote.
func ShellWords(s string) ([]string, error) {
	words, err := shellWords(s)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w.text
	}
	return out, nil
}

type word struct {
	text  string
	start int // byte offset in the input
}

// shellWords splits s like a POSIX shell would, without expansions:
// whitespace separates words; single quotes are literal; double quotes and
// backslashes escape.
func shellWords(s string) ([]word, error) {
	var words []word
	var cur strings.Builder
	in, start := false, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if in {
				words = append(words, word{cur.String(), start})
				cur.Reset()
				in = false
			}
			continue
		case !in:
			in, start = true, i
		}
		switch c {
		case '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated single quote")
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
		case '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\\\"$`\n", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New("unterminated double quote")
			}
		case '\\':
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			}
		default:
			cur.WriteByte(c)
		}
	}
	if in {
		words = append(words, word{cur.String(), start})
	}
	return words, nil
}
