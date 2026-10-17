package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackjakarta/hourglass/internal/humanize"
	"github.com/jackjakarta/hourglass/internal/jobs"
	"github.com/jackjakarta/hourglass/internal/runner"
	"github.com/jackjakarta/hourglass/internal/store"
)

func (a *app) runJob(ctx context.Context, args []string) int {
	fs := a.flags("run", "run [--cron-env] ID")
	cronEnv := fs.Bool("cron-env", false, "run in the environment cron would give the job (recorded as a test)")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return exitUsage
	}
	id := fs.Arg(0)
	svc, paths, cfg, err := a.service(ctx)
	if err != nil {
		return a.fail(err)
	}
	var job *jobs.Job
	for _, j := range svc.Snapshot().Jobs {
		if j.Managed && j.ID == id {
			job = &j
			break
		}
	}
	if job == nil {
		fmt.Fprintf(a.stderr, "hourglass: no job with id %q (hourglass list shows the ids)\n", id)
		return exitError
	}
	kind := jobs.RunManual
	if *cronEnv {
		kind = jobs.RunCronEnv
	}
	spec, _ := svc.Plan(job.Job, kind)

	// The job runs in its own process group, so ^C reaches only hourglass;
	// pass it on.
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE)
	defer signal.Stop(sig)

	tail := &tailWriter{w: a.stdout, max: 16 << 10}
	res := runner.Exec(ctx, runner.Options{
		JobID: spec.JobID, Command: spec.Command, Timeout: spec.Timeout, NoOverlap: spec.NoOverlap,
		Trigger: spec.Trigger, Record: spec.Record, Tee: tail, Stdout: a.stdout,
		Env: spec.Env, Dir: spec.Dir, Paths: paths, Config: cfg, Signals: sig,
	})

	summary := fmt.Sprintf("%s · %s", res.Status, humanize.Duration(res.Duration))
	switch res.Status {
	case store.StatusSuccess:
		summary = "✓ " + summary
	case store.StatusSkipped:
		summary = "↷ skipped · the previous run is still going"
	default:
		summary = fmt.Sprintf("✗ %s · exit %d", summary, res.ExitCode)
	}
	if res.RunID != 0 {
		summary += fmt.Sprintf(" · run %d (hourglass logs %s --run %d)", res.RunID, id, res.RunID)
	}
	fmt.Fprintln(a.stderr, summary)
	for _, tip := range jobs.Diagnose(spec.Command, res, string(tail.buf), spec.Env, svc.UserEnv(), jobs.NewPathCache()) {
		fmt.Fprintf(a.stderr, "⚠ %s\n", tip)
	}
	return res.ExitCode
}

// tailWriter passes output through and keeps its last max bytes.
type tailWriter struct {
	w   io.Writer
	buf []byte
	max int
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	t.w.Write(p) // a closed stdout mustn't stop the job
	return len(p), nil
}
