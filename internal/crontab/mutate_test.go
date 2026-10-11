package crontab

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, name+".crontab"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// changedLines returns the 1-based numbers of lines that differ between two
// equally long texts.
func changedLines(t *testing.T, before, after []byte) []int {
	t.Helper()
	a, b := strings.SplitAfter(string(before), "\n"), strings.SplitAfter(string(after), "\n")
	if len(a) != len(b) {
		t.Fatalf("line count changed: %d → %d", len(a), len(b))
	}
	var out []int
	for i := range a {
		if a[i] != b[i] {
			out = append(out, i+1)
		}
	}
	return out
}

func TestSetEnabledTouchesOneLine(t *testing.T) {
	orig := loadFixture(t, "realistic_mixed")
	c := Parse(orig)
	if err := c.SetEnabled("k3f9a2", false); err != nil {
		t.Fatal(err)
	}
	paused := c.Serialize()
	if got := changedLines(t, orig, paused); !reflect.DeepEqual(got, []int{12}) {
		t.Errorf("pause changed lines %v, want [12]", got)
	}
	if j, _ := c.Job("k3f9a2"); j.Enabled {
		t.Error("job still enabled")
	}
	if err := c.SetEnabled("k3f9a2", true); err != nil {
		t.Fatal(err)
	}
	if string(c.Serialize()) != string(orig) {
		t.Error("pause + resume didn't restore the original bytes")
	}
	if err := c.SetEnabled("nope00", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v", err)
	}
}

func TestResumeKeepsIndentation(t *testing.T) {
	c := Parse([]byte("# gocron:job id=abc234\n  #gocron:off 0 0 * * * x\n"))
	if err := c.SetEnabled("abc234", true); err != nil {
		t.Fatal(err)
	}
	if got := string(c.Serialize()); got != "# gocron:job id=abc234\n  0 0 * * * x\n" {
		t.Errorf("got %q", got)
	}
}

func TestRemoveJobTouchesOnlyItsLines(t *testing.T) {
	orig := loadFixture(t, "realistic_mixed")
	c := Parse(orig)
	if err := c.RemoveJob("pdk4r2"); err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(orig), "\n")
	want := strings.Join(append(append([]string{}, lines[:14]...), lines[16:]...), "")
	if got := string(c.Serialize()); got != want {
		t.Errorf("remove produced\n%s\nwant\n%s", got, want)
	}
	if _, ok := c.Job("pdk4r2"); ok {
		t.Error("job still present")
	}
}

func TestUpdateJobTouchesOnlyItsLines(t *testing.T) {
	orig := loadFixture(t, "realistic_mixed")
	c := Parse(orig)
	j, ok := c.Job("s7nc0p")
	if !ok {
		t.Fatal("fixture job missing")
	}
	j.Schedule, j.Timeout, j.Tags = "*/20 * * * *", time.Hour, []string{"photos", "nas"}
	if err := c.UpdateJob(j, ""); err != nil {
		t.Fatal(err)
	}
	after := c.Serialize()
	if got := changedLines(t, orig, after); !reflect.DeepEqual(got, []int{13, 14}) {
		t.Errorf("update changed lines %v, want [13 14]", got)
	}
	got, _ := c.Job("s7nc0p")
	if got.Schedule != "*/20 * * * *" || got.Timeout != time.Hour || !got.NoOverlap || !reflect.DeepEqual(got.Tags, []string{"photos", "nas"}) {
		t.Errorf("job after update = %+v", got)
	}
}

