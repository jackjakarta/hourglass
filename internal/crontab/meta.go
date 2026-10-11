package crontab

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Meta is the key=value metadata of a "# gocron:job" line. Pairs keep their
// order, and keys gocron doesn't know survive a rewrite.
type Meta struct {
	Pairs []Pair
}

// Pair is one key=value of a meta line.
type Pair struct{ Key, Value string }

// Get returns the first value for key.
func (m *Meta) Get(key string) string {
	for _, p := range m.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// Set replaces key's value in place, or appends it.
func (m *Meta) Set(key, value string) {
	for i, p := range m.Pairs {
		if p.Key == key {
			m.Pairs[i].Value = value
			return
		}
	}
	m.Pairs = append(m.Pairs, Pair{key, value})
}

// Delete removes every pair with key.
func (m *Meta) Delete(key string) {
	out := m.Pairs[:0]
	for _, p := range m.Pairs {
		if p.Key != key {
			out = append(out, p)
		}
	}
	m.Pairs = out
}

// Tags returns the comma-separated tags, trimmed, without empties.
func (m *Meta) Tags() []string {
	var out []string
	for _, t := range strings.Split(m.Get("tags"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ParseMeta parses a "# gocron:job key=value …" line.
func ParseMeta(line string) (*Meta, error) {
	s, ok := strings.CutPrefix(strings.TrimSpace(line), metaPrefix)
	if !ok {
		return nil, errors.New("not a gocron meta line")
	}
	m := &Meta{}
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			break
		}
		i := 0
		for i < len(s) && (s[i] >= 'a' && s[i] <= 'z' || i > 0 && (s[i] >= '0' && s[i] <= '9' || s[i] == '_')) {
			i++
		}
		if i == 0 || i == len(s) || s[i] != '=' {
			return nil, fmt.Errorf("malformed gocron meta near %q (want key=value)", truncate(s, 20))
		}
		key := s[:i]
		s = s[i+1:]
		var value string
		if strings.HasPrefix(s, `"`) {
			end := quotedEnd(s)
			if end < 0 {
				return nil, fmt.Errorf("unterminated quoted value for %s", key)
			}
			v, err := strconv.Unquote(s[:end])
			if err != nil {
				return nil, fmt.Errorf("bad quoted value for %s: %v", key, err)
			}
			value, s = v, s[end:]
			if s != "" && !isBlank(s[0]) {
				return nil, fmt.Errorf("missing space after the value of %s", key)
			}
		} else {
			j := strings.IndexAny(s, " \t\"")
			if j < 0 {
				j = len(s)
			}
			if j < len(s) && s[j] == '"' {
				return nil, fmt.Errorf("unexpected quote in the value of %s", key)
			}
			value, s = s[:j], s[j:]
		}
		m.Pairs = append(m.Pairs, Pair{key, value})
	}
	if m.Get("id") == "" {
		return nil, errors.New("gocron meta line has no id")
	}
	return m, nil
}

// quotedEnd returns the index just past the closing quote of the Go string
// literal at the start of s, or -1.
func quotedEnd(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return -1
}

// Format renders the meta line.
func (m *Meta) Format() string {
	var b strings.Builder
	b.WriteString(metaPrefix)
	for _, p := range m.Pairs {
		b.WriteByte(' ')
		b.WriteString(p.Key)
		b.WriteByte('=')
		b.WriteString(quoteMetaValue(p.Value))
	}
	return b.String()
}

func quoteMetaValue(v string) string {
	needs := v == "" || strings.ContainsAny(v, `"=#`) || strings.IndexFunc(v, func(r rune) bool {
		return unicode.IsSpace(r) || !unicode.IsPrint(r)
	}) >= 0
	if needs {
		return strconv.Quote(v)
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
