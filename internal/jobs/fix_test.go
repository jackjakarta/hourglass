package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFix(t *testing.T) {
	ctx := context.Background()
	initial := "# hourglass:job id=dup222 name=First\n" +
		"0 1 * * * /usr/bin/hourglass exec dup222 -- 'one'\n" +
		"# hand-written\n" +
		"# hourglass:job id=dup222 name=Second\n" +
		"0 2 * * * /old/place/hourglass exec --no-overlap dup222 -- 'two'\n" +
		"# hourglass:job id=raw333 name=Raw\n" +
		"0 3 * * * three\n"
	e := newEnv(t, initial)
	rep, err := e.svc.Fix(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.ReIDs) != 1 || rep.ReIDs[0].Old != "dup222" || rep.ReIDs[0].Name != "Second" || rep.ReIDs[0].Line != 5 {
		t.Fatalf("ReIDs = %+v", rep.ReIDs)
	}
	newID := rep.ReIDs[0].ID
	if len(rep.Rebinned) != 1 || rep.Rebinned[0].ID != newID || rep.Rebinned[0].From != "/old/place/hourglass" {
		t.Fatalf("Rebinned = %+v", rep.Rebinned)
	}
	want := strings.NewReplacer(
		"id=dup222 name=Second", "id="+newID+" name=Second",
		"/old/place/hourglass exec --no-overlap dup222", "/usr/bin/hourglass exec --no-overlap "+newID,
	).Replace(initial)
	if got := e.read(t); got != want {
		t.Errorf("crontab:\n%s\nwant:\n%s", got, want)
	}
	if n := e.backupCount(t); n != 1 {
		t.Errorf("backups = %d, want 1", n)
	}
	if len(e.svc.Snapshot().Problems) != 0 {
		t.Errorf("problems left: %v", e.svc.Snapshot().Problems)
	}

	// A second run has nothing to do and writes nothing.
	rep, err = e.svc.Fix(ctx)
	if err != nil || rep.Changed() {
		t.Fatalf("second Fix = %+v, %v", rep, err)
	}
	if n := e.backupCount(t); n != 1 {
		t.Errorf("backups after a no-op = %d", n)
	}

	// One undo step reverts the whole fix.
	if _, err := e.svc.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.read(t); got != initial {
		t.Errorf("after undo:\n%s", got)
	}
}

func TestFixWithoutUsableBinary(t *testing.T) {
	e := newEnv(t, "# hourglass:job id=abc234 name=A\n0 1 * * * /old/hourglass exec abc234 -- 'one'\n")
	e.svc.opts.Bin = filepath.Join(os.TempDir(), "go-build123", "exe", "hourglass")
	rep, err := e.svc.Fix(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Changed() || rep.BinErr == nil {
		t.Errorf("report = %+v", rep)
	}
}
