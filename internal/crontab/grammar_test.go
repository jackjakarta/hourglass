package crontab

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestParseMeta(t *testing.T) {
	tests := []struct {
		line    string
		want    []Pair
		wantErr string
	}{
		{`# hourglass:job id=k3f9a2`, []Pair{{"id", "k3f9a2"}}, ""},
		{`  # hourglass:job   id=k3f9a2  name="Nightly backup" tags=backup,db  `,
			[]Pair{{"id", "k3f9a2"}, {"name", "Nightly backup"}, {"tags", "backup,db"}}, ""},
		{`# hourglass:job id=a desc="say \"hi\"\tthere" x_2=""`,
			[]Pair{{"id", "a"}, {"desc", "say \"hi\"\tthere"}, {"x_2", ""}}, ""},
		{`# hourglass:job id=a name=caf√©`, []Pair{{"id", "a"}, {"name", "caf√©"}}, ""},
		{`# hourglass:job name=x`, nil, "no id"},
		{`# hourglass:job id=a name="open`, nil, "unterminated"},
		{`# hourglass:job id=a Name=x`, nil, "malformed"},
		{`# hourglass:job id=a name`, nil, "malformed"},
		{`# hourglass:job id=a name="x"y`, nil, "missing space"},
		{`# hourglass:job id=a name=x"y`, nil, "unexpected quote"},
		{`# hourglass:job id=a name="\q"`, nil, "bad quoted value"},
	}
	for _, tt := range tests {
		m, err := ParseMeta(tt.line)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ParseMeta(%q) err = %v, want %q", tt.line, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMeta(%q): %v", tt.line, err)
			continue
		}
		if !reflect.DeepEqual(m.Pairs, tt.want) {
			t.Errorf("ParseMeta(%q) = %v, want %v", tt.line, m.Pairs, tt.want)
		}
	}
}

func TestFormatMetaQuoting(t *testing.T) {
	m := &Meta{Pairs: []Pair{{"id", "k3f9a2"}, {"name", "Nightly backup"}, {"tags", "a,b"}, {"desc", ""}, {"x", "a=b"}, {"y", "#1"}, {"z", `back\slash`}}}
	want := `# hourglass:job id=k3f9a2 name="Nightly backup" tags=a,b desc="" x="a=b" y="#1" z=back\slash`
	if got := m.Format(); got != want {
		t.Errorf("Format\n got: %s\nwant: %s", got, want)
	}
}

func TestMetaUnknownKeysSurviveUpdate(t *testing.T) {
	c := Parse([]byte("# hourglass:job id=abc234 future=1 name=Old\n0 0 * * * x\n"))
	j, _ := c.Job("abc234")
	j.Name = "New name"
	if err := c.UpdateJob(j, ""); err != nil {
		t.Fatal(err)
	}
	if got := c.Lines[0].Raw; got != `# hourglass:job id=abc234 future=1 name="New name"` {
		t.Errorf("meta after update = %s", got)
	}
}

func FuzzMeta(f *testing.F) {
	f.Add("abc234", "Nightly backup", "a,b", `say "hi"`, "x_1", "=#")
	f.Fuzz(func(t *testing.T, id, name, tags, desc, key, extra string) {
		if !validKey(key) || id == "" {
			return
		}
		m := &Meta{Pairs: []Pair{{"id", id}, {"name", name}, {"tags", tags}, {"desc", desc}, {key, extra}}}
		line := m.Format()
		if strings.ContainsAny(line, "\r\n") {
			t.Fatalf("Format produced a multi-line meta: %q", line)
		}
		got, err := ParseMeta(line)
		if err != nil {
			t.Fatalf("ParseMeta(Format(%v)) = %v\nline: %q", m.Pairs, err, line)
		}
		if !reflect.DeepEqual(got.Pairs, m.Pairs) {
			t.Fatalf("round trip\n got: %q\nwant: %q\nline: %q", got.Pairs, m.Pairs, line)
		}
	})
}

func validKey(k string) bool {
	if k == "" || k[0] < 'a' || k[0] > 'z' {
		return false
	}
	return strings.Trim(k, "abcdefghijklmnopqrstuvwxyz0123456789_") == ""
}

