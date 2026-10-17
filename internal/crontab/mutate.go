package crontab

import (
	"crypto/rand"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/jackjakarta/hourglass/internal/cronexpr"
)

// ErrNotFound means no managed job has the given ID.
var ErrNotFound = errors.New("job not found")

const idAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

// NewID returns a random 6-character lowercase base32 ID unused in c.
func (c *Crontab) NewID() string {
	used := map[string]bool{}
	for _, l := range c.Lines {
		if l.Meta != nil {
			used[l.Meta.Get("id")] = true
		}
	}
	for {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic(err) // crypto/rand doesn't fail on supported platforms
		}
		for i := range b {
			b[i] = idAlphabet[b[i]%32]
		}
		if id := string(b[:]); !used[id] {
			return id
		}
	}
}

// metaIndex returns the line index of the managed job's meta line.
func (c *Crontab) metaIndex(id string) (int, error) {
	for i, l := range c.Lines {
		if l.Kind == JobMeta && l.Meta.Get("id") == id {
			return i, nil
		}
	}
	return 0, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// SetEnabled pauses or resumes a managed job by toggling the "#hourglass:off "
// prefix on its cron line. Nothing else changes.
func (c *Crontab) SetEnabled(id string, enabled bool) error {
	i, err := c.metaIndex(id)
	if err != nil {
		return err
	}
	l := c.Lines[i+1]
	if l.Entry.Disabled == !enabled {
		return nil
	}
	raw := l.Raw
	if enabled {
		lead := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
		raw = lead + strings.TrimPrefix(raw[len(lead):], offPrefix)
	} else {
		raw = offPrefix + raw
	}
	c.Lines[i+1] = parseLine(raw, l.EOL)
	c.pair()
	return nil
}

// RemoveJob deletes a managed job's two lines.
func (c *Crontab) RemoveJob(id string) error {
	i, err := c.metaIndex(id)
	if err != nil {
		return err
	}
	c.Lines = append(c.Lines[:i], c.Lines[i+2:]...)
	c.pair()
	return nil
}

// AddJob appends a managed job and returns its ID (j.ID, or a new one).
// bin is the hourglass binary path used when j.Wrapped.
func (c *Crontab) AddJob(j Job, bin string) (string, error) {
	if j.ID == "" {
		j.ID = c.NewID()
	} else if _, err := c.metaIndex(j.ID); err == nil {
		return "", fmt.Errorf("job id %s already exists", j.ID)
	}
	m := &Meta{Pairs: []Pair{{"id", j.ID}}}
	applyMeta(m, j)
	line, err := renderJobLine(j, bin)
	if err != nil {
		return "", err
	}
	eol := c.eol()
	if n := len(c.Lines); n > 0 && c.Lines[n-1].EOL == "" {
		c.Lines[n-1].EOL = eol
	}
	c.Lines = append(c.Lines, parseLine(m.Format(), eol), parseLine(line, eol))
	c.pair()
	return j.ID, nil
}

// UpdateJob rewrites the meta and cron lines of the managed job j.ID. Meta
// keys hourglass doesn't know are kept. bin overrides the binary path of a
// wrapped job; "" keeps j.Bin.
func (c *Crontab) UpdateJob(j Job, bin string) error {
	i, err := c.metaIndex(j.ID)
	if err != nil {
		return err
	}
	if bin == "" {
		bin = j.Bin
	}
	line, err := renderJobLine(j, bin)
	if err != nil {
		return err
	}
	m := &Meta{Pairs: append([]Pair(nil), c.Lines[i].Meta.Pairs...)}
	applyMeta(m, j)
	c.Lines[i] = parseLine(m.Format(), c.Lines[i].EOL)
	c.Lines[i+1] = parseLine(line, c.Lines[i+1].EOL)
	c.pair()
	return nil
}

func applyMeta(m *Meta, j Job) {
	for _, kv := range []Pair{{"name", j.Name}, {"tags", strings.Join(j.Tags, ",")}, {"desc", j.Desc}} {
		if kv.Value == "" {
			m.Delete(kv.Key)
		} else {
			m.Set(kv.Key, kv.Value)
		}
	}
}

func renderJobLine(j Job, bin string) (string, error) {
	sched := normalizeSpace(j.Schedule)
	if _, err := cronexpr.Parse(sched); err != nil {
		return "", fmt.Errorf("schedule: %w", err)
	}
	if strings.TrimSpace(j.Command) == "" {
		return "", errors.New("command is empty")
	}
	if strings.ContainsAny(j.Command, "\r\n") {
		return "", errors.New("command must be a single line")
	}
	for _, t := range j.Tags {
		if t == "" || strings.ContainsAny(t, ", \t") {
			return "", fmt.Errorf("tag %q must be non-empty without commas or spaces", t)
		}
	}
	cmd := j.Command
	if j.Wrapped {
		if bin == "" {
			return "", errors.New("wrapped job needs the hourglass binary path")
		}
		cmd = Wrapped{Bin: bin, ID: j.ID, Timeout: j.Timeout, NoOverlap: j.NoOverlap, Command: j.Command}.Encode()
	}
	line := sched + " " + EscapePercent(cmd)
	if !j.Enabled {
		line = offPrefix + line
	}
	return line, nil
}

// eol is the line ending new lines should use: CRLF if the crontab
// already uses it, else LF.
func (c *Crontab) eol() string {
	for _, l := range c.Lines {
		if l.EOL != "" {
			return l.EOL
		}
	}
	return "\n"
}

// Adopt turns the unmanaged entry on line (1-based) into a managed job by
// inserting a meta line above it, and returns the new job's ID. raw must
// match the line's current text, so a stale request fails with ErrNotFound
// instead of adopting a different line. Without wrap the cron line is kept
// byte for byte; with wrap it is rewritten to run through "hourglass exec" at
// bin.
func (c *Crontab) Adopt(line int, raw, name string, wrap bool, bin string) (string, error) {
	i := line - 1
	if i < 0 || i >= len(c.Lines) || c.Lines[i].Kind != Entry || c.Lines[i].Raw != raw {
		return "", fmt.Errorf("%w: no unmanaged entry %q on line %d", ErrNotFound, raw, line)
	}
	id := c.NewID()
	m := &Meta{Pairs: []Pair{{"id", id}}}
	if name != "" {
		m.Set("name", name)
	}
	l := c.Lines[i]
	if wrap {
		j := Job{
			ID:       id,
			Schedule: l.Entry.Schedule,
			Command:  UnescapePercent(strings.TrimRight(l.Entry.Command, " \t")),
			Enabled:  true,
			Wrapped:  true,
		}
		text, err := renderJobLine(j, bin)
		if err != nil {
			return "", err
		}
		l = parseLine(text, l.EOL)
	}
	eol := l.EOL
	if eol == "" {
		eol = c.eol()
	}
	rest := append([]*Line{parseLine(m.Format(), eol), l}, c.Lines[i+1:]...)
	c.Lines = append(c.Lines[:i], rest...)
	c.pair()
	return id, nil
}

// DefaultAdoptName suggests a name for adopting the entry on line
// (1-based): a lone comment line directly above it, or else the name of
// the program the command runs.
func (c *Crontab) DefaultAdoptName(line int) string {
	i := line - 1
	if i < 0 || i >= len(c.Lines) || c.Lines[i].Entry == nil {
		return ""
	}
	isComment := func(k int) bool {
		return k >= 0 && c.Lines[k].Kind == Comment && c.Lines[k].Meta == nil && c.Lines[k].Entry == nil
	}
	if isComment(i-1) && !isComment(i-2) {
		if s := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(c.Lines[i-1].Raw), "#")); s != "" {
			return clip(s, 40)
		}
	}
	return CommandName(UnescapePercent(c.Lines[i].Entry.Command))
}

