package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func codes(issues []Issue) string {
	var out []string
	for _, is := range issues {
		out = append(out, is.Severity.String()+" "+is.Code)
	}
	return strings.Join(out, ", ")
}

func TestLintDraft(t *testing.T) {
	tests := []struct {
		d    Draft
		want string
	}{
		{Draft{Schedule: "30 2 * * *", Command: "/usr/bin/backup"}, ""},
		{Draft{Schedule: "nope", Command: "x"}, "error unparsable"},
		{Draft{Schedule: "* * * * *", Command: "x"}, "info every-minute"},
		{Draft{Schedule: "30 2 31 2 *", Command: "x"}, "warning dom-never"},
		{Draft{Schedule: "0 0 1 * 1", Command: "x"}, "info dom-dow-or"},
		{Draft{Schedule: "@daily", Command: "   "}, "error empty-command"},
		{Draft{Schedule: "@daily", Command: "a\nb"}, "error multiline-command"},
		{Draft{Schedule: "@daily", Command: "x", Tags: []string{"ok", "not ok"}}, "error bad-tag"},
		{Draft{Schedule: "@daily", Command: "date +%F"}, "info percent"},
		{Draft{Schedule: "@daily", Command: `date +\%F`}, "warning backslash-percent"},
		{Draft{Schedule: "@daily", Command: `echo 'unterminated`}, "warning unbalanced-quote"},
		{Draft{Schedule: "@daily", Command: "./run.sh"}, "warning relative-path"},
		{Draft{Schedule: "@daily", Command: "FOO=1 bin/run.sh --x"}, "warning relative-path"},
		{Draft{Schedule: "@daily", Command: "~/bin/run.sh"}, "info home"},
		{Draft{Schedule: "@daily", Command: "$HOME/bin/run.sh"}, "info home"},
		{Draft{Schedule: "@daily", Command: `cp x "~/backup"`}, "warning quoted-tilde"},
		{Draft{Schedule: "@daily", Command: "sudo apt update"}, "warning interactive"},
		{Draft{Schedule: "@daily", Command: "ls | less"}, "warning interactive"},
		{Draft{Schedule: "@daily", Command: "ssh host uptime"}, "warning interactive"},
		{Draft{Schedule: "@daily", Command: "ssh -o BatchMode=yes host uptime"}, ""},
		{Draft{Schedule: "@daily", Command: "echo vim"}, ""}, // not in command position
		{Draft{Schedule: "* * * * *", Command: "./x.sh %"}, "info every-minute, info percent, warning relative-path"},
	}
	for _, tt := range tests {
		if got := codes(LintDraft(tt.d)); got != tt.want {
			t.Errorf("LintDraft(%q, %q) = %q, want %q", tt.d.Schedule, tt.d.Command, got, tt.want)
		}
	}
	if !HasErrors(LintDraft(Draft{Schedule: "@daily"})) || HasErrors(LintDraft(Draft{Schedule: "@daily", Command: "%"})) {
		t.Error("HasErrors is wrong")
	}
}

func TestLintDraftFields(t *testing.T) {
	issues := LintDraft(Draft{Schedule: "bad", Command: "", Tags: []string{"a b"}})
	got := map[Field]bool{}
	for _, is := range issues {
		got[is.Field] = true
	}
	if !got[FieldSchedule] || !got[FieldCommand] || !got[FieldTags] {
		t.Errorf("fields = %v", got)
	}
}

func executable(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLintPath(t *testing.T) {
	root := t.TempDir()
	cronBin, localBin := filepath.Join(root, "usr", "bin"), filepath.Join(root, "local", "bin")
	executable(t, cronBin, "gzip", 0o755)
	pgDump := executable(t, localBin, "pg_dump", 0o755)
	script := executable(t, root, "script.sh", 0o755)
	noexec := executable(t, root, "noexec.sh", 0o644)
	env := CronEnv{PATH: cronBin}
	userPATH := cronBin + ":" + localBin

	tests := []struct {
		cmd, want, msg string
	}{
		{"gzip -9 file", "", ""},
		{"pg_dump app | gzip > x", "warning not-on-cron-path", "pg_dump is in " + localBin + ", which isn't on cron's PATH"},
		{"FOO=1 pg_dump app", "warning not-on-cron-path", "Use " + pgDump},
		{"nosuchprog", "warning not-found", "isn't on cron's PATH or yours"},
		{script + " --flag", "", ""},
		{filepath.Join(root, "missing.sh"), "warning not-found", "doesn't exist"},
		{noexec, "warning not-executable", "chmod +x"},
		{"cd /tmp && nosuchprog", "", ""}, // only the first program is checked
		{"echo hi", "", ""},
		{"$TOOL run", "", ""},
		{"./relative.sh", "", ""}, // LintDraft covers relative paths
		{"", "", ""},
	}
	cache := NewPathCache()
	for _, tt := range tests {
		issues := LintPath(tt.cmd, env, userPATH, cache)
		if got := codes(issues); got != tt.want {
			t.Errorf("LintPath(%q) = %q, want %q", tt.cmd, got, tt.want)
			continue
		}
		if tt.msg != "" && !strings.Contains(issues[0].Message, tt.msg) {
			t.Errorf("LintPath(%q) message %q doesn't contain %q", tt.cmd, issues[0].Message, tt.msg)
		}
	}

	// A crontab PATH= line that includes the directory fixes it.
	if issues := LintPath("pg_dump", CronEnv{PATH: userPATH}, userPATH, cache); len(issues) != 0 {
		t.Errorf("with PATH set: %v", codes(issues))
	}
	// Lookups are cached: removing the file doesn't change the answer.
	if err := os.Remove(pgDump); err != nil {
		t.Fatal(err)
	}
	if got := codes(LintPath("pg_dump", env, userPATH, cache)); got != "warning not-on-cron-path" {
		t.Errorf("cached lookup = %q", got)
	}
	if got := codes(LintPath("pg_dump", env, userPATH, NewPathCache())); got != "warning not-found" {
		t.Errorf("fresh lookup = %q", got)
	}
}
