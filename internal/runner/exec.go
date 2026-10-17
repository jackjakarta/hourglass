// Package runner runs job commands the way hourglass exec does: in their own
// process group, with output captured to a capped log, a timeout, overlap
// prevention, and a row in the run history.
//
// The one rule above all others: whatever goes wrong inside hourglass (a
// locked or corrupt database, a full disk, bad arguments), the user's
// command still runs and its exit code comes back.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jackjakarta/hourglass/internal/config"
	"github.com/jackjakarta/hourglass/internal/lockfile"
	"github.com/jackjakarta/hourglass/internal/store"
)

// Exit codes Exec uses for outcomes that aren't the command's own.
const (
	ExitTimeout   = 124 // like timeout(1)
	ExitCantStart = 127
)

// DefaultKillGrace is how long a timed-out or stopped job gets between
// SIGTERM and SIGKILL.
const DefaultKillGrace = 10 * time.Second

// Options describes one run.
type Options struct {
	JobID     string
	Command   string // run with $SHELL -c
	Timeout   time.Duration
	NoOverlap bool
	Trigger   string // store.Trigger*; default cron
	Record    bool   // write a history row and a log
	// Tee receives the output as it happens (hourglass exec --tee).
	Tee io.Writer
	// Stdout is where output goes when it isn't recorded, and where
	// runs.mail_output echoes it for cron to mail. Default os.Stdout.
	Stdout io.Writer
	Env    []string // the command's environment; nil means os.Environ()
	Dir    string   // the command's working directory; "" inherits
	Paths  config.Paths
	Config config.Config
	// KillGrace is the wait between SIGTERM and SIGKILL; default 10s.
	KillGrace time.Duration
	// Signals are forwarded to the command's process group. Exec never
	// installs signal handlers itself; the CLI does and passes them in.
	Signals <-chan os.Signal
	// Started is called with the run id once the run is recorded.
	Started func(runID int64)
	Now     func() time.Time
}

// Result is how a run ended.
type Result struct {
	RunID     int64 // 0 when not recorded
	Status    string
	ExitCode  int
	Duration  time.Duration
	LogPath   string
	Truncated bool
	// Prev is the status of the job's previous finished cron run, "" when
	// there is none or the run isn't recorded.
	Prev string
}

// validID accepts what hourglass generates (6 chars of a-z2-7) and what
// people plausibly hand-edit, but nothing that could escape a directory.
var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidID reports whether id is safe to use in file names as a job id.
func ValidID(id string) bool { return validID.MatchString(id) }

// Exec runs the command and records it. It never refuses to run the
// command: problems with hourglass's own state are logged to the error log
// and the run carries on unrecorded.
func Exec(ctx context.Context, o Options) Result {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.KillGrace <= 0 {
		o.KillGrace = DefaultKillGrace
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Trigger == "" {
		o.Trigger = store.TriggerCron
	}
	if o.Config.Runs.MaxLogBytes <= 0 {
		o.Config = config.Default()
	}
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	dbCtx := context.WithoutCancel(ctx)
	r := &run{o: o, start: o.Now()}

	if o.Record && !ValidID(o.JobID) {
		r.logErr(fmt.Errorf("job id %q isn't valid; running without recording", o.JobID))
		o.Record = false
	}
	if o.Record {
		r.open(dbCtx)
	}
	defer r.close()

	if o.NoOverlap && ValidID(o.JobID) {
		l, err := r.lock()
		switch {
		case errors.Is(err, lockfile.ErrHeld):
			return r.finish(dbCtx, store.StatusSkipped, 0)
		case err != nil:
			r.logErr(fmt.Errorf("overlap lock: %w (running anyway)", err))
		}
		defer l.Release()
	}

	out := r.outputs()
	shell := Lookup(env, "SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-c", o.Command)
	cmd.Env, cmd.Dir = env, o.Dir
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A background process ("foo &") can hold the output pipe open after
	// the shell exits; don't wait on it forever.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(out, "hourglass: can't start %s: %v\n", shell, err)
		return r.finish(dbCtx, store.StatusError, ExitCantStart)
	}
	status, code := r.wait(ctx, cmd)
	return r.finish(dbCtx, status, code)
}

