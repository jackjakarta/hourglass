package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func mixed(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "crontabs", "realistic_mixed.crontab"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// startCrontab runs realistic_mixed with two seeded revisions: an older
// one without "Say hi", and a newer one with Nightly backup at 03:30.
// edit, when set, replaces the editor: it gets the copy's text and
// returns what the user saves.
func startCrontab(t *testing.T, w, h int, edit func(string) string) *harness {
	t.Helper()
	orig := mixed(t)
	revs := map[string]string{
		"crontab-2026-10-10T09-00-00.000000000Z": strings.Replace(orig, "# hourglass:job id=p7m2qd name=\"Say hi\"\n0 9 * * 1-5 notify-send \"hi\"\n", "", 1),
		"crontab-2026-10-11T12-00-00.000000000Z": strings.Replace(orig, "30 2 * * *", "30 3 * * *", 1),
	}
	return startWith(t, "realistic_mixed", w, h, func(o *Options) {
		if err := os.MkdirAll("backups", 0o700); err != nil {
			t.Fatal(err)
		}
		for name, content := range revs {
			if err := os.WriteFile(filepath.Join("backups", name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if edit != nil {
			o.RunEditor = func(path string, done func(error) tea.Msg) tea.Cmd {
				return func() tea.Msg {
					b, err := os.ReadFile(path)
					if err == nil {
						err = os.WriteFile(path, []byte(edit(string(b))), 0o600)
					}
					return done(err)
				}
			}
		}
	})
}

func TestGoldenCrontab(t *testing.T) {
	hs := startCrontab(t, 140, 32, nil)
	hs.waitFor("Nightly backup")
	hs.keys("4")
	hs.waitFor("Revisions · 2")
	requireGolden(t, hs.screen())
}

func TestGoldenCrontabDiff(t *testing.T) {
	hs := startCrontab(t, 140, 32, nil)
	hs.waitFor("Nightly backup")
	hs.keys("4")
	hs.waitFor("Revisions · 2")
	hs.keys("down")
	hs.waitFor("RESTORING WOULD")
	requireGolden(t, hs.screen())
}

func TestGoldenCrontabNarrow(t *testing.T) {
	hs := startCrontab(t, 100, 24, nil)
	hs.waitFor("Nightly backup")
	hs.keys("4")
	hs.waitFor("Revisions · 2")
	hs.keys("down", "down", "enter")
	hs.waitFor("RESTORING WOULD")
	requireGolden(t, hs.screen())
}

func TestCrontabRestoreUndo(t *testing.T) {
	hs := startCrontab(t, 140, 32, nil)
	orig := hs.read()
	hs.waitFor("Nightly backup")
	hs.keys("4")
	hs.waitFor("Revisions · 2")
	hs.keys("down", "down", "R")
	hs.waitFor("Restore this revision?")
	hs.waitFor("Restoring would: −1 job.")
	hs.keys("right", "enter")
	hs.waitFor(`Restored "revision from Sat 10 Oct 09`)
	hs.waitFor("Revisions · 3") // the restore made a backup too
	if strings.Contains(hs.read(), "Say hi") {
		t.Errorf("the restore didn't write the revision:\n%s", hs.read())
	}
	hs.keys("u")
	hs.waitFor("Undid the last change")
	if hs.read() != orig {
		t.Errorf("undo didn't bring the crontab back:\n%s", hs.read())
	}
	hs.screen()
}

func TestCrontabEditInvalid(t *testing.T) {
	hs := startCrontab(t, 140, 32, func(s string) string { return s + "*/5 * * * \n" })
	orig := hs.read()
	hs.waitFor("Nightly backup")
	hs.keys("4", "E")
	hs.waitFor("The crontab has problems")
	hs.waitFor("line 24: not a valid cron line")
	hs.keys("right", "enter") // Discard
	s := hs.screen()
	if strings.Contains(s, "has problems") {
		t.Errorf("the dialog didn't close:\n%s", s)
	}
	if hs.read() != orig || hs.backupCount() != 2 {
		t.Error("an invalid edit wrote the crontab")
	}
}

func TestCrontabEditValid(t *testing.T) {
	hs := startCrontab(t, 140, 32, func(s string) string { return s + "# by hand\n" })
	orig := hs.read()
	hs.waitFor("Nightly backup")
	hs.keys("4", "E")
	hs.waitFor("Saved the crontab · backup created")
	if hs.read() != orig+"# by hand\n" {
		t.Errorf("crontab after edit:\n%s", hs.read())
	}
	hs.waitFor("Revisions · 3")
	hs.keys("u")
	hs.waitFor("Undid the last change")
	if hs.read() != orig {
		t.Error("undo didn't revert the edit")
	}
	hs.screen()
}

func TestCrontabEditUnchanged(t *testing.T) {
	hs := startCrontab(t, 140, 32, func(s string) string { return s })
	hs.waitFor("Nightly backup")
	hs.keys("4", "E")
	hs.waitFor("No changes.")
	hs.screen()
	if hs.backupCount() != 2 {
		t.Error("an unchanged edit made a backup")
	}
}

func TestCrontabEditConflict(t *testing.T) {
	var hs *harness
	hs = startCrontab(t, 140, 32, func(s string) string {
		hs.write(s + "# someone else\n") // an outside change while editing
		return s + "# mine\n"
	})
	hs.waitFor("Nightly backup")
	hs.keys("4", "E")
	hs.waitFor("The crontab changed while you were editing")
	if !strings.Contains(hs.read(), "# someone else") || strings.Contains(hs.read(), "# mine") {
		t.Errorf("the outside change was overwritten:\n%s", hs.read())
	}
	hs.screen()
}
