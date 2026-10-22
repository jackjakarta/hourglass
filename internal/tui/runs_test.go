package tui

import (
	"context"

	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackjakarta/hourglass/internal/config"
	"github.com/jackjakarta/hourglass/internal/jobs"
	"github.com/jackjakarta/hourglass/internal/runner"
	"github.com/jackjakarta/hourglass/internal/store"
)

// livePID is the pid seeded "running" rows have; the stubbed liveness
// check says it's alive, so they aren't reported lost.
const livePID = 4242

type seedRun struct {
	job, trigger, status string
	start                time.Time
	took                 time.Duration
	exit                 int
	log                  string
}

// seedHistory creates a run history for the realistic_mixed jobs, with
// times relative to testNow, and returns its opener.
func seedHistory(t *testing.T) func(context.Context) (*store.DB, error) {
	t.Helper()
	dir := t.TempDir()
	opts := store.Options{
		Path: filepath.Join(dir, "runs.db"), LogsDir: filepath.Join(dir, "logs"), LocksDir: dir,
		Alive: func(pid int) bool { return pid == livePID },
	}
	day := 24 * time.Hour
	at := func(d time.Duration, hh, mm int) time.Time {
		y, mo, dd := testNow.Add(d).Date()
		return time.Date(y, mo, dd, hh, mm, 0, 0, time.UTC)
	}
	var runs []seedRun
	for i := 19; i >= 0; i-- {
		r := seedRun{job: "k3f9a2", trigger: store.TriggerCron, status: store.StatusSuccess,
			start: at(-time.Duration(i)*day, 2, 30), took: time.Duration(60+(i*7)%30) * time.Second,
			log: "dumping app…\ndumped 3 tables (412 MB)\n"}
		if i == 5 {
			r.status, r.exit, r.log = store.StatusFailed, 1, "pg_dump: error: connection to server failed\n"
		}
		runs = append(runs, r)
	}
	for i := 19; i >= 0; i-- {
		runs = append(runs, seedRun{job: "s7nc0p", trigger: store.TriggerCron, status: store.StatusSuccess,
			start: testNow.Truncate(15 * time.Minute).Add(-time.Duration(i) * 15 * time.Minute), took: time.Duration(5+i%7) * time.Second,
			log: "sent 12 files\n"})
	}
	runs = append(runs,
		seedRun{job: "pdk4r2", trigger: store.TriggerCron, status: store.StatusSuccess, start: at(-14*day, 4, 0), took: 40 * time.Second, log: "Total reclaimed space: 1.2GB\n"},
		seedRun{job: "pdk4r2", trigger: store.TriggerCron, status: store.StatusSuccess, start: at(-7*day, 4, 0), took: 35 * time.Second, log: "Total reclaimed space: 800MB\n"},
		seedRun{job: "pdk4r2", trigger: store.TriggerCron, status: store.StatusFailed, start: at(0, 4, 0), took: time.Second, exit: 1,
			log: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock.\nIs the docker daemon running?\nretrying…\ndocker daemon still unreachable\n"},
		seedRun{job: "pdk4r2", trigger: store.TriggerTest, status: store.StatusFailed, start: at(0, 14, 0), took: 0, exit: 127,
			log: "sh: 1: docker: not found\n"},
		seedRun{job: "p7m2qd", trigger: store.TriggerManual, status: store.StatusSuccess, start: at(0, 13, 0), took: time.Second},
		seedRun{job: "r0tl6s", trigger: store.TriggerCron, status: store.StatusTimeout, start: at(0, 12, 0), took: 10 * time.Minute, exit: 124,
			log: strings.Repeat("rotating…\n", 3) + "\n[hourglass: timed out after 10m0s]\n"},
		seedRun{job: "r0tl6s", trigger: store.TriggerCron, status: store.StatusSuccess, start: at(0, 13, 0), took: 2 * time.Second},
		seedRun{job: "r0tl6s", trigger: store.TriggerCron, status: store.StatusSkipped, start: at(0, 13, 30)},
		seedRun{job: "r0tl6s", trigger: store.TriggerCron, status: store.StatusRunning, start: testNow.Add(-41 * time.Second)},
	)

	ctx := context.Background()
	db, err := store.Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, r := range runs {
		pid := 1
		if r.status == store.StatusRunning {
			pid = livePID
		}
		rec, err := db.Insert(ctx, r.job, r.trigger, r.start, pid)
		if err != nil {
			t.Fatal(err)
		}
		if r.log != "" {
			os.MkdirAll(filepath.Dir(rec.LogPath), 0o700)
			if err := os.WriteFile(rec.LogPath, []byte(r.log), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if r.status == store.StatusRunning {
			continue
		}
		var exit *int
		if r.status != store.StatusSkipped {
			exit = &r.exit
		}
		if err := db.Finish(ctx, rec.ID, r.status, exit, r.start.Add(r.took), int64(len(r.log)), false); err != nil {
			t.Fatal(err)
		}
	}
	return func(ctx context.Context) (*store.DB, error) { return store.Open(ctx, opts) }
}

// startRuns starts the TUI over realistic_mixed with a seeded history.
func startRuns(t *testing.T, w, h int, launcher runner.Launcher) *harness {
	t.Helper()
	open := seedHistory(t)
	return startFull(t, "realistic_mixed", w, h, func(o *jobs.Options) {
		o.OpenRuns = open
		o.Launcher = launcher
	}, nil)
}

func TestGoldenListRuns(t *testing.T) {
	hs := startRuns(t, 140, 32, nil)
	hs.waitFor("20/20 ok")
	hs.waitFor("running 0:41")
	requireGolden(t, hs.screen())
}

func TestGoldenDetailRuns(t *testing.T) {
	hs := startRuns(t, 100, 32, nil)
	hs.waitFor("running 0:41")
	hs.keys("down", "down", "enter") // Nightly backup
	hs.waitFor("19/20 ok")
	requireGolden(t, hs.screen())
}

func TestGoldenHistory(t *testing.T) {
	hs := startRuns(t, 140, 32, nil)
	hs.waitFor("Nightly backup")
	hs.keys("3")
	hs.waitFor("History · 49 ─")
	requireGolden(t, hs.screen())
}

func TestHistoryFilters(t *testing.T) {
	hs := startRuns(t, 140, 32, nil)
	hs.waitFor("Nightly backup")
	hs.keys("3")
	hs.waitFor("History · 49 ─")
	hs.keys("f")
	hs.waitFor("History · 4 ─") // a failed backup and prune, the prune's test, the timeout
	hs.keys("c")                // trigger: cron
	hs.waitFor("trigger: cron")
	hs.waitFor("History · 3 ─")
	hs.keys("c") // manual
	hs.waitFor("No runs match")
	hs.keys("esc")
	hs.waitFor("History · 49 ─")
	hs.keys("/", "sent 12")
	hs.waitFor("History · 20 ─") // matched on the output's first line
	hs.keys("enter", "esc")
	hs.waitFor("History · 49 ─")
	hs.screen()
}

func TestHistoryFromJob(t *testing.T) {
	hs := startRuns(t, 140, 32, nil)
	hs.waitFor("Nightly backup")
	hs.keys("down", "down", "h")
	hs.waitFor("job: Nightly backup")
	hs.waitFor("History · 20 ─")
	hs.screen()
}

func TestGoldenLogViewer(t *testing.T) {
	hs := startRuns(t, 120, 24, nil)
	hs.waitFor("Sync photos")
	hs.keys("3", "f")
	hs.waitFor("History · 4 ─")
	hs.keys("down", "down", "enter") // the failed 04:00 prune (after the test and the timeout)
	hs.waitFor("Prune docker · run")
	hs.keys("/", "docker", "enter")
	hs.waitFor("1/3 · n/N")
	hs.keys("n")
	hs.waitFor("2/3 · n/N")
	requireGolden(t, hs.screen())
}

func TestHistoryUnavailable(t *testing.T) {
	hs := start(t, "realistic_mixed", 100, 24)
	hs.waitFor("Nightly backup")
	hs.keys("3")
	hs.waitFor("Can't read the run history")
	hs.screen()
}

// fakeLauncher plays back scripted runs.
type fakeLauncher struct {
	mu     sync.Mutex
	specs  []runner.Spec
	output string
	res    runner.Result
	hold   chan struct{} // when set, runs end only when it's closed
	stops  int
}

func (f *fakeLauncher) Launch(s runner.Spec) (*runner.Handle, error) {
	f.mu.Lock()
	f.specs = append(f.specs, s)
	f.mu.Unlock()
	pr, pw := io.Pipe()
	done := make(chan runner.Result, 1)
	go func() {
		pw.Write([]byte(f.output))
		if f.hold != nil {
			<-f.hold
		}
		pw.Close()
		done <- f.res
	}()
	return runner.NewHandle(pr, done, func() {
		f.mu.Lock()
		f.stops++
		f.mu.Unlock()
	}), nil
}

func (f *fakeLauncher) spec(i int) runner.Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.specs[i]
}

func TestGoldenRunPane(t *testing.T) {
	fl := &fakeLauncher{
		output: "syncing…\n\x1b[32mphoto-001.jpg\x1b[0m\nphoto-002.jpg\n10%\r55%\r100%\nrsync: connection unexpectedly closed\n",
		res:    runner.Result{RunID: 99, Status: store.StatusFailed, ExitCode: 12, Duration: 3 * time.Second},
	}
	hs := startRuns(t, 120, 32, fl)
	hs.waitFor("Sync photos")
	hs.keys("r")
	hs.waitFor("exit 12")
	if s := fl.spec(0); s.JobID != "s7nc0p" || !s.Record || s.Trigger != store.TriggerManual || !s.NoOverlap ||
		runner.Lookup(s.Env, "SHELL") != "/bin/bash" || s.Dir != "/home/jack" {
		t.Errorf("spec = %+v", s)
	}
	requireGolden(t, hs.screen())
}

func TestRunPaneHideKeepsRunning(t *testing.T) {
	fl := &fakeLauncher{output: "working\n", hold: make(chan struct{}),
		res: runner.Result{RunID: 7, Status: store.StatusSuccess, Duration: 2 * time.Second}}
	hs := startRuns(t, 140, 32, fl)
	hs.waitFor("Sync photos")
	hs.keys("r")
	hs.waitFor("working")
	hs.keys("x")
	hs.waitFor("stopping")
	hs.keys("esc")
	hs.waitFor("keeps running")
	hs.waitFor("◐ Sync photos")
	hs.keys("r") // reopens the same run rather than starting another
	hs.waitFor("stopping")
	hs.keys("esc")
	close(fl.hold)
	hs.waitFor("Sync photos finished · 0:02")
	hs.screen()
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if len(fl.specs) != 1 || fl.stops != 1 {
		t.Errorf("%d launches, %d stops", len(fl.specs), fl.stops)
	}
}

// The acceptance check in the TUI: t on a job whose program is only in
// the user's own bin directory fails in cron's environment, the pane
// suggests the PATH fix, and c reruns it in the user's shell.
func TestCronEnvTestSuggestsPATH(t *testing.T) {
	// A short directory keeps the PATH hint on one line of the pane; macOS's
	// default temp dir is long enough to wrap it.
	bin, err := os.MkdirTemp("/tmp", "hg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(bin) })
	if err := os.WriteFile(filepath.Join(bin, "only-in-local-bin"), []byte("#!/bin/sh\necho it works\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	p := config.Paths{StateDir: state, DB: filepath.Join(state, "runs.db"), LogsDir: filepath.Join(state, "logs"),
		LocksDir: filepath.Join(state, "locks"), ErrorLog: filepath.Join(state, "errors.log")}
	home := t.TempDir()
	hs := startFull(t, "inline:# hourglass:job id=abc234 name=Local\n0 0 * * * only-in-local-bin --now\n", 120, 32, func(o *jobs.Options) {
		o.Launcher = runner.InProcessLauncher{Paths: p, Config: config.Default()}
		o.OpenRuns = func(ctx context.Context) (*store.DB, error) {
			p.EnsureStateDirs()
			return store.Open(ctx, store.Options{Path: p.DB, LogsDir: p.LogsDir, LocksDir: p.LocksDir})
		}
		o.Identity = &runner.Identity{Name: "jack", Home: home}
		o.Environ = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "SHELL=/bin/sh"}
	}, nil)
	hs.waitFor("Local")
	hs.keys("t")
	hs.waitFor("Test in cron's environment · Local")
	hs.waitFor("exit 127")
	hs.waitFor("only-in-local-bin is in " + bin)
	hs.waitFor("c rerun in your shell")
	hs.keys("c")
	hs.waitFor("Run in your shell · Local")
	hs.waitFor("it works")
	hs.waitFor("✓ success")
	hs.screen()
}

func TestEditorTestDraft(t *testing.T) {
	fl := &fakeLauncher{output: "hello from the draft\n", res: runner.Result{Status: store.StatusSuccess}}
	hs := startRuns(t, 140, 40, fl)
	hs.waitFor("Nightly backup")
	hs.keys("ctrl+t") // not in the editor: nothing
	hs.keys("n", "tab", "tab", "tab", "tab", "echo draft")
	hs.waitFor("echo draft")
	hs.keys("ctrl+t")
	hs.waitFor("Test in cron's environment · echo")
	hs.waitFor("hello from the draft")
	hs.waitFor("Not kept in history: this job isn't saved yet.")
	hs.keys("esc")
	hs.waitFor("New job")
	s := hs.screen()
	if strings.Contains(s, "Test in cron's environment") {
		t.Errorf("pane still open:\n%s", s)
	}
	if sp := fl.spec(0); sp.Record || sp.Trigger != store.TriggerTest || sp.Command != "echo draft" ||
		runner.Lookup(sp.Env, "PATH") != "/home/jack/.local/bin:/usr/local/bin:/usr/bin:/bin" || runner.Lookup(sp.Env, "SHELL") != "/bin/bash" {
		t.Errorf("spec = %+v", sp)
	}
}

func TestRunUnmanagedIsUnrecorded(t *testing.T) {
	fl := &fakeLauncher{res: runner.Result{Status: store.StatusSuccess}}
	hs := startRuns(t, 140, 32, fl)
	hs.waitFor("Nightly backup")
	hs.keys("G", "r")
	hs.waitFor("Not kept in history: it's an unmanaged entry")
	hs.screen()
	if s := fl.spec(0); s.Record || s.JobID != "" || s.Command != "~/bin/first-or-monday.sh" {
		t.Errorf("spec = %+v", s)
	}
}
