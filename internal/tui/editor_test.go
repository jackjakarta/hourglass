package tui

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jackjakarta/hourglass/internal/cronexpr"
	"github.com/jackjakarta/hourglass/internal/jobs"
)

func TestGoldenEditorNew(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 40)
	hs.waitFor("Nightly backup")
	hs.keys("n")
	hs.waitFor("New job")
	requireGolden(t, hs.screen())
}

func TestGoldenEditorCustom(t *testing.T) {
	hs := start(t, "realistic_mixed", 120, 40)
	hs.waitFor("Nightly backup")
	hs.keys("n", "tab", "left", "left", "left", "tab", "ctrl+u", "*/15 9-17 * * 1-5", "left", "left", "left", "left", "left")
	hs.waitFor("dom  mon")
	requireGolden(t, hs.screen())
}

func TestGoldenEditorEdit(t *testing.T) {
	hs := start(t, "realistic_mixed", 100, 32)
	hs.waitFor("Nightly backup")
	hs.keys("down", "down", "e")
	hs.waitFor("Edit · Nightly backup")
	requireGolden(t, hs.screen())
}

func TestGoldenEditorNarrow(t *testing.T) {
	hs := start(t, "realistic_mixed", 80, 24)
	hs.waitFor("Nightly backup")
	hs.keys("n", "tab", "right", "right", "tab", "right", "space")
	hs.waitFor("0 9 * * 1,2")
	requireGolden(t, hs.screen())
}

func TestEditorCreate(t *testing.T) {
	hs := start(t, "", 100, 32)
	hs.waitFor("Welcome to hourglass")
	const cmd = `pg_dump app | gzip > /bk/app-$(date +%F).sql.gz`
	hs.keys("n", "Nightly backup", "tab", "tab", "0230", "tab", "tab", cmd, "tab", "backup, db", "ctrl+s")
	hs.waitFor(`Created "Nightly backup" · backup created · u undo`)
	// Tracked by default: wrapped, skipping overlaps (config defaults).
	re := regexp.MustCompile(`^# hourglass:job id=([a-z2-7]{6}) name="Nightly backup" tags=backup,db\n30 2 \* \* \* /usr/bin/hourglass exec --no-overlap [a-z2-7]{6} -- 'pg_dump app \| gzip > /bk/app-\$\(date \+\\%F\)\.sql\.gz'\n$`)
	if got := hs.read(); !re.MatchString(got) {
		t.Errorf("crontab:\n%s", got)
	}
	s := hs.screen()
	if strings.Contains(s, "New job") || !strings.Contains(s, "Nightly backup") {
		t.Errorf("editor still open or job missing:\n%s", s)
	}
}

func TestEditorEditTouchesOnlyItsLines(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 40)
	orig := hs.read()
	hs.waitFor("Nightly backup")
	hs.keys("down", "down", "e")
	hs.waitFor("Edit · Nightly backup")
	hs.keys("tab", "right", "ctrl+s") // Daily → Weekdays
	hs.waitFor(`Saved "Nightly backup"`)
	got, want := strings.Split(hs.read(), "\n"), strings.Split(orig, "\n")
	if len(got) != len(want) {
		t.Fatalf("line count changed:\n%s", hs.read())
	}
	var changed []string
	for i := range want {
		if got[i] != want[i] {
			changed = append(changed, got[i])
		}
	}
	if len(changed) != 1 || changed[0] != `30 2 * * 1-5 /home/jack/.local/bin/hourglass exec --timeout=30m --no-overlap k3f9a2 -- 'pg_dump app | gzip > /bk/app-$(date +\%F).sql.gz'` {
		t.Errorf("changed lines: %q", changed)
	}
	hs.screen()
}

