package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jackjakarta/gocron/internal/crontab"
)

const tab = `MAILTO=""
@reboot ~/bin/start-tmux.sh
# gocron:job id=abc234 name=A
0 0 * * * a
# gocron:job id=def567 name=B
#gocron:off 0 1 * * * b
`

type env struct {
	path, backups string
	svc           *Service
}

func newEnv(t *testing.T, initial string) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{path: filepath.Join(dir, "crontab"), backups: filepath.Join(dir, "backups")}
	if initial != "" {
		e.write(t, initial)
	}
	store, err := crontab.Open(context.Background(), crontab.FileSource{Path: e.path}, crontab.Options{BackupDir: e.backups})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = New(store, "/usr/bin/gocron")
	return e
}

func (e *env) read(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (e *env) write(t *testing.T, s string) {
	t.Helper()
	if err := os.WriteFile(e.path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e *env) backupCount(t *testing.T) int {
	t.Helper()
	entries, _ := os.ReadDir(e.backups)
	return len(entries)
}

func TestSnapshot(t *testing.T) {
	e := newEnv(t, tab+"bogus line\n")
	snap := e.svc.Snapshot()
	if len(snap.Jobs) != 3 || snap.Jobs[0].Managed || !snap.Jobs[1].Managed || snap.Jobs[2].Enabled {
		t.Errorf("jobs = %+v", snap.Jobs)
	}
	if len(snap.Problems) != 1 || !strings.HasPrefix(snap.Source, "file: ") {
		t.Errorf("snapshot = %+v", snap)
	}
}

func TestChangesAndUndo(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, tab)
	if e.svc.CanUndo() {
		t.Error("CanUndo before any change")
	}

	if _, err := e.svc.SetEnabled(ctx, "abc234", false); err != nil {
		t.Fatal(err)
	}
	afterPause := e.read(t)
	if !strings.Contains(afterPause, "#gocron:off 0 0 * * * a\n") {
		t.Errorf("not paused:\n%s", afterPause)
	}
	res, err := e.svc.Delete(ctx, "def567")
	if err != nil || res.Backup == "" || res.ID != "def567" {
		t.Fatalf("delete: %+v %v", res, err)
	}
	res, err = e.svc.Adopt(ctx, 2, "@reboot ~/bin/start-tmux.sh")
	if err != nil {
		t.Fatal(err)
	}
	adopted := e.read(t)
	if !strings.Contains(adopted, "# gocron:job id="+res.ID+" name=start-tmux.sh\n@reboot ~/bin/start-tmux.sh\n") {
		t.Errorf("adopt:\n%s", adopted)
	}
	if n := e.backupCount(t); n != 3 {
		t.Errorf("%d backups after 3 changes", n)
	}

	// A no-op change writes nothing and adds no undo step.
	if res, err := e.svc.SetEnabled(ctx, "abc234", false); err != nil || res.Backup != "" {
		t.Errorf("no-op: %+v %v", res, err)
	}

	// Undo walks back through each change, each with its own backup.
	for _, want := range []string{
		strings.Replace(afterPause, "# gocron:job id=def567 name=B\n#gocron:off 0 1 * * * b\n", "", 1),
		afterPause,
		tab,
	} {
		if _, err := e.svc.Undo(ctx); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t); got != want {
			t.Errorf("after undo:\n%s\nwant:\n%s", got, want)
		}
	}
	if n := e.backupCount(t); n != 6 {
		t.Errorf("%d backups after 3 changes and 3 undos", n)
	}
	if _, err := e.svc.Undo(ctx); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("4th undo: %v", err)
	}
}

func TestUndoFirstChangeToEmptyCrontab(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, "@daily x\n")
	if _, err := e.svc.Adopt(ctx, 1, "@daily x"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.read(t); got != "@daily x\n" {
		t.Errorf("got %q", got)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, tab)
	if _, err := e.svc.Delete(ctx, "nope00"); !errors.Is(err, crontab.ErrNotFound) {
		t.Errorf("delete unknown: %v", err)
	}
	if _, err := e.svc.Adopt(ctx, 2, "@reboot something else"); !errors.Is(err, crontab.ErrNotFound) {
		t.Errorf("adopt stale: %v", err)
	}
	if e.svc.CanUndo() || e.backupCount(t) != 0 {
		t.Error("failed changes left an undo step or a backup")
	}
}

func TestConflictRetry(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, tab)
	external := tab + "# added by hand\n"
	e.write(t, external)

	_, err := e.svc.SetEnabled(ctx, "abc234", false)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, crontab.ErrConflict) || conflict.Retry == nil {
		t.Fatalf("err = %v", err)
	}
	if e.read(t) != external {
		t.Fatal("conflicting save wrote the file")
	}
	if _, err := conflict.Retry(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.read(t); got != strings.Replace(external, "0 0 * * * a", "#gocron:off 0 0 * * * a", 1) {
		t.Errorf("retry didn't reapply onto the external change:\n%s", got)
	}
	// Undo goes back to the external version, not past it.
	if _, err := e.svc.Undo(ctx); err != nil || e.read(t) != external {
		t.Errorf("undo: %v\n%s", err, e.read(t))
	}
}

func TestUndoRefusesAfterExternalChange(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, tab)
	if _, err := e.svc.Delete(ctx, "abc234"); err != nil {
		t.Fatal(err)
	}
	external := e.read(t) + "# hand edit\n"
	e.write(t, external)

	// Not yet noticed: the safe write refuses.
	if _, err := e.svc.Undo(ctx); !errors.Is(err, crontab.ErrConflict) {
		t.Errorf("undo over an unnoticed edit: %v", err)
	}
	// Noticed: the reload drops the undo history.
	if changed, err := e.svc.CheckExternal(ctx); !changed || err != nil {
		t.Fatalf("CheckExternal = %v, %v", changed, err)
	}
	if _, err := e.svc.Undo(ctx); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo after reload: %v", err)
	}
	if e.read(t) != external {
		t.Error("the external edit was lost")
	}
	if changed, _ := e.svc.CheckExternal(ctx); changed {
		t.Error("CheckExternal reported a change twice")
	}
}

func TestConcurrentChanges(t *testing.T) {
	ctx := context.Background()
	var b strings.Builder
	for i := range 20 {
		fmt.Fprintf(&b, "# gocron:job id=job%03d\n0 0 * * * x\n", i)
	}
	e := newEnv(t, b.String())
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.SetEnabled(ctx, fmt.Sprintf("job%03d", i), false); err != nil {
				t.Error(err)
			}
			e.svc.Snapshot()
		}()
	}
	wg.Wait()
	if n := strings.Count(e.read(t), "#gocron:off "); n != 20 {
		t.Errorf("%d of 20 jobs paused", n)
	}
}
