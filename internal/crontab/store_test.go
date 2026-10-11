package crontab

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type storeEnv struct {
	src     FileSource
	backups string
	store   *Store
}

func newStoreEnv(t *testing.T, initial string, keep int) *storeEnv {
	t.Helper()
	dir := t.TempDir()
	e := &storeEnv{src: FileSource{Path: filepath.Join(dir, "crontab")}, backups: filepath.Join(dir, "backups")}
	if initial != "" {
		if err := os.WriteFile(e.src.Path, []byte(initial), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tick := time.Date(2026, 10, 11, 16, 55, 0, 0, time.UTC)
	s, err := Open(context.Background(), e.src, Options{BackupDir: e.backups, Keep: keep, Now: func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.store = s
	return e
}

func (e *storeEnv) read(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.src.Path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const managedTab = "# gocron:job id=abc234 name=A\n0 0 * * * a\n"

func TestSaveWritesAndBacksUp(t *testing.T) {
	ctx := context.Background()
	e := newStoreEnv(t, managedTab, 50)
	for i, enabled := range []bool{false, true, false} {
		c := e.store.Crontab()
		if err := c.SetEnabled("abc234", enabled); err != nil {
			t.Fatal(err)
		}
		before := string(e.store.Content())
		backup, err := e.store.Save(ctx, c)
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		if b, err := os.ReadFile(backup); err != nil || string(b) != before {
			t.Errorf("save %d: backup %s = %q, %v; want the pre-save content %q", i, backup, b, err, before)
		}
		if got := e.read(t); got != string(c.Serialize()) {
			t.Errorf("save %d: file = %q", i, got)
		}
		if all, _ := e.store.Backups(); len(all) != i+1 {
			t.Errorf("save %d: %d backups, want %d", i, len(all), i+1)
		}
	}
	if fi, err := os.Stat(e.src.Path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("crontab file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
}

func TestSaveConflict(t *testing.T) {
	ctx := context.Background()
	e := newStoreEnv(t, managedTab, 50)
	c := e.store.Crontab()
	if err := c.SetEnabled("abc234", false); err != nil {
		t.Fatal(err)
	}
	external := managedTab + "*/5 * * * * added-by-crontab-e\n"
	if err := os.WriteFile(e.src.Path, []byte(external), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := e.store.Changed(ctx); err != nil || !changed {
		t.Errorf("Changed = %v, %v; want true", changed, err)
	}
	if _, err := e.store.Save(ctx, c); !errors.Is(err, ErrConflict) {
		t.Fatalf("Save err = %v, want ErrConflict", err)
	}
	if got := e.read(t); got != external {
		t.Errorf("conflicting save overwrote the file: %q", got)
	}
	if all, _ := e.store.Backups(); len(all) != 0 {
		t.Errorf("conflicting save left backups: %v", all)
	}

	// Reapply: reload, replay the change on fresh state.
	if err := e.store.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	c = e.store.Crontab()
	if err := c.SetEnabled("abc234", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	if got := e.read(t); !strings.Contains(got, "added-by-crontab-e") || !strings.Contains(got, "#gocron:off 0 0 * * * a") {
		t.Errorf("reapplied content = %q", got)
	}
}

func TestSaveFromNoCrontab(t *testing.T) {
	ctx := context.Background()
	e := newStoreEnv(t, "", 50)
	c := e.store.Crontab()
	if _, err := c.AddJob(Job{ID: "new234", Schedule: "@daily", Command: "x", Enabled: true}, ""); err != nil {
		t.Fatal(err)
	}
	backup, err := e.store.Save(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(backup); len(b) != 0 {
		t.Errorf("backup of a missing crontab should be empty, got %q", b)
	}
	if got := e.read(t); got != "# gocron:job id=new234\n@daily x\n" {
		t.Errorf("file = %q", got)
	}
}

func TestSaveUnchangedIsNoop(t *testing.T) {
	e := newStoreEnv(t, managedTab, 50)
	backup, err := e.store.Save(context.Background(), e.store.Crontab())
	if err != nil || backup != "" {
		t.Errorf("Save(unchanged) = %q, %v", backup, err)
	}
	if all, _ := e.store.Backups(); len(all) != 0 {
		t.Errorf("unchanged save made backups: %v", all)
	}
}

func TestSaveAddsFinalNewline(t *testing.T) {
	e := newStoreEnv(t, "# gocron:job id=abc234\n0 0 * * * a", 50)
	c := e.store.Crontab()
	if err := c.SetEnabled("abc234", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got := e.read(t); got != "# gocron:job id=abc234\n#gocron:off 0 0 * * * a\n" {
		t.Errorf("file = %q", got)
	}
}

func TestSavePrunesBackups(t *testing.T) {
	ctx := context.Background()
	e := newStoreEnv(t, managedTab, 3)
	var made []string
	for i := 0; i < 6; i++ {
		c := e.store.Crontab()
		if err := c.SetEnabled("abc234", i%2 == 1); err != nil {
			t.Fatal(err)
		}
		b, err := e.store.Save(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, b)
	}
	all, err := e.store.Backups()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(all, ",") != strings.Join(made[3:], ",") {
		t.Errorf("kept backups %v, want the newest three %v", all, made[3:])
	}
}

// lyingSource writes but reads back something else.
type lyingSource struct{ FileSource }

func (l lyingSource) Write(ctx context.Context, b []byte) error {
	return l.FileSource.Write(ctx, append(b, "# tampered\n"...))
}

func TestSaveVerifyMismatch(t *testing.T) {
	dir := t.TempDir()
	src := lyingSource{FileSource{Path: filepath.Join(dir, "crontab")}}
	if err := os.WriteFile(src.Path, []byte(managedTab), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), src, Options{BackupDir: filepath.Join(dir, "b")})
	if err != nil {
		t.Fatal(err)
	}
	c := s.Crontab()
	_ = c.SetEnabled("abc234", false)
	_, err = s.Save(context.Background(), c)
	var ve *VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("Save err = %v, want VerifyError", err)
	}
	if b, _ := os.ReadFile(ve.BackupPath); string(b) != managedTab {
		t.Errorf("VerifyError backup = %q", b)
	}
}

func TestSystemSourceRefusedInTests(t *testing.T) {
	t.Setenv(EnvAllowSystem, "")
	if _, err := NewSystemSource(); err == nil {
		t.Fatal("NewSystemSource succeeded under go test without opt-in")
	}
	t.Setenv(EnvCrontabFile, "")
	if _, err := SourceFromEnv(); err == nil {
		t.Fatal("SourceFromEnv fell through to the real crontab under go test")
	}
	t.Setenv(EnvCrontabFile, "/tmp/x")
	if s, err := SourceFromEnv(); err != nil || s.Name() != "file: /tmp/x" {
		t.Errorf("SourceFromEnv = %v, %v", s, err)
	}
}
