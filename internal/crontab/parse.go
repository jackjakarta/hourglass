// Package crontab reads and writes crontabs without disturbing lines it
// doesn't own. Every line keeps its original text, so Serialize(Parse(x))
// is always x, byte for byte.
package crontab

import (
	"errors"
	"regexp"
	"strings"

	"github.com/jackjakarta/hourglass/internal/cronexpr"
)

// Kind classifies a crontab line.
type Kind int

const (
	Blank   Kind = iota
	Comment      // includes orphaned hourglass meta lines (Line.Orphan)
	Env          // NAME=value
	Entry        // a cron entry hourglass doesn't manage
	JobMeta      // "# hourglass:job …" followed by its JobLine
	JobLine      // the cron line of a managed job (maybe "#hourglass:off ")
	Invalid      // not a comment, env line or valid entry
)

func (k Kind) String() string {
	switch k {
	case Blank:
		return "blank"
	case Comment:
		return "comment"
	case Env:
		return "env"
	case Entry:
		return "entry"
	case JobMeta:
		return "jobmeta"
	case JobLine:
		return "jobline"
	case Invalid:
		return "invalid"
	}
	return "unknown"
}

// Line is one line of a crontab.
type Line struct {
	Raw  string // the text without its line ending
	EOL  string // "\n", "\r\n", or "" for a final line without one
	Kind Kind

	Env    *EnvVar    // Kind == Env
	Entry  *CronEntry // Kind == Entry or JobLine; also a "#hourglass:off" comment
	Meta   *Meta      // Kind == JobMeta, or an orphan meta comment
	Orphan bool       // a hourglass meta line not followed by its cron line
	Err    error      // Kind == Invalid, or a malformed meta comment
}

// EnvVar is an environment assignment line.
type EnvVar struct{ Name, Value string }

// CronEntry is a schedule plus command.
type CronEntry struct {
	Schedule string // as written, including internal whitespace
	Command  string // as written: still %-escaped, trailing whitespace kept
	Disabled bool   // written as "#hourglass:off <entry>"
	Expr     *cronexpr.Expr
}

const (
	metaPrefix = "# hourglass:job"
	offPrefix  = "#hourglass:off "
)

// Crontab is a parsed crontab.
type Crontab struct {
	Lines []*Line
}

// Parse splits content into lines and classifies them. It never fails:
// lines it can't understand are kept as Invalid.
func Parse(content []byte) *Crontab {
	c := &Crontab{}
	s := string(content)
	for s != "" {
		raw, rest, found := strings.Cut(s, "\n")
		eol := ""
		if found {
			eol = "\n"
			if strings.HasSuffix(raw, "\r") {
				raw, eol = raw[:len(raw)-1], "\r\n"
			}
		}
		c.Lines = append(c.Lines, parseLine(raw, eol))
		s = rest
	}
	c.pair()
	return c
}

// Serialize returns the crontab's bytes.
func (c *Crontab) Serialize() []byte {
	var b strings.Builder
	for _, l := range c.Lines {
		b.WriteString(l.Raw)
		b.WriteString(l.EOL)
	}
	return []byte(b.String())
}

var envRe = regexp.MustCompile(`^[ \t]*("[^"]*"|'[^']*'|[^\s="']+)[ \t]*=(.*)$`)

// parseLine classifies a line on its own; pair() then links meta lines to
// the cron lines that follow them.
func parseLine(raw, eol string) *Line {
	l := &Line{Raw: raw, EOL: eol}
	trimmed := strings.TrimSpace(raw)
	switch {
	case trimmed == "":
		l.Kind = Blank
	case strings.HasPrefix(trimmed, "#"):
		l.Kind = Comment
		if isMetaLine(trimmed) {
			l.Meta, l.Err = ParseMeta(trimmed)
		} else if rest, ok := strings.CutPrefix(strings.TrimLeft(raw, " \t"), offPrefix); ok {
			if e, err := parseEntry(rest); err == nil {
				e.Disabled = true
				l.Entry = e
			}
		}
	case envRe.MatchString(raw):
		m := envRe.FindStringSubmatch(raw)
		l.Kind = Env
		l.Env = &EnvVar{Name: unquote(m[1]), Value: unquote(strings.TrimSpace(m[2]))}
	default:
		e, err := parseEntry(raw)
		if err != nil {
			l.Kind, l.Err = Invalid, err
		} else {
			l.Kind, l.Entry = Entry, e
		}
	}
	return l
}

func isMetaLine(trimmed string) bool {
	rest, ok := strings.CutPrefix(trimmed, metaPrefix)
	return ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t')
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func isBlank(b byte) bool { return b == ' ' || b == '\t' }

// splitEntry splits "<schedule> <command>" where the schedule is five
// fields or one @-descriptor.
func splitEntry(text string) (schedule, command string, err error) {
	s := strings.TrimLeft(text, " \t")
	n := 5
	if strings.HasPrefix(s, "@") {
		n = 1
	}
	i := 0
	for f := 0; f < n; f++ {
		for i < len(s) && isBlank(s[i]) {
			i++
		}
		if i == len(s) {
			return "", "", errors.New("not a cron entry (too few fields)")
		}
		for i < len(s) && !isBlank(s[i]) {
			i++
		}
	}
	j := i
	for j < len(s) && isBlank(s[j]) {
		j++
	}
	if j == len(s) {
		return "", "", errors.New("missing command")
	}
	return s[:i], s[j:], nil
}

func parseEntry(text string) (*CronEntry, error) {
	sched, cmd, err := splitEntry(text)
	if err != nil {
		return nil, err
	}
	expr, err := cronexpr.Parse(sched)
	if err != nil {
		return nil, err
	}
	return &CronEntry{Schedule: sched, Command: cmd, Expr: expr}, nil
}

// pair marks each meta line followed by a cron line as a managed job and
// every other meta line as an orphan.
func (c *Crontab) pair() {
	for _, l := range c.Lines {
		switch {
		case l.Kind == JobMeta:
			l.Kind = Comment
		case l.Kind == JobLine && l.Entry.Disabled:
			l.Kind = Comment
		case l.Kind == JobLine:
			l.Kind = Entry
		}
	}
	for i, l := range c.Lines {
		if l.Meta == nil || l.Kind == JobLine {
			continue
		}
		var next *Line
		if i+1 < len(c.Lines) {
			next = c.Lines[i+1]
		}
		if next != nil && next.Entry != nil && (next.Kind == Entry || next.Kind == Comment) {
			l.Kind, l.Orphan = JobMeta, false
			next.Kind = JobLine
			continue
		}
		l.Kind, l.Orphan = Comment, true
	}
}