func TestEditorSaveWithoutChanges(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 40)
	orig := hs.read()
	hs.waitFor("Nightly backup")
	hs.keys("down", "down", "e")
	hs.waitFor("Edit · Nightly backup")
	hs.keys("ctrl+s")
	hs.waitFor("Nothing changed.")
	if hs.read() != orig || hs.backupCount() != 0 {
		t.Error("an unchanged save wrote the crontab")
	}
	hs.screen()
}

func TestEditorDiscardConfirm(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 40)
	orig := hs.read()
	hs.waitFor("Nightly backup")

	hs.keys("n", "esc") // clean: closes at once
	hs.waitFor("n new")
	hs.keys("n", "x", "esc")
	hs.waitFor("Discard your changes?")
	hs.keys("esc") // the safe choice: keep editing
	hs.waitFor("tab next")
	hs.keys("esc")
	hs.waitFor("Discard your changes?")
	hs.keys("right", "enter")
	hs.waitFor("n new")
	s := hs.screen()
	if strings.Contains(s, "New job") {
		t.Errorf("editor still open:\n%s", s)
	}
	if hs.read() != orig {
		t.Error("discarding wrote the crontab")
	}
}

func TestEditorCapturesKeys(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 40)
	hs.waitFor("Nightly backup")
	hs.keys("n", "q2u?")
	hs.waitFor("q2u?")
	s := hs.screen()
	if !strings.Contains(s, "New job") || strings.Contains(s, "Coming soon") {
		t.Errorf("global keys fired in the editor:\n%s", s)
	}
}

func TestEditorBlockedSave(t *testing.T) {
	hs := start(t, "", 100, 32)
	hs.waitFor("Welcome to hourglass")
	hs.keys("n", "ctrl+s")
	hs.waitFor("Fix the problems marked ✗ before saving.")
	hs.waitFor("Enter the command to run.")
	if b, err := os.ReadFile(hs.path); err == nil || len(b) > 0 {
		t.Errorf("an invalid job was saved:\n%s", b)
	}
	hs.screen()
}

func TestEditorPathLint(t *testing.T) {
	hs := startWith(t, "", 100, 32, func(o *Options) {
		o.PathLint = func(cmd string, env jobs.CronEnv) []jobs.Issue {
			if strings.HasPrefix(cmd, "pg_dump") && env.PATH == jobs.DefaultCronPATH {
				return []jobs.Issue{{Field: jobs.FieldCommand, Issue: cronexpr.Issue{Severity: cronexpr.Warn, Message: "pg_dump is in /usr/local/bin, which isn't on cron's PATH"}}}
			}
			return nil
		}
	})
	hs.waitFor("Welcome to hourglass")
	hs.keys("n", "tab", "tab", "tab", "tab", "pg_dump app")
	hs.waitFor("pg_dump is in /usr/local/bin, which isn't on cron's PATH")
	hs.screen()
}

func TestEditOnUnmanagedAsksToAdopt(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 40)
	hs.waitFor("Nightly backup")
	hs.keys("G", "e")
	hs.waitFor("Press a to adopt it first")
	hs.screen()
}

func TestEditorConflictKeepEditing(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 40)
	hs.waitFor("Nightly backup")
	hs.keys("n", "tab", "tab", "tab", "tab", "echo hi")
	hs.waitFor("echo hi")
	hs.write(hs.read() + "# edited elsewhere\n") // the dirty editor keeps this from reloading
	hs.keys("ctrl+s")
	hs.waitFor("Crontab changed outside hourglass")
	hs.keys("enter") // Keep editing
	hs.waitFor("your edits are still here")
	hs.keys("ctrl+s")
	hs.waitFor(`Created "echo"`)
	if got := hs.read(); !strings.Contains(got, "# edited elsewhere\n# hourglass:job id=") || !regexp.MustCompile(`0 9 \* \* \* /usr/bin/hourglass exec --no-overlap [a-z2-7]{6} -- 'echo hi'\n$`).MatchString(got) {
		t.Errorf("crontab:\n%s", got)
	}
	hs.screen()
}