func TestWrappedEncode(t *testing.T) {
	tests := []struct {
		w    Wrapped
		want string
	}{
		{Wrapped{Bin: "/usr/local/bin/hourglass", ID: "k3f9a2", Command: "echo hi"},
			`/usr/local/bin/hourglass exec k3f9a2 -- 'echo hi'`},
		{Wrapped{Bin: "/home/jack/.local/bin/hourglass", ID: "k3f9a2", Timeout: 30 * time.Minute, NoOverlap: true, Command: "pg_dump app | gzip > /bk/app-$(date +%F).sql.gz"},
			`/home/jack/.local/bin/hourglass exec --timeout=30m --no-overlap k3f9a2 -- 'pg_dump app | gzip > /bk/app-$(date +%F).sql.gz'`},
		{Wrapped{Bin: "/home/my tools/hourglass", ID: "a", Timeout: 90 * time.Minute, Command: "it's"},
			`'/home/my tools/hourglass' exec --timeout=1h30m a -- 'it'\''s'`},
	}
	for _, tt := range tests {
		if got := tt.w.Encode(); got != tt.want {
			t.Errorf("Encode\n got: %s\nwant: %s", got, tt.want)
		}
		back, ok := DecodeWrapped(tt.want)
		if !ok || back != tt.w {
			t.Errorf("DecodeWrapped(%s) = %+v, %v; want %+v", tt.want, back, ok, tt.w)
		}
	}
}

func TestDecodeWrappedRejects(t *testing.T) {
	for _, cmd := range []string{
		"echo hi",
		"hourglass list",
		"/usr/bin/hourglass-other exec id -- 'x'",
		"hourglass exec --tee id -- 'x'",
		"hourglass exec --timeout=banana id -- 'x'",
		"hourglass exec id 'x'",
		"hourglass exec -- 'x'",
		"hourglass exec id -- 'unterminated",
	} {
		if w, ok := DecodeWrapped(cmd); ok {
			t.Errorf("DecodeWrapped(%q) = %+v, want not wrapped", cmd, w)
		}
	}
}

func FuzzWrapped(f *testing.F) {
	for _, s := range []string{"echo hi", `it's "quoted" | tee > $(x) %F \%`, "", "a\tb  c", `\`} {
		f.Add(s, int64(0), false)
	}
	f.Fuzz(func(t *testing.T, cmd string, timeoutSec int64, noOverlap bool) {
		if strings.ContainsAny(cmd, "\r\n") || !utf8.ValidString(cmd) || timeoutSec < 0 || timeoutSec > 1e6 {
			return
		}
		w := Wrapped{Bin: "/usr/local/bin/hourglass", ID: "abc234", Timeout: time.Duration(timeoutSec) * time.Second, NoOverlap: noOverlap, Command: cmd}
		line := EscapePercent(w.Encode())
		got, ok := DecodeWrapped(UnescapePercent(line))
		if !ok || got != w {
			t.Fatalf("round trip of %q\n got: %+v (%v)\nwant: %+v\nline: %s", cmd, got, ok, w, line)
		}
	})
}

func TestPercent(t *testing.T) {
	tests := []struct{ plain, escaped string }{
		{"date +%F", `date +\%F`},
		{"no percent", "no percent"},
		{"%%", `\%\%`},
		{`a\%b`, `a\\%b`},
	}
	for _, tt := range tests {
		if got := EscapePercent(tt.plain); got != tt.escaped {
			t.Errorf("EscapePercent(%q) = %q, want %q", tt.plain, got, tt.escaped)
		}
		if got := UnescapePercent(tt.escaped); got != tt.plain {
			t.Errorf("UnescapePercent(%q) = %q, want %q", tt.escaped, got, tt.plain)
		}
	}
}

func FuzzPercent(f *testing.F) {
	for _, s := range []string{"%", `\%`, `\\%%`, "plain"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		e := EscapePercent(s)
		if got := UnescapePercent(e); got != s {
			t.Fatalf("UnescapePercent(EscapePercent(%q)) = %q", s, got)
		}
		if strings.Count(e, "%") != strings.Count(s, "%") {
			t.Fatalf("escape changed the number of %%: %q → %q", s, e)
		}
	})
}

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Minute: "30m", 90 * time.Minute: "1h30m", 45 * time.Second: "45s", 2 * time.Hour: "2h",
		time.Hour + time.Second: "1h1s", 1500 * time.Millisecond: "1.5s",
	} {
		if got := FormatDuration(d); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
