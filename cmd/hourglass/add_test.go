package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackjakarta/hourglass/internal/crontab"
)

func TestAdd(t *testing.T) {
	file := filepath.Join(t.TempDir(), "crontab")
	if err := os.WriteFile(file, []byte("MAILTO=\"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const cmd = `echo 'it'\''s' "a|b" > /tmp/x-$(date +%F) 2>&1`
	out, errOut, code := runCLI(t, file, "add", "--name", "Nightly backup", "--schedule", "30 2 * * *", "--tag", "backup,db", "--tag", "ops", "--", cmd)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	m := regexp.MustCompile(`^Created job ([a-z2-7]{6}) "Nightly backup" · backup: .+\n$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("stdout = %q", out)
	}
	id := m[1]
	if !strings.Contains(errOut, `% will be escaped`) {
		t.Errorf("stderr = %q, want the %% note", errOut)
	}
	// Tracked by default (config defaults: wrap, no_overlap), with the
	// command as one quoted, %-escaped word.
	b, _ := os.ReadFile(file)
	want := "MAILTO=\"\"\n# hourglass:job id=" + id + " name=\"Nightly backup\" tags=backup,db,ops\n" +
		"30 2 * * * " + testBin + " exec --no-overlap " + id + " -- " +
		crontab.EscapePercent(crontab.ShellQuote(cmd)) + "\n"
	if string(b) != want {
		t.Errorf("crontab:\n%s\nwant:\n%s", b, want)
	}

	// The command comes back unchanged.
	out, _, _ = runCLI(t, file, "list", "--json")
	var listed []jsonJob
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Command != cmd || strings.Join(listed[0].Tags, ",") != "backup,db,ops" ||
		!listed[0].Wrapped || !listed[0].NoOverlap {
		t.Errorf("listed = %+v", listed)
	}
}

func TestAddDefaultsAndJoinedWords(t *testing.T) {
	file := filepath.Join(t.TempDir(), "crontab")
	out, errOut, code := runCLI(t, file, "add", "--schedule", "@daily", "--", "echo", "hello", "world")
	if code != 0 || !strings.Contains(out, `"echo"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	b, _ := os.ReadFile(file)
	if !regexp.MustCompile(`name=echo\n@daily /usr/local/bin/hourglass exec --no-overlap [a-z2-7]{6} -- 'echo hello world'\n$`).Match(b) {
		t.Errorf("crontab:\n%s", b)
	}
}

func TestAddErrors(t *testing.T) {
	tests := []struct {
		args   []string
		code   int
		stderr string
	}{
		{[]string{"add", "--", "echo"}, exitUsage, "Usage: hourglass add"},
		{[]string{"add", "--schedule", "@daily"}, exitUsage, "Usage: hourglass add"},
		{[]string{"add", "--bogus"}, exitUsage, "flag provided but not defined"},
		{[]string{"add", "--schedule", "61 * * * *", "--", "echo"}, exitError, `minute: "61" is out of range`},
		{[]string{"add", "--schedule", "@daily", "--tag", "a b", "--", "echo"}, exitError, `can't contain spaces`},
		{[]string{"add", "--raw", "--schedule", "@daily", "--timeout", "5m", "--", "echo"}, exitUsage, "need run tracking"},
		{[]string{"add", "--raw", "--schedule", "@daily", "--no-overlap", "--", "echo"}, exitUsage, "need run tracking"},
		{[]string{"add", "--schedule", "@daily", "--timeout", "-5m", "--", "echo"}, exitUsage, "can't be negative"},
	}
	for _, tt := range tests {
		file := filepath.Join(t.TempDir(), "crontab")
		_, errOut, code := runCLI(t, file, tt.args...)
		if code != tt.code || !strings.Contains(errOut, tt.stderr) {
			t.Errorf("%q: code=%d stderr=%q; want %d and %q", tt.args, code, errOut, tt.code, tt.stderr)
		}
		if _, err := os.Stat(file); err == nil {
			t.Errorf("%q wrote the crontab", tt.args)
		}
	}
}

func TestAddRaw(t *testing.T) {
	file := filepath.Join(t.TempDir(), "crontab")
	if _, errOut, code := runCLI(t, file, "add", "--raw", "--schedule", "@hourly", "--", "echo hi"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	b, _ := os.ReadFile(file)
	if !strings.HasSuffix(string(b), "@hourly echo hi\n") {
		t.Errorf("crontab:\n%s", b)
	}
}

func TestAddOptionsAndConfigDefaults(t *testing.T) {
	file := filepath.Join(t.TempDir(), "crontab")
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[defaults]\nwrap = false\nno_overlap = false\ntimeout = \"2h\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOURGLASS_CONFIG", cfg)

	// wrap = false gives a plain line.
	if _, errOut, code := runCLI(t, file, "add", "--schedule", "@hourly", "--", "echo a"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	// Asking for overlap prevention turns tracking on anyway, with the
	// configured timeout; flags beat defaults.
	if _, errOut, code := runCLI(t, file, "add", "--schedule", "@hourly", "--no-overlap", "--", "echo b"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := runCLI(t, file, "add", "--schedule", "@hourly", "--timeout", "5m", "--", "echo c"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	b, _ := os.ReadFile(file)
	want := `(?s)@hourly echo a\n.*@hourly /usr/local/bin/hourglass exec --timeout=2h --no-overlap [a-z2-7]{6} -- 'echo b'\n` +
		`.*@hourly /usr/local/bin/hourglass exec --timeout=5m [a-z2-7]{6} -- 'echo c'\n$`
	if !regexp.MustCompile(want).Match(b) {
		t.Errorf("crontab:\n%s", b)
	}
}

func TestAddNeedsAnInstalledHourglass(t *testing.T) {
	file := filepath.Join(t.TempDir(), "crontab")
	t.Setenv(crontab.EnvCrontabFile, file)
	t.Setenv("HOURGLASS_STATE_DIR", t.TempDir())
	var out, errOut strings.Builder
	a := &app{stdout: &out, stderr: &errOut, now: time.Now, source: crontab.SourceFromEnv,
		bin: func() string { return filepath.Join(os.TempDir(), "go-build123/b001/exe/hourglass") }}
	if code := a.run(context.Background(), []string{"add", "--schedule", "@daily", "--", "echo"}); code != exitError ||
		!strings.Contains(errOut.String(), "temporary build") || !strings.Contains(errOut.String(), "--raw") {
		t.Errorf("code=%d stderr=%q", code, errOut.String())
	}
	if code := a.run(context.Background(), []string{"add", "--raw", "--schedule", "@daily", "--", "echo"}); code != exitOK {
		t.Errorf("--raw: code=%d stderr=%q", code, errOut.String())
	}
}
