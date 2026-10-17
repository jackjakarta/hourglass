package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRevisionsRestoreUndo(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, tab)
	if revs, err := e.svc.Revisions(ctx); err != nil || len(revs) != 0 {
		t.Fatalf("revisions before any save = %v, %v", revs, err)
	}
	if _, err := e.svc.Delete(ctx, "abc234"); err != nil {
		t.Fatal(err)
	}
	afterDelete := e.read(t)
	if _, err := e.svc.SetEnabled(ctx, "def567", true); err != nil {
		t.Fatal(err)
	}
	revs, err := e.svc.Revisions(ctx)
	if err != nil || len(revs) != 2 {
		t.Fatalf("revisions = %d, %v", len(revs), err)
	}
	// Newest first: the newest backup is the crontab before the resume.
	if string(revs[0].Content) != afterDelete || string(revs[1].Content) != tab || !revs[0].At.After(revs[1].At) {
		t.Errorf("revisions out of order: %q / %q", revs[0].Content, revs[1].Content)
	}
	current := e.read(t)
	res, err := e.svc.Restore(ctx, revs[1].Path)
	if err != nil || res.Backup == "" {
		t.Fatalf("restore = %+v, %v", res, err)
	}
	if e.read(t) != tab {
		t.Errorf("after restore:\n%s", e.read(t))
	}
	if _, err := e.svc.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if e.read(t) != current {
		t.Errorf("undo didn't bring back the crontab before the restore:\n%s", e.read(t))
	}
	if _, err := e.svc.Restore(ctx, "/etc/passwd"); err == nil {
		t.Error("restoring a file that isn't a backup succeeded")
	}
}

func TestReplace(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, tab)
	base := e.svc.Snapshot().Content
	next := strings.Replace(tab, "0 0 * * * a", "5 0 * * * a", 1)
	if _, err := e.svc.Replace(ctx, base, []byte(next)); err != nil {
		t.Fatal(err)
	}
	if e.read(t) != next {
		t.Errorf("after replace:\n%s", e.read(t))
	}
	// A stale base is a conflict without a retry, and nothing is written.
	backups := e.backupCount(t)
	_, err := e.svc.Replace(ctx, base, []byte(tab))
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Retry != nil {
		t.Errorf("stale base: %v", err)
	}
	// So is an outside change since the load.
	e.write(t, next+"# by hand\n")
	_, err = e.svc.Replace(ctx, []byte(next), []byte(tab))
	if !errors.As(err, &conflict) || conflict.Retry != nil {
		t.Errorf("outside change: %v", err)
	}
	if e.read(t) != next+"# by hand\n" || e.backupCount(t) != backups {
		t.Error("a conflicting replace wrote something")
	}
	if got, err := e.svc.Disk(ctx); err != nil || string(got) != next+"# by hand\n" {
		t.Errorf("Disk = %q, %v", got, err)
	}
}

func TestValidateEdit(t *testing.T) {
	before := tab + "bogus line\n"
	tests := []struct {
		name, after string
		want        []string
	}{
		{"unchanged", before, nil},
		{"old problem moved", "bogus line\n" + tab, nil},
		{"new invalid line", tab + "bogus line\n* * * *\n", []string{"line 8: not a valid cron line"}},
		{"new orphan meta", before + "# hourglass:job id=zzz234 name=Z\n", []string{"line 8: hourglass job zzz234 has no cron line below it"}},
		{"duplicate id", before + "# hourglass:job id=abc234 name=Copy\n0 0 * * * a\n", []string{"line 8: job id abc234 is also used on line 3"}},
		{"a second copy of an old problem", before + "bogus line\n", []string{"line 8: not a valid cron line"}},
	}
	for _, tt := range tests {
		got := ValidateEdit([]byte(before), []byte(tt.after))
		if len(got) != len(tt.want) {
			t.Errorf("%s: problems = %v, want %v", tt.name, got, tt.want)
			continue
		}
		for i, p := range got {
			if !strings.HasPrefix(p.String(), tt.want[i]) {
				t.Errorf("%s: problem %q, want %q", tt.name, p, tt.want[i])
			}
		}
	}
}

func TestSummarize(t *testing.T) {
	tests := []struct {
		name, after, want string
	}{
		{"same", tab, "no changes"},
		{"job added", tab + "# hourglass:job id=zzz234 name=Z\n0 3 * * * z\n", "+1 job"},
		{"entry added", tab + "* * * * * x\n", "+1 job"},
		{"job removed", strings.Replace(tab, "# hourglass:job id=abc234 name=A\n0 0 * * * a\n", "", 1), "−1 job"},
		{"edited", strings.Replace(tab, "0 0 * * * a", "5 0 * * * a", 1), "~1 edited"},
		{"resumed", strings.Replace(tab, "#hourglass:off 0 1", "0 1", 1), "1 resumed"},
		{"renamed and paused", strings.Replace(strings.Replace(tab, "name=A", "name=AA", 1), "0 0 * * * a", "#hourglass:off 0 0 * * * a", 1), "~1 edited, 1 paused"},
		{"env changed", strings.Replace(tab, `MAILTO=""`, "MAILTO=me@x.com", 1), "2 other lines"},
		{"comment added", "# hi\n" + tab, "1 other line"},
	}
	for _, tt := range tests {
		if got := Summarize([]byte(tab), []byte(tt.after)).String(); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}