// launchers are programs whose first operand is the interesting part.
var launchers = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "env": true, "exec": true,
	"nice": true, "ionice": true, "nohup": true, "timeout": true, "flock": true, "chronic": true,
	"python": true, "python3": true, "node": true, "ruby": true, "perl": true, "php": true,
}

// CommandName returns the basename of the program cmd runs, skipping env
// assignments, launchers and their flags: "~/bin/sync.sh >/dev/null" is
// "sync.sh", "bash -c ~/x/backup.sh" is "backup.sh".
func CommandName(cmd string) string {
	var fields []string
	if words, err := shellWords(cmd); err == nil {
		for _, w := range words {
			fields = append(fields, w.text)
		}
	} else {
		fields = strings.Fields(cmd)
	}
	first := ""
	for _, f := range fields {
		base := path.Base(f)
		switch {
		case f == "" || strings.ContainsAny(f[:1], "<>|&;"):
			continue
		case first == "" && strings.Contains(f, "=") && !strings.Contains(f, "/"):
			continue // FOO=bar
		case first != "" && (strings.HasPrefix(f, "-") || isNumber(f)):
			continue
		case launchers[base] || strings.HasPrefix(base, "python"):
			if first == "" {
				first = base
			}
			continue
		}
		return clip(base, 40)
	}
	return first
}

func isNumber(s string) bool {
	return strings.Trim(s, "0123456789.smhd") == ""
}

// clip shortens s to at most n runes, ending in "…" when cut.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// ReID gives the managed job whose meta line is line (1-based) the new ID
// id, in its meta line and, when it is wrapped, in its exec command. It
// works by line, so it can tell apart jobs that share an ID.
func (c *Crontab) ReID(line int, id string) error {
	i := line - 1
	if i < 0 || i+1 >= len(c.Lines) || c.Lines[i].Kind != JobMeta {
		return fmt.Errorf("%w: no hourglass job on line %d", ErrNotFound, line)
	}
	m := &Meta{Pairs: append([]Pair(nil), c.Lines[i].Meta.Pairs...)}
	m.Set("id", id)
	j := managedJob(c.Lines[i].Meta, c.Lines[i+1].Entry, line+1, "")
	if j.Wrapped {
		j.ID = id
		text, err := renderJobLine(j, j.Bin)
		if err != nil {
			return err
		}
		c.Lines[i+1] = parseLine(text, c.Lines[i+1].EOL)
	}
	c.Lines[i] = parseLine(m.Format(), c.Lines[i].EOL)
	c.pair()
	return nil
}
