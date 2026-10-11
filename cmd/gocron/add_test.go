package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	if !regexp.MustCompile(`^Created job [a-z2-7]{6} "Nightly backup" · backup: .+\n$`).MatchString(out) {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(errOut, `% will be escaped`) {
		t.Errorf("stderr = %q, want the %% note", errOut)
	}
	b, _ := os.ReadFile(file)
	if !regexp.MustCompile(`^MAILTO=""\n# gocron:job id=[a-z2-7]{6} name="Nightly backup" tags=backup,db,ops\n30 2 \* \* \* echo 'it'\\''s' "a\|b" > /tmp/x-\$\(date \+\\%F\) 2>&1\n$`).Match(b) {
		t.Errorf("crontab:\n%s", b)
	}

	// The command comes back unchanged.
	out, _, _ = runCLI(t, file, "list", "--json")
	var listed []jsonJob
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Command != cmd || strings.Join(listed[0].Tags, ",") != "backup,db,ops" || listed[0].Wrapped {
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
	if !strings.HasSuffix(string(b), "name=echo\n@daily echo hello world\n") {
		t.Errorf("crontab:\n%s", b)
	}
}

func TestAddErrors(t *testing.T) {
	tests := []struct {
		args   []string
		code   int
		stderr string
	}{
		{[]string{"add", "--", "echo"}, exitUsage, "Usage: gocron add"},
		{[]string{"add", "--schedule", "@daily"}, exitUsage, "Usage: gocron add"},
		{[]string{"add", "--bogus"}, exitUsage, "flag provided but not defined"},
		{[]string{"add", "--schedule", "61 * * * *", "--", "echo"}, exitError, `minute: "61" is out of range`},
		{[]string{"add", "--schedule", "@daily", "--tag", "a b", "--", "echo"}, exitError, `can't contain spaces`},
		{[]string{"add", "--schedule", "@daily", "--timeout", "5m", "--", "echo"}, exitUsage, "need run tracking"},
		{[]string{"add", "--schedule", "@daily", "--no-overlap", "--", "echo"}, exitUsage, "need run tracking"},
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
