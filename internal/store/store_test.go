package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 11, 14, 0, 0, 0, time.UTC)

func open(t *testing.T, alive func(int) bool) (*DB, Options) {
	t.Helper()
	dir := t.TempDir()
	opts := Options{
		Path:     filepath.Join(dir, "runs.db"),
		LogsDir:  filepath.Join(dir, "logs"),
		LocksDir: dir,
		Alive:    alive,
	}
	d, err := Open(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, opts
}

func allAlive(int) bool { return true }

func code(c int) *int { return &c }

// add records a finished run of job that started at t0+min minutes.
func add(t *testing.T, d *DB, job, status string, min int, dur time.Duration) Run {
	t.Helper()
	ctx := context.Background()
	start := t0.Add(time.Duration(min) * time.Minute)
	r, err := d.Insert(ctx, job, TriggerCron, start, 4242)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusRunning {
		var exit *int
		if status != StatusSkipped {
			exit = code(map[bool]int{true: 0, false: 1}[status == StatusSuccess])
		}
		if err := d.Finish(ctx, r.ID, status, exit, start.Add(dur), 10, false); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOpenCreatesSchemaInWAL(t *testing.T) {
	d, opts := open(t, allAlive)
	if d.Version() != SchemaVersion {
		t.Errorf("Version = %d", d.Version())
	}
	if d.JournalMode() != "wal" {
		t.Errorf("JournalMode = %q", d.JournalMode())
	}
	d.Close()
	again, err := Open(context.Background(), opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	again.Close()
}

func TestOpenNewerSchemaIsBestEffort(t *testing.T) {
	d, opts := open(t, allAlive)
	if _, err := d.db.Exec(`UPDATE schema_version SET version = 99`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err := Open(context.Background(), opts)
	if err != nil {
		t.Fatalf("a newer schema must still open: %v", err)
	}
	defer d.Close()
	if d.Version() != 99 {
		t.Errorf("Version = %d", d.Version())
	}
	if _, err := d.Insert(context.Background(), "abcdef", TriggerCron, t0, 1); err != nil {
		t.Errorf("Insert on a newer schema: %v", err)
	}
}

func TestOpenRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "runs.db")
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), Options{Path: p}); err == nil {
		t.Fatal("want an error for a directory")
	}
}

func TestInsertFinishGet(t *testing.T) {
	d, opts := open(t, allAlive)
	ctx := context.Background()
	r, err := d.Insert(ctx, "k3f9a2", TriggerManual, t0, 77)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == 0 || r.LogPath != filepath.Join(opts.LogsDir, "k3f9a2", "1.log") || r.Status != StatusRunning {
		t.Fatalf("Insert = %+v", r)
	}
	if err := d.Finish(ctx, r.ID, StatusFailed, code(3), t0.Add(90*time.Second), 2048, true); err != nil {
		t.Fatal(err)
	}
	got, err := d.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || *got.ExitCode != 3 || got.Duration(t0) != 90*time.Second ||
		got.LogBytes != 2048 || !got.Truncated || got.PID != 77 || got.Trigger != TriggerManual || !got.StartedAt.Equal(t0) {
		t.Errorf("Get = %+v", got)
	}
	if _, err := d.Get(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(999) = %v", err)
	}
	if err := d.Finish(ctx, 999, StatusSuccess, code(0), t0, 0, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("Finish(999) = %v", err)
	}
}

func TestLostRuns(t *testing.T) {
	dead := map[int]bool{4242: true}
	d, _ := open(t, func(pid int) bool { return !dead[pid] })
	r := add(t, d, "aaaaaa", StatusRunning, 0, 0)
	if r.Status != StatusLost || !r.Failed() || r.Duration(t0.Add(time.Hour)) != 0 {
		t.Errorf("dead running row = %+v", r)
	}
	delete(dead, 4242)
	if r, _ = d.Get(context.Background(), r.ID); r.Status != StatusRunning || r.Duration(t0.Add(time.Minute)) != time.Minute {
		t.Errorf("live running row = %+v", r)
	}
	if !ProcessAlive(os.Getpid()) || ProcessAlive(0) {
		t.Error("ProcessAlive is wrong about this process or pid 0")
	}
}

func TestList(t *testing.T) {
	d, _ := open(t, allAlive)
	ctx := context.Background()
	add(t, d, "aaaaaa", StatusSuccess, 0, time.Second)
	add(t, d, "bbbbbb", StatusFailed, 1, time.Second)
	add(t, d, "aaaaaa", StatusTimeout, 2, time.Second)
	m, _ := d.Insert(ctx, "aaaaaa", TriggerManual, t0.Add(3*time.Minute), 1)
	d.Finish(ctx, m.ID, StatusSuccess, code(0), t0.Add(4*time.Minute), 0, false)

	ids := func(runs []Run) (out []int64) {
		for _, r := range runs {
			out = append(out, r.ID)
		}
		return out
	}
	for name, tt := range map[string]struct {
		f    Filter
		want []int64
	}{
		"all newest first": {Filter{}, []int64{4, 3, 2, 1}},
		"one job":          {Filter{JobIDs: []string{"aaaaaa"}}, []int64{4, 3, 1}},
		"failures":         {Filter{FailuresOnly: true}, []int64{3, 2}},
		"trigger":          {Filter{Trigger: TriggerManual}, []int64{4}},
		"limit":            {Filter{Limit: 2}, []int64{4, 3}},
		"combined":         {Filter{JobIDs: []string{"aaaaaa", "bbbbbb"}, FailuresOnly: true, Trigger: TriggerCron}, []int64{3, 2}},
	} {
		t.Run(name, func(t *testing.T) {
			runs, err := d.List(ctx, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(runs); !equal(got, tt.want) {
				t.Errorf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStats(t *testing.T) {
	d, _ := open(t, allAlive)
	for i := 0; i < 25; i++ {
		status := StatusSuccess
		if i%5 == 0 {
			status = StatusFailed
		}
		add(t, d, "aaaaaa", status, i, time.Duration(i+1)*time.Second)
	}
	add(t, d, "aaaaaa", StatusSkipped, 30, 0)
	add(t, d, "aaaaaa", StatusRunning, 31, 0)
	add(t, d, "bbbbbb", StatusSuccess, 0, time.Minute)

	stats, err := d.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a := stats["aaaaaa"]
	if len(a.Recent) != RecentRuns {
		t.Fatalf("Recent = %d runs", len(a.Recent))
	}
	if !a.Recent[0].StartedAt.Before(a.Recent[19].StartedAt) {
		t.Error("Recent should be oldest first")
	}
	if a.Running == nil || !a.Running.StartedAt.Equal(t0.Add(31*time.Minute)) {
		t.Errorf("Running = %+v", a.Running)
	}
	if a.Last == nil || a.Last.Status != StatusSkipped {
		t.Errorf("Last = %+v", a.Last)
	}
	// Recent holds runs 7..24 plus the skipped and running ones; failures at 10, 15, 20.
	if a.Counted != 18 || a.OK != 15 {
		t.Errorf("Counted/OK = %d/%d", a.Counted, a.OK)
	}
	if want := 100 * 15.0 / 18; a.SuccessPct != want {
		t.Errorf("SuccessPct = %v, want %v", a.SuccessPct, want)
	}
	if want := 16500 * time.Millisecond; a.AvgDur != want { // mean of 8s..25s
		t.Errorf("AvgDur = %v, want %v", a.AvgDur, want)
	}
	if b := stats["bbbbbb"]; b.SuccessPct != 100 || b.AvgDur != time.Minute {
		t.Errorf("b = %+v", b)
	}
}

func TestPrune(t *testing.T) {
	d, _ := open(t, allAlive)
	ctx := context.Background()
	var logs []string
	running := add(t, d, "aaaaaa", StatusRunning, 0, 0)
	for i := 1; i <= 5; i++ {
		r := add(t, d, "aaaaaa", StatusSuccess, i, time.Second)
		os.MkdirAll(filepath.Dir(r.LogPath), 0o700)
		os.WriteFile(r.LogPath, []byte("x"), 0o600)
		logs = append(logs, r.LogPath)
	}
	add(t, d, "bbbbbb", StatusSuccess, 0, time.Second)

	n, err := d.Prune(ctx, "aaaaaa", 3)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("pruned %d, want 2", n)
	}
	runs, _ := d.List(ctx, Filter{JobIDs: []string{"aaaaaa"}})
	if len(runs) != 4 {
		t.Errorf("left %d runs, want 3 newest + the running one", len(runs))
	}
	if _, err := d.Get(ctx, running.ID); err != nil {
		t.Error("the running row was pruned")
	}
	for i, p := range logs {
		_, err := os.Stat(p)
		if gone := os.IsNotExist(err); gone != (i < 2) {
			t.Errorf("log %d gone = %v", i, gone)
		}
	}
	if b, _ := d.List(ctx, Filter{JobIDs: []string{"bbbbbb"}}); len(b) != 1 {
		t.Error("pruning one job touched another")
	}
}

// Separate handles contend like separate processes for the database
// (SQLite's file locks), though not for the setup flock.
func TestConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	opts := Options{Path: filepath.Join(dir, "runs.db"), LogsDir: dir, LocksDir: dir, Alive: allAlive, MaxConns: 1}
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := Open(ctx, opts)
			if err != nil {
				errs <- err
				return
			}
			defer d.Close()
			r, err := d.Insert(ctx, "aaaaaa", TriggerCron, t0, 1)
			if err == nil {
				err = d.Finish(ctx, r.ID, StatusSuccess, code(0), t0, 0, false)
			}
			if err == nil {
				_, err = d.Prune(ctx, "aaaaaa", 5)
			}
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	d, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	if err := d.db.QueryRow(`SELECT count(*) FROM runs`).Scan(&n); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if n < 5 {
		t.Errorf("%d rows left", n)
	}
}
