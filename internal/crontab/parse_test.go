package crontab

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

const fixtureDir = "../../testdata/crontabs"

func fixtures(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.crontab"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 10 {
		t.Fatalf("spec requires ≥ 10 fixtures, found %d", len(paths))
	}
	return paths
}

func TestRoundTripFixtures(t *testing.T) {
	for _, p := range fixtures(t) {
		in, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if out := Parse(in).Serialize(); !bytes.Equal(out, in) {
			t.Errorf("%s: round trip changed the content\n got: %q\nwant: %q", filepath.Base(p), out, in)
		}
	}
}

func TestRoundTripEdgeCases(t *testing.T) {
	for _, in := range []string{
		"", "\n", "\n\n", "\r\n", "x", "\r", "a\rb\n", "no newline", "0 0 * * * cmd", "trailing \t \n",
		"\r\n\r\n", "mixed\r\nendings\nhere\r\n", "# hourglass:job id=x\n", "# hourglass:job id=x\n0 0 * * * y",
	} {
		if out := string(Parse([]byte(in)).Serialize()); out != in {
			t.Errorf("round trip %q → %q", in, out)
		}
	}
}

// dump renders a crontab's classification for golden files.
func dump(c *Crontab) string {
	var b strings.Builder
	for i, l := range c.Lines {
		fmt.Fprintf(&b, "%3d %-7s", i+1, l.Kind)
		switch {
		case l.Env != nil:
			fmt.Fprintf(&b, " %s=%q", l.Env.Name, l.Env.Value)
		case l.Entry != nil:
			fmt.Fprintf(&b, " sched=%q cmd=%q", l.Entry.Schedule, l.Entry.Command)
			if l.Entry.Disabled {
				b.WriteString(" disabled")
			}
		case l.Meta != nil:
			for _, p := range l.Meta.Pairs {
				fmt.Fprintf(&b, " %s=%q", p.Key, p.Value)
			}
		}
		if l.Orphan {
			b.WriteString(" ORPHAN")
		}
		if l.Err != nil {
			fmt.Fprintf(&b, " err=%q", l.Err)
		}
		if l.EOL != "\n" {
			fmt.Fprintf(&b, " eol=%q", l.EOL)
		}
		b.WriteByte('\n')
	}
	b.WriteString("--- jobs\n")
	for _, j := range c.Jobs() {
		fmt.Fprintf(&b, "line=%d managed=%v id=%q name=%q tags=%q enabled=%v wrapped=%v sched=%q tz=%q\n    cmd=%q",
			j.Line, j.Managed, j.ID, j.Name, j.Tags, j.Enabled, j.Wrapped, j.Schedule, j.TZ, j.Command)
		if j.Wrapped {
			fmt.Fprintf(&b, " bin=%q timeout=%s no_overlap=%v", j.Bin, j.Timeout, j.NoOverlap)
		}
		b.WriteByte('\n')
	}
	b.WriteString("--- problems\n")
	for _, p := range c.Problems() {
		fmt.Fprintf(&b, "%s [%s]\n", p, p.Code)
	}
	return b.String()
}

func TestParseGolden(t *testing.T) {
	for _, p := range fixtures(t) {
		in, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		got := dump(Parse(in))
		golden := strings.TrimSuffix(p, ".crontab") + ".golden"
		if *update {
			if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run go test -update)", err)
		}
		if got != string(want) {
			t.Errorf("%s: classification changed (run go test -update to accept)\n--- got\n%s--- want\n%s", filepath.Base(p), got, want)
		}
	}
}

func FuzzRoundTrip(f *testing.F) {
	for _, s := range []string{"", "0 0 * * * x\n", "# hourglass:job id=a\n#hourglass:off @daily y\r\n", "A=b\n\tz"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		c := Parse(in)
		if out := c.Serialize(); !bytes.Equal(out, in) {
			t.Fatalf("round trip %q → %q", in, out)
		}
		_ = c.Jobs()
		_ = c.Problems()
	})
}
