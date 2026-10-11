package runner

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackjakarta/gocron/internal/config"
	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/lockfile"
	"github.com/jackjakarta/gocron/internal/store"
)

const job = "k3f9a2"

func paths(t *testing.T) config.Paths {
	t.Helper()
	t.Setenv("GOCRON_STATE_DIR", t.TempDir())
	t.Setenv("GOCRON_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
	p, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func runs(t *testing.T, p config.Paths) []store.Run {
	t.Helper()
	p.EnsureStateDirs()
	db, err := store.Open(context.Background(), store.Options{Path: p.DB, LogsDir: p.LogsDir, LocksDir: p.LocksDir})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rs, err := db.List(context.Background(), store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func execOpts(p config.Paths, cmd string) Options {
	return Options{JobID: job, Command: cmd, Record: true, Paths: p, Config: config.Default(), Stdout: io.Discard}
}

func TestExecSuccessAndFailure(t *testing.T) {
	p := paths(t)
	res := Exec(context.Background(), execOpts(p, `echo out; echo err >&2; printf '100%%'`))
	if res.Status != store.StatusSuccess || res.ExitCode != 0 || res.RunID == 0 {
		t.Fatalf("success = %+v", res)
	}
	if got := readLog(t, res.LogPath); got != "out\nerr\n100%" {
		t.Errorf("log = %q", got)
	}

	res = Exec(context.Background(), execOpts(p, `echo boom | tr a-z A-Z; exit 3`))
	if res.Status != store.StatusFailed || res.ExitCode != 3 {
		t.Fatalf("failure = %+v", res)
	}
	if got := readLog(t, res.LogPath); got != "BOOM\n" {
		t.Errorf("log = %q", got)
	}

	rs := runs(t, p)
	if len(rs) != 2 || rs[0].Status != store.StatusFailed || *rs[0].ExitCode != 3 || rs[0].LogBytes != 5 ||
		rs[1].Status != store.StatusSuccess || rs[1].Trigger != store.TriggerCron || rs[1].PID != os.Getpid() {
		t.Errorf("runs = %+v", rs)
	}
}

func TestExecKilledBySignal(t *testing.T) {
	res := Exec(context.Background(), execOpts(paths(t), `kill -USR1 $$`))
	if res.Status != store.StatusFailed || res.ExitCode != 128+int(syscall.SIGUSR1) {
		t.Errorf("res = %+v", res)
	}
}

func TestExecTeeAndUsesShellFromEnv(t *testing.T) {
	p := paths(t)
	var tee bytes.Buffer
	o := execOpts(p, `echo "$0 $FOO"`)
	o.Tee = &tee
	o.Env = []string{"SHELL=/bin/sh", "FOO=bar", "PATH=" + DefaultCronPATH}
	o.Dir = t.TempDir()
	res := Exec(context.Background(), o)
	if tee.String() != "/bin/sh bar\n" || readLog(t, res.LogPath) != tee.String() {
		t.Errorf("tee = %q, log = %q", tee.String(), readLog(t, res.LogPath))
	}
	o.Command = "pwd"
	tee.Reset()
	Exec(context.Background(), o)
	if strings.TrimSpace(tee.String()) != o.Dir {
		t.Errorf("pwd = %q, want %q", tee.String(), o.Dir)
	}
}

func TestExecCantStart(t *testing.T) {
	o := execOpts(paths(t), "true")
	o.Env = []string{"SHELL=/nonexistent/shell"}
	res := Exec(context.Background(), o)
	if res.Status != store.StatusError || res.ExitCode != ExitCantStart {
		t.Errorf("res = %+v", res)
	}
	if !strings.Contains(readLog(t, res.LogPath), "can't start /nonexistent/shell") {
		t.Errorf("log = %q", readLog(t, res.LogPath))
	}
}

// The timeout kills the whole process group, escalating to SIGKILL for a
// grandchild that ignores SIGTERM.
func TestExecTimeoutKillsGroup(t *testing.T) {
	p := paths(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	o := execOpts(p, `trap '' TERM; sleep 30 & echo $! > `+pidFile+`; wait`)
	o.Timeout = 300 * time.Millisecond
	o.KillGrace = 200 * time.Millisecond
	start := time.Now()
	res := Exec(context.Background(), o)
	if res.Status != store.StatusTimeout || res.ExitCode != ExitTimeout {
		t.Fatalf("res = %+v", res)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %v", took)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	deadline := time.Now().Add(3 * time.Second)
	for store.ProcessAlive(pid) && !zombie(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived the timeout", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(readLog(t, res.LogPath), "timed out after 300ms") {
		t.Errorf("log = %q", readLog(t, res.LogPath))
	}
	if rs := runs(t, p); rs[0].Status != store.StatusTimeout || *rs[0].ExitCode != ExitTimeout {
		t.Errorf("run = %+v", rs[0])
	}
}

// zombie reports whether pid has exited but not been reaped (by init, for
// an orphaned grandchild).
func zombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i > 0 && i+2 < len(s) && s[i+2] == 'Z'
}

func TestExecForwardsSignals(t *testing.T) {
	sig := make(chan os.Signal, 1)
	o := execOpts(paths(t), `sleep 30`)
	o.Signals = sig
	o.KillGrace = time.Second
	go func() {
		time.Sleep(100 * time.Millisecond)
		sig <- syscall.SIGTERM
	}()
	res := Exec(context.Background(), o)
	if res.Status != store.StatusFailed || res.ExitCode != 128+int(syscall.SIGTERM) {
		t.Errorf("res = %+v", res)
	}
}

func TestExecNoOverlap(t *testing.T) {
	p := paths(t)
	if err := p.EnsureStateDirs(); err != nil {
		t.Fatal(err)
	}
	l, err := lockfile.TryLock(filepath.Join(p.LocksDir, job+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	o := execOpts(p, "touch "+marker)
	o.NoOverlap = true
	res := Exec(context.Background(), o)
	if res.Status != store.StatusSkipped || res.ExitCode != 0 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a skipped run ran the command")
	}
	if rs := runs(t, p); rs[0].Status != store.StatusSkipped || rs[0].ExitCode != nil {
		t.Errorf("run = %+v", rs[0])
	}
	l.Release()
	if res := Exec(context.Background(), o); res.Status != store.StatusSuccess {
		t.Errorf("after release = %+v", res)
	}
}

func TestExecCapsLog(t *testing.T) {
	p := paths(t)
	o := execOpts(p, `head -c 5000 /dev/zero | tr '\0' x`)
	o.Config.Runs.MaxLogBytes = 1024
	res := Exec(context.Background(), o)
	if !res.Truncated {
		t.Fatal("not truncated")
	}
	got := readLog(t, res.LogPath)
	if !strings.HasPrefix(got, strings.Repeat("x", 1024)+"\n[gocron: output truncated at 1024 bytes; the run wrote 5000]") {
		t.Errorf("log = %q", got)
	}
	if rs := runs(t, p); !rs[0].Truncated || rs[0].LogBytes != 5000 {
		t.Errorf("run = %+v", rs[0])
	}
}

func TestExecPrunesByRetention(t *testing.T) {
	p := paths(t)
	o := execOpts(p, "true")
	o.Config.Runs.Retention = 3
	var first Result
	for i := 0; i < 5; i++ {
		res := Exec(context.Background(), o)
		if i == 0 {
			first = res
		}
	}
	if rs := runs(t, p); len(rs) != 3 {
		t.Errorf("%d runs kept, want 3", len(rs))
	}
	if _, err := os.Stat(first.LogPath); !os.IsNotExist(err) {
		t.Error("the pruned run's log is still there")
	}
}

// With the database unusable, the command still runs, its output goes to
// stdout like plain cron, its exit code passes through, and the problem
// lands in the error log.
func TestExecDatabaseUnavailable(t *testing.T) {
	p := paths(t)
	if err := p.EnsureStateDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p.DB, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	o := execOpts(p, "echo still here; exit 7")
	o.Stdout = &stdout
	res := Exec(context.Background(), o)
	if res.ExitCode != 7 || res.Status != store.StatusFailed || res.RunID != 0 {
		t.Errorf("res = %+v", res)
	}
	if stdout.String() != "still here\n" {
		t.Errorf("stdout = %q", stdout.String())
	}
	if log := readLog(t, p.ErrorLog); !strings.Contains(log, job+" run history:") {
		t.Errorf("error log = %q", log)
	}
}

func TestExecInvalidIDRunsUnrecorded(t *testing.T) {
	p := paths(t)
	var stdout bytes.Buffer
	o := execOpts(p, "echo hi")
	o.JobID, o.Stdout = "../../etc", &stdout
	if res := Exec(context.Background(), o); res.Status != store.StatusSuccess || res.RunID != 0 || stdout.String() != "hi\n" {
		t.Errorf("res = %+v, stdout = %q", res, stdout.String())
	}
	if _, err := os.Stat(p.DB); err == nil {
		t.Error("an invalid id must not touch the database")
	}
}

func TestExecMailOutput(t *testing.T) {
	for _, tt := range []struct {
		mode, cmd, want string
	}{
		{config.MailNever, "echo a; exit 1", ""},
		{config.MailAlways, "echo a", "a\n"},
		{config.MailOnFailure, "echo a", ""},
		{config.MailOnFailure, "echo a; exit 1", "a\n"},
	} {
		var stdout bytes.Buffer
		o := execOpts(paths(t), tt.cmd)
		o.Config.Runs.MailOutput, o.Stdout = tt.mode, &stdout
		Exec(context.Background(), o)
		if stdout.String() != tt.want {
			t.Errorf("%s %q: stdout = %q, want %q", tt.mode, tt.cmd, stdout.String(), tt.want)
		}
	}
}

func TestErrorLogRotates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "errors.log")
	os.WriteFile(p, bytes.Repeat([]byte("x"), maxErrorLog+1), 0o600)
	LogError(p, time.Now(), job, io.EOF)
	if b, _ := os.ReadFile(p); !strings.HasSuffix(string(b), job+" EOF\n") || len(b) > 100 {
		t.Errorf("log = %d bytes", len(b))
	}
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Error("no rotated log")
	}
}

func TestFanoutDropsFailedSink(t *testing.T) {
	var a bytes.Buffer
	pr, pw := io.Pipe()
	pr.Close()
	f := &fanout{sinks: []io.Writer{pw, &a}}
	for i := 0; i < 2; i++ {
		if n, err := f.Write([]byte("hi")); n != 2 || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if a.String() != "hihi" || len(f.sinks) != 1 {
		t.Errorf("a = %q, sinks = %d", a.String(), len(f.sinks))
	}
}

func TestCronEnv(t *testing.T) {
	env := CronEnv(Identity{Name: "jack", Home: "/home/jack"}, []crontab.EnvVar{
		{Name: "PATH", Value: "/opt/bin:/usr/bin"}, {Name: "MAILTO", Value: ""}, {Name: "PATH", Value: "/x"},
	})
	want := []string{"HOME=/home/jack", "LOGNAME=jack", "USER=jack", "SHELL=/bin/sh", "MAILTO=", "PATH=/x"}
	if strings.Join(env, "|") != strings.Join(want, "|") {
		t.Errorf("env = %q", env)
	}
	if Lookup(env, "PATH") != "/x" || Lookup(env, "NOPE") != "" {
		t.Error("Lookup")
	}
	m := ManualEnv([]string{"SHELL=/bin/zsh", "PATH=/home/jack/bin:/usr/bin"}, []crontab.EnvVar{{Name: "FOO", Value: "1"}})
	if Lookup(m, "SHELL") != "/bin/sh" || Lookup(m, "PATH") != "/home/jack/bin:/usr/bin" || Lookup(m, "FOO") != "1" {
		t.Errorf("manual env = %q", m)
	}
}

func TestInProcessLauncher(t *testing.T) {
	p := paths(t)
	l := InProcessLauncher{Paths: p, Config: config.Default(), KillGrace: time.Second}
	h, err := l.Launch(Spec{JobID: job, Record: true, Command: "echo one; echo two", Trigger: store.TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(h.Output)
	res := <-h.Done
	if string(out) != "one\ntwo\n" || res.Status != store.StatusSuccess || res.RunID == 0 {
		t.Errorf("out = %q, res = %+v", out, res)
	}
	if rs := runs(t, p); rs[0].Trigger != store.TriggerManual {
		t.Errorf("trigger = %q", rs[0].Trigger)
	}

	h, _ = l.Launch(Spec{Command: "sleep 30", Trigger: store.TriggerTest})
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.Stop()
		h.Stop() // twice is fine
	}()
	io.Copy(io.Discard, h.Output)
	if res := <-h.Done; res.Status != store.StatusFailed || res.RunID != 0 {
		t.Errorf("stopped = %+v", res)
	}
}

func TestReadStatus(t *testing.T) {
	var b bytes.Buffer
	s := StatusReporter{W: &b}
	s.Ready()
	s.Started(42)
	s.Done(Result{Status: store.StatusTimeout, ExitCode: 124, Duration: 1500 * time.Millisecond})
	ready := false
	res, ok := readStatus(&b, func() { ready = true })
	if !ok || !ready || res.RunID != 42 || res.Status != store.StatusTimeout || res.ExitCode != 124 || res.Duration != 1500*time.Millisecond {
		t.Errorf("readStatus = %+v, %v", res, ok)
	}
	if _, ok := readStatus(strings.NewReader("start 1\n"), nil); ok {
		t.Error("a missing done line must not be ok")
	}
}