func TestAddJobRoundTripsTrickyCommands(t *testing.T) {
	cmds := []string{
		`pg_dump app | gzip > /bk/app-$(date +%F).sql.gz`,
		`echo "it's" 'single' "double \"inner\"" | tee -a /tmp/x >/dev/null 2>&1`,
		`printf '%s %d%%\n' "$(whoami)" 100`,
		`a\%b and \\ backslashes`,
		"tabs\tinside  and  spaces",
	}
	for _, wrapped := range []bool{true, false} {
		c := Parse(loadFixture(t, "realistic_mixed"))
		for _, cmd := range cmds {
			in := Job{Name: "Tricky", Schedule: "0 3 * * *", Command: cmd, Enabled: true, Wrapped: wrapped, Timeout: 5 * time.Minute, NoOverlap: true}
			id, err := c.AddJob(in, "/usr/local/bin/gocron")
			if err != nil {
				t.Fatal(err)
			}
			reloaded := Parse(c.Serialize())
			got, ok := reloaded.Job(id)
			if !ok {
				t.Fatalf("job %s missing after reload", id)
			}
			if got.Command != cmd || got.Wrapped != wrapped {
				t.Errorf("wrapped=%v: command\n got: %q\nwant: %q", wrapped, got.Command, cmd)
			}
			// Edit something else and make sure the command still survives.
			got.Name = "Renamed"
			if err := reloaded.UpdateJob(got, ""); err != nil {
				t.Fatal(err)
			}
			if again, _ := Parse(reloaded.Serialize()).Job(id); again.Command != cmd {
				t.Errorf("after edit: %q, want %q", again.Command, cmd)
			}
		}
	}
}

