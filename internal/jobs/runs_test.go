package jobs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackjakarta/hourglass/internal/config"
	"github.com/jackjakarta/hourglass/internal/crontab"
	"github.com/jackjakarta/hourglass/internal/runner"
	"github.com/jackjakarta/hourglass/internal/store"
)

func TestCheckBin(t *testing.T) {
	for bin, ok := range map[string]bool{
		"/usr/local/bin/hourglass":     true,
		"/home/u/go/bin/hourglass":     true,
		"":                             false,
		"hourglass":                    false,
		"/usr/local/bin/hourglass-dev": false,
		filepath.Join(os.TempDir(), "go-build1/exe/hourglass"): false,
	} {
		if err := CheckBin(bin); (err == nil) != ok {
			t.Errorf("CheckBin(%q) = %v", bin, err)
		}
	}
}

func TestCreateWrapsOnlyWithAStableBinary(t *testing.T) {
	e := newEnv(t, "")
	res, err := e.svc.Create(context.Background(), Draft{Name: "x", Schedule: "@daily", Command: "echo hi", Wrapped: true, NoOverlap: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := "# hourglass:job id=" + res.ID + " name=x\n@daily /usr/bin/hourglass exec --no-overlap " + res.ID + " -- 'echo hi'\n"; e.read(t) != want {
		t.Errorf("crontab:\n%s", e.read(t))
	}

	cstore, _ := crontab.Open(context.Background(), crontab.FileSource{Path: e.path}, crontab.Options{BackupDir: e.backups})
	bad := New(cstore, Options{Bin: filepath.Join(os.TempDir(), "go-build1/exe/hourglass")})
	if bad.WrapError() == nil {
		t.Fatal("a temporary build should not be wrappable")
	}
	if _, err := bad.Create(context.Background(), Draft{Name: "y", Schedule: "@daily", Command: "x", Wrapped: true}); err == nil {
		t.Error("Create wrapped with a temporary build should fail")
	}
}

func TestAdoptFollowsDefaults(t *testing.T) {
	for _, wrap := range []bool{true, false} {
		e := newEnv(t, "@daily x\n")
		cstore, _ := crontab.Open(context.Background(), crontab.FileSource{Path: e.path}, crontab.Options{BackupDir: e.backups})
		svc := New(cstore, Options{Bin: "/usr/bin/hourglass", Defaults: config.Defaults{Wrap: wrap}})
		res, err := svc.Adopt(context.Background(), 1, "@daily x")
		if err != nil {
			t.Fatal(err)
		}
		if j := reloadedJobOf(t, svc, res.ID); j.Wrapped != wrap {
			t.Errorf("wrap=%v: adopted job wrapped = %v", wrap, j.Wrapped)
		}
	}
}

func reloadedJobOf(t *testing.T, svc *Service, id string) crontab.Job {
	t.Helper()
	for _, j := range svc.Snapshot().Jobs {
		if j.ID == id {
			return j.Job
		}
	}
	t.Fatalf("no job %s", id)
	return crontab.Job{}
}

func TestEnvAt(t *testing.T) {
	e := newEnv(t, "PATH=/a\n0 0 * * * x\nSHELL=/bin/bash\n0 0 * * * y\n")
	snap := e.svc.Snapshot()
	if got := snap.EnvAt(2); len(got) != 1 || got[0].Value != "/a" {
		t.Errorf("EnvAt(2) = %v", got)
	}
	if got := snap.EnvAt(0); len(got) != 2 || got[1].Name != "SHELL" {
		t.Errorf("EnvAt(0) = %v", got)
	}
}

// recorder captures the specs it's asked to launch.
type recorder struct{ specs []runner.Spec }

func (r *recorder) Launch(s runner.Spec) (*runner.Handle, error) {
	r.specs = append(r.specs, s)
	done := make(chan runner.Result, 1)
	done <- runner.Result{Status: store.StatusSuccess}
	return &runner.Handle{Output: strings.NewReader(""), Done: done}, nil
}

func TestLaunchSpecs(t *testing.T) {
	e := newEnv(t, "PATH=/opt/bin:/usr/bin\nFOO=1\n# hourglass:job id=abc234 name=A\n0 0 * * * /usr/bin/hourglass exec --timeout=5m --no-overlap abc234 -- 'a'\n0 0 * * * b\n")
	rec := &recorder{}
	cstore, _ := crontab.Open(context.Background(), crontab.FileSource{Path: e.path}, crontab.Options{BackupDir: e.backups})
	svc := New(cstore, Options{
		Bin:      "/usr/bin/hourglass",
		Launcher: rec,
		Identity: &runner.Identity{Name: "jack", Home: "/home/jack"},
		Environ:  []string{"PATH=/home/jack/bin:/usr/bin", "SHELL=/bin/zsh", "TERM=xterm"},
	})
	jobs := svc.Snapshot().Jobs
	managed, unmanaged := jobs[0].Job, jobs[1].Job

	l, err := svc.Launch(managed, RunCronEnv)
	if err != nil {
		t.Fatal(err)
	}
	s := rec.specs[0]
	if s.JobID != "abc234" || !s.Record || s.Command != "a" || s.Timeout != 5*time.Minute || !s.NoOverlap ||
		s.Trigger != store.TriggerTest || s.Dir != "/home/jack" {
		t.Errorf("cron-env spec = %+v", s)
	}
	if strings.Join(s.Env, "|") != "HOME=/home/jack|LOGNAME=jack|USER=jack|SHELL=/bin/sh|PATH=/opt/bin:/usr/bin|FOO=1" {
		t.Errorf("cron env = %q", s.Env)
	}
	if strings.Join(l.CronEnv, "|") != strings.Join(s.Env, "|") {
		t.Errorf("Launched.CronEnv = %q", l.CronEnv)
	}

	svc.Launch(unmanaged, RunManual)
	s = rec.specs[1]
	if s.Record || s.JobID != "" || s.Trigger != store.TriggerManual || s.Timeout != 0 {
		t.Errorf("manual spec = %+v", s)
	}
	if runner.Lookup(s.Env, "SHELL") != "/bin/sh" || runner.Lookup(s.Env, "PATH") != "/opt/bin:/usr/bin" || runner.Lookup(s.Env, "TERM") != "xterm" {
		t.Errorf("manual env = %q", s.Env)
	}

	svc.Launch(managed, RunUserEnv)
	if s = rec.specs[2]; runner.Lookup(s.Env, "SHELL") != "/bin/zsh" || runner.Lookup(s.Env, "PATH") != "/home/jack/bin:/usr/bin" {
		t.Errorf("user env = %q", s.Env)
	}

	if _, err := New(cstore, Options{}).Launch(managed, RunManual); err == nil {
		t.Error("Launch without a launcher should fail")
	}
}

// The acceptance check: a command that only exists in the user's own bin
// directory fails a cron-environment test, and Diagnose points at PATH.
func TestCronEnvTestOfLocalBinCommand(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "only-in-local-bin"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	p := config.Paths{StateDir: state, DB: filepath.Join(state, "runs.db"), LogsDir: filepath.Join(state, "logs"),
		LocksDir: filepath.Join(state, "locks"), ErrorLog: filepath.Join(state, "errors.log")}
	e := newEnv(t, "# hourglass:job id=abc234 name=A\n0 0 * * * only-in-local-bin --now\n")
	cstore, _ := crontab.Open(context.Background(), crontab.FileSource{Path: e.path}, crontab.Options{BackupDir: e.backups})
	userEnv := []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir()}
	svc := New(cstore, Options{
		Bin:      "/usr/bin/hourglass",
		Launcher: runner.InProcessLauncher{Paths: p, Config: config.Default()},
		Identity: &runner.Identity{Name: "u", Home: t.TempDir()},
		Environ:  userEnv,
	})
	j := svc.Snapshot().Jobs[0].Job

	l, err := svc.Launch(j, RunCronEnv)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(l.Output)
	res := <-l.Done
	if res.Status != store.StatusFailed || res.ExitCode != 127 {
		t.Fatalf("cron-env test = %+v, output %q", res, out)
	}
	tips := Diagnose(j.Command, res, string(out), l.Spec.Env, svc.UserEnv(), NewPathCache())
	want := "only-in-local-bin is in " + bin + ", which isn't on this run's PATH (/usr/bin:/bin)"
	if len(tips) == 0 || !strings.HasPrefix(tips[0], want) {
		t.Errorf("tips = %q, want one starting %q", tips, want)
	}

	// Run now keeps the user's PATH when the crontab sets none, so it works.
	l, _ = svc.Launch(j, RunManual)
	io.Copy(io.Discard, l.Output)
	if res := <-l.Done; res.Status != store.StatusSuccess {
		t.Errorf("manual run = %+v", res)
	}
	l, _ = svc.Launch(j, RunUserEnv)
	io.Copy(io.Discard, l.Output)
	if res := <-l.Done; res.Status != store.StatusSuccess {
		t.Errorf("user-env run = %+v", res)
	}
}

func TestDiagnose(t *testing.T) {
	failed := runner.Result{Status: store.StatusFailed, ExitCode: 1}
	notFound := runner.Result{Status: store.StatusFailed, ExitCode: 127}
	cron := []string{"PATH=/usr/bin:/bin", "SHELL=/bin/sh", "HOME=/h"}
	user := []string{"PATH=/nope:/usr/bin", "API_KEY=x", "SSH_AUTH_SOCK=/s", "DISPLAY=:0"}
	tests := []struct {
		name, cmd string
		res       runner.Result
		output    string
		want      string // a substring of some tip; "" means no tips
	}{
		{"success", "foo", runner.Result{Status: store.StatusSuccess}, "", ""},
		{"skipped", "foo", runner.Result{Status: store.StatusSkipped}, "", ""},
		{"missing everywhere", "nosuchprog123 --x", notFound, "", "nosuchprog123 isn't on this run's PATH or yours"},
		{"second in pipeline", "true | nosuchprog123", notFound, "", "nosuchprog123 isn't"},
		{"output says not found", "nosuchprog123", failed, "sh: 1: nosuchprog123: not found", "nosuchprog123 isn't"},
		{"env var", `curl -H "$API_KEY" x`, failed, "", "$API_KEY is set in your shell but not here"},
		{"assigned var", `API_KEY=1 curl -H "$API_KEY" x`, failed, "", ""},
		{"single-quoted var", `awk '{print $API_KEY}'`, failed, "", ""},
		{"desktop", "notify-send hi", failed, "", "DISPLAY and DBUS_SESSION_BUS_ADDRESS"},
		{"ssh agent", "rsync -a x host:y", failed, "", "ssh-agent"},
		{"bashism", "[[ -f x ]] && echo y", failed, "", "[[ is bash syntax"},
		{"brace", "cp x{,.bak}", failed, "", "{,.bak} is bash syntax"},
		{"awk braces are fine", "awk '{print $1,$2}' f", failed, "", ""},
		{"permission", "./x.sh", runner.Result{Status: store.StatusFailed, ExitCode: 126}, "", "isn't executable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tips := Diagnose(tt.cmd, tt.res, tt.output, cron, user, NewPathCache())
			if tt.want == "" {
				if len(tips) > 0 {
					t.Errorf("tips = %q, want none", tips)
				}
				return
			}
			for _, tip := range tips {
				if strings.Contains(tip, tt.want) {
					return
				}
			}
			t.Errorf("tips = %q, want one containing %q", tips, tt.want)
		})
	}
}
