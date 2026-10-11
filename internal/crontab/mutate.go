package crontab

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/jackjakarta/gocron/internal/cronexpr"
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

// SetEnabled pauses or resumes a managed job by toggling the "#gocron:off "
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
// bin is the gocron binary path used when j.Wrapped.
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
// keys gocron doesn't know are kept. bin overrides the binary path of a
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
			return "", errors.New("wrapped job needs the gocron binary path")
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