func TestAddJobLineEndings(t *testing.T) {
	c := Parse(loadFixture(t, "no_trailing_newline"))
	if _, err := c.AddJob(Job{ID: "new234", Schedule: "@daily", Command: "x", Enabled: true}, ""); err != nil {
		t.Fatal(err)
	}
	if got := string(c.Serialize()); !strings.HasSuffix(got, "/usr/bin/tail-job\n# gocron:job id=new234\n@daily x\n") {
		t.Errorf("got %q", got)
	}

	c = Parse(loadFixture(t, "crlf"))
	if _, err := c.AddJob(Job{ID: "new234", Schedule: "@daily", Command: "x"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := string(c.Serialize()); !strings.HasSuffix(got, "\r\n# gocron:job id=new234\r\n#gocron:off @daily x\r\n") {
		t.Errorf("got %q", got)
	}
}

func TestAddJobValidates(t *testing.T) {
	c := Parse(loadFixture(t, "managed"))
	for _, tt := range []struct {
		job  Job
		bin  string
		want string
	}{
		{Job{Schedule: "61 * * * *", Command: "x"}, "", "schedule"},
		{Job{Schedule: "@daily", Command: "  "}, "", "empty"},
		{Job{Schedule: "@daily", Command: "a\nb"}, "", "single line"},
		{Job{Schedule: "@daily", Command: "x", Tags: []string{"a,b"}}, "", "tag"},
		{Job{Schedule: "@daily", Command: "x", Wrapped: true}, "", "binary path"},
		{Job{ID: "k3f9a2", Schedule: "@daily", Command: "x"}, "", "already exists"},
	} {
		if _, err := c.AddJob(tt.job, tt.bin); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("AddJob(%+v) err = %v, want %q", tt.job, err, tt.want)
		}
	}
}

func TestNewID(t *testing.T) {
	c := Parse(nil)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := c.NewID()
		if len(id) != 6 || strings.Trim(id, idAlphabet) != "" {
			t.Fatalf("bad id %q", id)
		}
		if seen[id] {
			t.Fatalf("NewID repeated an id in use: %s", id)
		}
		seen[id] = true
		if _, err := c.AddJob(Job{ID: id, Schedule: "@daily", Command: "x"}, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNextRunsHonorsCronTZ(t *testing.T) {
	jobs := Parse(loadFixture(t, "cron_tz")).Jobs()
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	// 02:30 Berlin (CEST) = 00:30 UTC; 09:00 New York (EDT) = 13:00 UTC.
	if got := jobs[0].NextRuns(from, 1); len(got) != 1 || !got[0].Equal(time.Date(2026, 6, 1, 0, 30, 0, 0, time.UTC)) || got[0].Location() != time.UTC {
		t.Errorf("Berlin job next = %v", got)
	}
	if got := jobs[1].NextRuns(from, 1); len(got) != 1 || !got[0].Equal(time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("New York job next = %v", got)
	}
}

func TestBetweenHonorsCronTZ(t *testing.T) {
	jobs := Parse(loadFixture(t, "cron_tz")).Jobs()
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	var got []time.Time
	for at := range jobs[0].Between(from, from.Add(48*time.Hour)) {
		got = append(got, at)
	}
	if len(got) != 2 || !got[0].Equal(time.Date(2026, 6, 1, 0, 30, 0, 0, time.UTC)) || got[0].Location() != time.UTC {
		t.Errorf("Berlin job fires = %v", got)
	}
	for range (Job{}).Between(from, from.Add(time.Hour)) {
		t.Error("a job without a schedule fired")
	}
}

func TestAdoptInsertsOnlyAMetaLine(t *testing.T) {
	orig := loadFixture(t, "realistic_mixed")
	c := Parse(orig)
	// Line 9: */5 * * * * ~/bin/sync.sh >/dev/null 2>&1
	raw := c.Lines[8].Raw
	id, err := c.Adopt(9, raw, "Sync", false, "")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(orig), "\n")
	want := strings.Join(lines[:8], "") + "# gocron:job id=" + id + " name=Sync\n" + strings.Join(lines[8:], "")
	if got := string(c.Serialize()); got != want {
		t.Errorf("adopt:\n%s\nwant:\n%s", got, want)
	}
	j, ok := c.Job(id)
	if !ok || !j.Managed || j.Wrapped || j.Name != "Sync" || j.Command != "~/bin/sync.sh >/dev/null 2>&1" || j.Line != 10 {
		t.Errorf("job = %+v", j)
	}
}

func TestAdoptWrapped(t *testing.T) {
	c := Parse([]byte("0 3 * * * echo 50% > /tmp/x\n"))
	id, err := c.Adopt(1, "0 3 * * * echo 50% > /tmp/x", "", true, "/usr/bin/gocron")
	if err != nil {
		t.Fatal(err)
	}
	want := "# gocron:job id=" + id + "\n0 3 * * * /usr/bin/gocron exec " + id + ` -- 'echo 50\% > /tmp/x'` + "\n"
	if got := string(c.Serialize()); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if j, _ := c.Job(id); !j.Wrapped || j.Command != "echo 50% > /tmp/x" {
		t.Errorf("job = %+v", j)
	}
}

func TestAdoptCRLFAndNoTrailingNewline(t *testing.T) {
	c := Parse([]byte("A=1\r\n@daily x"))
	id, err := c.Adopt(2, "@daily x", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(c.Serialize()); got != "A=1\r\n# gocron:job id="+id+"\r\n@daily x" {
		t.Errorf("got %q", got)
	}
}

func TestAdoptStale(t *testing.T) {
	c := Parse([]byte("# c\n@daily x\n# gocron:job id=abc234\n@daily y\n"))
	for _, tt := range []struct {
		line int
		raw  string
	}{{2, "@daily changed"}, {1, "# c"}, {4, "@daily y"}, {0, ""}, {9, ""}} {
		if _, err := c.Adopt(tt.line, tt.raw, "", false, ""); !errors.Is(err, ErrNotFound) {
			t.Errorf("Adopt(%d, %q): err = %v", tt.line, tt.raw, err)
		}
	}
}

func TestDefaultAdoptName(t *testing.T) {
	c := Parse([]byte(`# header
# more header
@reboot ~/bin/start-tmux.sh

# Back up photos
0 1 * * * rsync -a ~/p nas:/p
#
0 2 * * * FOO=1 nice -n 10 python3 -u /opt/jobs/report.py --weekly
0 3 * * * bash -c "~/x/backup.sh" >/dev/null 2>&1
0 4 * * * timeout 30m /usr/local/bin/sync-mail
`))
	want := map[int]string{3: "start-tmux.sh", 6: "Back up photos", 8: "report.py", 9: "backup.sh", 10: "sync-mail", 1: ""}
	for line, name := range want {
		if got := c.DefaultAdoptName(line); got != name {
			t.Errorf("line %d: %q, want %q", line, got, name)
		}
	}
}
