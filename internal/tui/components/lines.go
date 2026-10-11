package components

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Lines collects streamed program output as display-safe lines, keeping
// the most recent max of them. Chunks may split lines and runes anywhere.
type Lines struct {
	max     int
	lines   []string
	partial []byte // the line still being written
	dropped int    // lines that scrolled out of the buffer
}

// NewLines returns a buffer that keeps up to max lines.
func NewLines(max int) *Lines { return &Lines{max: max} }

// Write adds output.
func (b *Lines) Write(p []byte) {
	b.partial = append(b.partial, p...)
	for {
		i := strings.IndexByte(string(b.partial), '\n')
		if i < 0 {
			break
		}
		b.push(Sanitize(string(b.partial[:i])))
		b.partial = b.partial[i+1:]
	}
	if len(b.partial) > 64<<10 { // a very long line: show it in pieces
		cut := len(b.partial)
		for cut > 0 && !utf8.RuneStart(b.partial[cut-1]) {
			cut--
		}
		if cut > 0 {
			cut--
		}
		b.push(Sanitize(string(b.partial[:cut])))
		b.partial = b.partial[cut:]
	}
}

func (b *Lines) push(l string) {
	b.lines = append(b.lines, l)
	if over := len(b.lines) - b.max; over > 0 {
		b.lines = append(b.lines[:0:0], b.lines[over:]...)
		b.dropped += over
	}
}

// Lines returns the complete lines plus the one being written, if any.
func (b *Lines) Lines() []string {
	if len(b.partial) == 0 {
		return b.lines
	}
	p := b.partial
	for len(p) > 0 && !utf8.FullRune(p[len(p)-len(lastRuneStart(p)):]) {
		p = p[:len(p)-len(lastRuneStart(p))] // a rune split across chunks
	}
	return append(append([]string(nil), b.lines...), Sanitize(string(p)))
}

func lastRuneStart(p []byte) []byte {
	i := len(p) - 1
	for i > 0 && !utf8.RuneStart(p[i]) {
		i--
	}
	return p[i:]
}

// Dropped is how many lines scrolled out of the buffer.
func (b *Lines) Dropped() int { return b.dropped }

// Len is the number of lines Lines returns.
func (b *Lines) Len() int {
	n := len(b.lines)
	if len(b.partial) > 0 {
		n++
	}
	return n
}

// Sanitize makes one line of program output safe to draw: escape
// sequences go, a carriage return keeps only what was drawn last (as a
// progress bar would leave it), tabs become spaces, other control
// characters and invalid UTF-8 are replaced.
func Sanitize(s string) string {
	s = strings.TrimSuffix(s, "\r")
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	s = ansi.Strip(strings.ToValidUTF8(s, "�"))
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := 8 - col%8
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}