type run struct {
	o       Options
	start   time.Time
	db      *store.DB
	rec     store.Run
	log     *cappedWriter
	logFile *os.File
	echo    io.Writer
	prev    string // status of the previous finished cron run
}

func (r *run) logErr(err error) { LogError(r.o.Paths.ErrorLog, r.o.Now(), r.o.JobID, err) }

func (r *run) open(ctx context.Context) {
	fail := func(err error) {
		r.logErr(fmt.Errorf("run history: %w (running without recording)", err))
		if r.db != nil {
			r.db.Close()
			r.db = nil
		}
	}
	if err := r.o.Paths.EnsureStateDirs(); err != nil {
		fail(err)
		return
	}
	db, err := store.Open(ctx, store.Options{
		Path: r.o.Paths.DB, LogsDir: r.o.Paths.LogsDir, LocksDir: r.o.Paths.LocksDir, MaxConns: 1,
	})
	if err != nil {
		fail(err)
		return
	}
	r.db = db
	if r.prev, err = db.LastFinished(ctx, r.o.JobID); err != nil {
		r.logErr(fmt.Errorf("reading the previous run: %w", err))
	}
	rec, err := db.Insert(ctx, r.o.JobID, r.o.Trigger, r.start, os.Getpid())
	if err != nil {
		fail(err)
		return
	}
	r.rec = rec
	if r.o.Started != nil {
		r.o.Started(rec.ID)
	}
	if err := os.MkdirAll(filepath.Dir(rec.LogPath), 0o700); err != nil {
		r.logErr(fmt.Errorf("log directory: %w", err))
		return
	}
	f, err := os.OpenFile(rec.LogPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		r.logErr(fmt.Errorf("log file: %w", err))
		return
	}
	r.logFile = f
	r.log = &cappedWriter{w: f, limit: r.o.Config.Runs.MaxLogBytes}
}

func (r *run) close() {
	if r.logFile != nil {
		r.logFile.Close()
	}
	if r.db != nil {
		r.db.Close()
	}
}

func (r *run) lock() (*lockfile.Lock, error) {
	if err := os.MkdirAll(r.o.Paths.LocksDir, 0o700); err != nil {
		return nil, err
	}
	return lockfile.TryLock(filepath.Join(r.o.Paths.LocksDir, r.o.JobID+".lock"))
}

// outputs wires where the command's output goes: the log, the tee, and
// stdout when nothing else would keep it (or mail_output asks for it).
func (r *run) outputs() io.Writer {
	switch {
	case r.o.Tee != nil:
		r.echo = r.o.Tee
	case r.log == nil || r.o.Config.Runs.MailOutput == config.MailAlways:
		r.echo = r.o.Stdout
	}
	f := &fanout{}
	if r.log != nil {
		f.sinks = append(f.sinks, r.log)
	}
	if r.echo != nil {
		f.sinks = append(f.sinks, r.echo)
	}
	return f
}

// wait waits for the command, enforcing the timeout and forwarding
// signals. Every kill goes to the whole process group, so a pipeline or a
// script's children die with it.
func (r *run) wait(ctx context.Context, cmd *exec.Cmd) (string, int) {
	pgid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var timeout, kill <-chan time.Time
	if r.o.Timeout > 0 {
		t := time.NewTimer(r.o.Timeout)
		defer t.Stop()
		timeout = t.C
	}
	ctxDone := ctx.Done()
	timedOut, stopped := false, false
	terminate := func(sig syscall.Signal) {
		syscall.Kill(-pgid, sig)
		if kill == nil {
			kill = time.After(r.o.KillGrace)
		}
	}
	var err error
loop:
	for {
		select {
		case err = <-done:
			break loop
		case <-timeout:
			timedOut = true
			terminate(syscall.SIGTERM)
		case s := <-r.o.Signals:
			sig, ok := s.(syscall.Signal)
			if !ok {
				continue
			}
			if sig == syscall.SIGPIPE {
				continue // a closed tee; the fan-out already dropped it
			}
			stopped = true
			terminate(sig)
		case <-ctxDone:
			ctxDone = nil
			stopped = true
			terminate(syscall.SIGTERM)
		case <-kill:
			syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}
	if timedOut || stopped {
		// Stragglers that ignored SIGTERM, or outlived the shell.
		syscall.Kill(-pgid, syscall.SIGKILL)
	}

	code := 0
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		switch {
		case ws.Signaled():
			code = 128 + int(ws.Signal())
		default:
			code = ws.ExitStatus()
		}
	} else if err != nil {
		code = 1
	}
	switch {
	case timedOut:
		fmt.Fprintf(cmd.Stdout, "\n[hourglass: timed out after %s]\n", r.o.Timeout)
		return store.StatusTimeout, ExitTimeout
	case code == 0:
		return store.StatusSuccess, 0
	}
	return store.StatusFailed, code
}

