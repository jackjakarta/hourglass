package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackjakarta/gocron/internal/crontab"
)

// envHelper makes the test binary act as gocron itself, so tests can run
// "gocron exec" as real, separate processes.
const envHelper = "GOCRON_TEST_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(envHelper) == "1" {
		main()
	}
	time.Local = time.UTC
	os.Exit(m.Run())
}

// testBin is the gocron path tests write into wrapped lines.
const testBin = "/usr/local/bin/gocron"

// Sun 11 Oct 2026 14:32:07 UTC
var testNow = time.Date(2026, 10, 11, 14, 32, 7, 0, time.UTC)

func runCLI(t *testing.T, crontabFile string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv(crontab.EnvCrontabFile, crontabFile)
	t.Setenv("GOCRON_STATE_DIR", t.TempDir())
	var out, errOut bytes.Buffer
	a := &app{stdout: &out, stderr: &errOut, now: func() time.Time { return testNow }, source: crontab.SourceFromEnv,
		bin: func() string { return testBin }}
	code = a.run(context.Background(), args)
	return out.String(), errOut.String(), code
}

func fixture(name string) string {
	p, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "crontabs", name+".crontab"))
	return p
}

func TestList(t *testing.T) {
	out, errOut, code := runCLI(t, fixture("realistic_mixed"), "list")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	want := `ID      NAME            SCHEDULE      WHEN                             NEXT        STATE
k3f9a2  Nightly backup  30 2 * * *    At 02:30, every day              in 11h 57m  ● enabled
s7nc0p  Sync photos     */15 * * * *  Every 15 minutes                 in 12m 53s  ● enabled
pdk4r2  Prune docker    0 4 * * 0     At 04:00, on Sundays             in 6d 13h   ● enabled
r0tl6s  Rotate logs     0 * * * *     Every hour                       in 27m 53s  ● enabled
wkrp7t  Weekly report   0 9 * * 1     At 09:00, on Mondays             —           ○ paused
p7m2qd  Say hi          0 9 * * 1-5   At 09:00, Monday through Friday  in 18h 27m  ● enabled

+ 3 unmanaged entries (gocron list --all)
`
	if out != want {
		t.Errorf("list output:\n%s\nwant:\n%s", out, want)
	}
}

func TestListAllAndProblems(t *testing.T) {
	out, errOut, code := runCLI(t, fixture("orphan_meta"), "list", "--all")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out, "/usr/bin/unmanaged-after-bad-me…") || !strings.Contains(out, "· unmanaged") {
		t.Errorf("--all output missing the unmanaged entry:\n%s", out)
	}
	if !strings.Contains(errOut, "7 problem(s)") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestListJSON(t *testing.T) {
	out, _, code := runCLI(t, fixture("cron_tz"), "list", "--json", "--all")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	var jobs []jsonJob
	if err := json.Unmarshal([]byte(out), &jobs); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	// 02:30 in Berlin (CEST, UTC+2) is 00:30 UTC.
	if j := jobs[0]; j.TZ != "Europe/Berlin" || j.Next == nil || *j.Next != "2026-10-12T00:30:00Z" ||
		j.Description != "At 02:30, every day (Europe/Berlin)" {
		t.Errorf("job = %+v next=%v", j, *j.Next)
	}
}

func TestListEmpty(t *testing.T) {
	out, _, code := runCLI(t, filepath.Join(t.TempDir(), "missing"), "list")
	if code != 0 || !strings.HasPrefix(out, "No gocron jobs in file: ") {
		t.Errorf("code=%d out=%q", code, out)
	}
	out, _, _ = runCLI(t, fixture("simple"), "list")
	if !strings.Contains(out, "3 unmanaged entries; see them with gocron list --all") {
		t.Errorf("out=%q", out)
	}
}

func TestDescribe(t *testing.T) {
	out, _, code := runCLI(t, "", "describe", "*/15 9-17 * * 1-5")
	want := `*/15 9-17 * * 1-5
Every 15 minutes, between 09:00 and 17:59, Monday through Friday

Next runs (local time):
  Mon 12 Oct 09:00   in 18h 27m
  Mon 12 Oct 09:15   in 18h 42m
  Mon 12 Oct 09:30   in 18h 57m
  Mon 12 Oct 09:45   in 19h 12m
  Mon 12 Oct 10:00   in 19h 27m
`
	if code != 0 || out != want {
		t.Errorf("code=%d out:\n%s\nwant:\n%s", code, out, want)
	}

	// Unquoted fields are joined, and lints are shown.
	out, _, _ = runCLI(t, "", "describe", "-n", "0", "30", "2", "31", "*", "*")
	if !strings.Contains(out, "⚠ Skips months without a 31st") || strings.Contains(out, "Next runs") {
		t.Errorf("out:\n%s", out)
	}
	out, _, _ = runCLI(t, "", "describe", "@reboot")
	if !strings.Contains(out, "At boot") || !strings.Contains(out, "each time the system boots") {
		t.Errorf("out:\n%s", out)
	}
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		args []string
		code int
		out  string // in stdout or stderr
	}{
		{nil, 0, "The gocron TUI needs a terminal."},
		{[]string{"help"}, 0, "Usage:"},
		{[]string{"bogus"}, 2, `unknown command "bogus"`},
		{[]string{"describe"}, 2, "Usage: gocron describe"},
		{[]string{"describe", "61 * * * *"}, 1, `minute: "61" is out of range`},
		{[]string{"list", "--nope"}, 2, "flag provided but not defined"},
		{[]string{"version"}, 0, "gocron "},
	}
	for _, tt := range tests {
		out, errOut, code := runCLI(t, fixture("simple"), tt.args...)
		if code != tt.code || !strings.Contains(out+errOut, tt.out) {
			t.Errorf("gocron %v: code=%d (want %d)\nstdout: %s\nstderr: %s", tt.args, code, tt.code, out, errOut)
		}
	}
}

func TestDoctorAgainstFile(t *testing.T) {
	out, _, code := runCLI(t, fixture("invalid_lines"), "doctor")
	if code != 0 && code != 1 { // 1 when this machine has no cron daemon
		t.Fatalf("code=%d", code)
	}
	for _, want := range []string{"✓ crontab access", "⚠ crontab contents", "line 3: not a valid cron line", "· MAILTO"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func TestRealCrontabIsRefusedInTests(t *testing.T) {
	_, errOut, code := runCLI(t, "", "list")
	if code != 1 || !strings.Contains(errOut, "refusing to use the real crontab") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}
