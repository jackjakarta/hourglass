package tui

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jackjakarta/gocron/internal/config"
	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/store"
)

// startupBudget is how long gocron may take from reading the crontab to
// the first screen with jobs and their run history (spec §10, Phase 6).
const startupBudget = 150 * time.Millisecond

// bigState writes a crontab with nJobs wrapped jobs and a run history of
// runsPerJob runs each, and returns the crontab path and store options.
func bigState(tb testing.TB, nJobs, runsPerJob int) (string, store.Options) {
	tb.Helper()
	dir := tb.TempDir()
	var b strings.Builder
	b.WriteString("MAILTO=\"\"\nPATH=/usr/local/bin:/usr/bin:/bin\n")
	ids := make([]string, nJobs)
	for i := range nJobs {
		ids[i] = fmt.Sprintf("job%03d", i)
		fmt.Fprintf(&b, "# gocron:job id=%s name=\"Job number %d\" tags=perf,t%d\n", ids[i], i, i%7)
		fmt.Fprintf(&b, "%d %d * * %d /usr/local/bin/gocron exec --no-overlap %s -- 'echo %d | gzip > /tmp/x-$(date +\\%%F)'\n",
			i%60, i%24, i%7, ids[i], i)
	}
	path := filepath.Join(dir, "crontab")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		tb.Fatal(err)
	}

	opts := store.Options{Path: filepath.Join(dir, "runs.db"), LogsDir: filepath.Join(dir, "logs"), LocksDir: dir}
	db, err := store.Open(context.Background(), opts)
	if err != nil {
		tb.Fatal(err)
	}
	db.Close()
	raw, err := sql.Open("sqlite", "file:"+opts.Path)
	if err != nil {
		tb.Fatal(err)
	}
	defer raw.Close()
	tx, err := raw.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	ins, err := tx.Prepare(`INSERT INTO runs (job_id, trigger, status, started_at, ended_at, exit_code, pid, log_path, log_bytes)
		VALUES (?, 'cron', ?, ?, ?, ?, 1, ?, 100)`)
	if err != nil {
		tb.Fatal(err)
	}
	start := testNow.Add(-time.Duration(runsPerJob) * time.Hour)
	for k := range runsPerJob {
		for i, id := range ids {
			at := start.Add(time.Duration(k)*time.Hour + time.Duration(i)*time.Second)
			status, code := store.StatusSuccess, 0
			if (k+i)%9 == 0 {
				status, code = store.StatusFailed, 1
			}
			if _, err := ins.Exec(id, status, at.UnixMilli(), at.Add(3*time.Second).UnixMilli(), code,
				filepath.Join(opts.LogsDir, id, fmt.Sprint(k)+".log")); err != nil {
				tb.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	return path, opts
}

// startup does what launching gocron does before its first full screen:
// read the crontab, build the TUI, load the jobs and their run stats, and
// render.
func startup(tb testing.TB, path string, so store.Options) string {
	ctx := context.Background()
	st, err := crontab.Open(ctx, crontab.FileSource{Path: path}, crontab.Options{BackupDir: filepath.Join(filepath.Dir(path), "backups")})
	if err != nil {
		tb.Fatal(err)
	}
	svc := jobs.New(st, jobs.Options{
		Bin:      "/usr/local/bin/gocron",
		Defaults: config.Default().Defaults,
		OpenRuns: func(ctx context.Context) (*store.DB, error) { return store.Open(ctx, so) },
	})
	m := New(ctx, Options{Service: svc, Clock: fixedClock{testNow}, UI: config.Default().UI})
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	tm, _ = tm.Update(m.loadSnapshot()())
	tm, _ = tm.Update(m.loadStats()())
	return tm.(Model).render()
}

func TestStartupBudget(t *testing.T) {
	if os.Getenv("GOCRON_PERF") == "" {
		t.Skip("set GOCRON_PERF=1 to check the startup budget")
	}
	path, so := bigState(t, 100, 100)
	startup(t, path, so) // warm the file cache, like a second launch
	var best time.Duration
	for range 5 {
		start := time.Now()
		screen := startup(t, path, so)
		d := time.Since(start)
		if best == 0 || d < best {
			best = d
		}
		if !strings.Contains(screen, "Job number") || !strings.Contains(screen, "■") {
			t.Fatalf("the screen lacks jobs or run strips:\n%s", screen)
		}
	}
	t.Logf("startup with 100 jobs and 10k runs: %s (budget %s)", best, startupBudget)
	if best > startupBudget {
		t.Errorf("startup took %s, over the %s budget", best, startupBudget)
	}
}

func BenchmarkStartup(b *testing.B) {
	path, so := bigState(b, 100, 100)
	b.ResetTimer()
	for b.Loop() {
		startup(b, path, so)
	}
}