func (r *run) finish(ctx context.Context, status string, code int) Result {
	end := r.o.Now()
	res := Result{RunID: r.rec.ID, Status: status, ExitCode: code, Duration: end.Sub(r.start), LogPath: r.rec.LogPath, Prev: r.prev}
	var logBytes int64
	if r.log != nil {
		logBytes, res.Truncated = r.log.total, r.log.truncated
		if res.Truncated {
			fmt.Fprintf(r.logFile, "\n[hourglass: output truncated at %d bytes; the run wrote %d]\n", r.log.limit, r.log.total)
		}
	}
	if r.o.Config.Runs.MailOutput == config.MailOnFailure && r.echo == nil && status != store.StatusSuccess && status != store.StatusSkipped {
		r.mailLog()
	}
	if r.db == nil {
		res.RunID, res.LogPath = 0, ""
		return res
	}
	exit := &code
	if status == store.StatusSkipped {
		exit = nil
	}
	if err := r.db.Finish(ctx, r.rec.ID, status, exit, end, logBytes, res.Truncated); err != nil {
		r.logErr(fmt.Errorf("recording the result: %w", err))
	}
	if _, err := r.db.Prune(ctx, r.o.JobID, r.o.Config.Runs.Retention); err != nil {
		r.logErr(fmt.Errorf("pruning old runs: %w", err))
	}
	return res
}

// mailLog copies the log to stdout, where cron picks it up for MAILTO.
func (r *run) mailLog() {
	if r.logFile == nil {
		return
	}
	f, err := os.Open(r.rec.LogPath)
	if err != nil {
		return
	}
	defer f.Close()
	io.Copy(r.o.Stdout, f)
}

// Lookup returns the last value of key in env.
func Lookup(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}

// maxErrorLog caps hourglass-errors.log; past it the log rotates once.
const maxErrorLog = 1 << 20

// LogError appends a wrapper problem to the error log. It never fails:
// there's nowhere left to report to.
func LogError(path string, now time.Time, jobID string, err error) {
	if path == "" || err == nil {
		return
	}
	if fi, statErr := os.Stat(path); statErr == nil && fi.Size() > maxErrorLog {
		os.Rename(path, path+".1")
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if openErr != nil {
		return
	}
	defer f.Close()
	if jobID == "" {
		jobID = "-"
	}
	fmt.Fprintf(f, "%s %s %v\n", now.Format(time.RFC3339), jobID, err)
}

// fanout writes to every sink, dropping a sink after its first error so a
// closed tee or a full disk never stops the others or blocks the command.
type fanout struct{ sinks []io.Writer }

func (f *fanout) Write(p []byte) (int, error) {
	kept := f.sinks[:0]
	for _, w := range f.sinks {
		if _, err := w.Write(p); err == nil {
			kept = append(kept, w)
		}
	}
	f.sinks = kept
	return len(p), nil
}

// cappedWriter writes up to limit bytes and counts the rest.
type cappedWriter struct {
	w         io.Writer
	limit     int64
	written   int64
	total     int64
	truncated bool
	failed    bool
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	c.total += int64(len(p))
	if room := c.limit - c.written; !c.failed && room > 0 {
		chunk := p
		if int64(len(chunk)) > room {
			chunk = chunk[:room]
		}
		n, err := c.w.Write(chunk)
		c.written += int64(n)
		if err != nil {
			c.failed = true // keep counting; the fan-out mustn't lose other sinks
		}
	}
	if c.total > c.limit {
		c.truncated = true
	}
	return len(p), nil
}
