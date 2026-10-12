package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackjakarta/gocron/internal/config"
	"github.com/jackjakarta/gocron/internal/notify"
	"github.com/jackjakarta/gocron/internal/runner"
	runs "github.com/jackjakarta/gocron/internal/store"
)

// helper returns a command running this test binary as gocron.
func helper(t *testing.T, state string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), envHelper+"=1", "GOCRON_STATE_DIR="+state,
		"GOCRON_CONFIG="+quietConfig(t, t.TempDir()))
	return cmd
}

func history(t *testing.T, state string) []runs.Run {
	t.Helper()
	db, err := runs.Open(context.Background(), runs.Options{
		Path: filepath.Join(state, "runs.db"), LogsDir: filepath.Join(state, "logs"), LocksDir: filepath.Join(state, "locks"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	list, err := db.List(context.Background(), runs.Filter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestExecRecordsAndPassesExitCode(t *testing.T) {
	file := filepath.Join(t.TempDir(), "crontab")
	// Hand-edited into several words, with flags exec doesn't know: it
	// still runs the command, and logs what it ignored.
	out, errOut, code := runCLI(t, file, "exec", "--timeout=soon", "--colour", "abc234", "--", "echo", "hi;", "exit", "3")
	if code != 3 || out != "" || errOut != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	state := os.Getenv("GOCRON_STATE_DIR")
	errLog, _ := os.ReadFile(filepath.Join(state, "gocron-errors.log"))
	if !strings.Contains(string(errLog), "ignoring --timeout=soon") || !strings.Contains(string(errLog), "ignoring unknown option --colour") {
		t.Errorf("error log = %q", errLog)
	}
	h := history(t, state)
	if len(h) != 1 || h[0].JobID != "abc234" || h[0].Status != runs.StatusFailed || *h[0].ExitCode != 3 || h[0].Trigger != runs.TriggerCron {
		t.Errorf("history = %+v", h)
	}
}

func TestExecUsage(t *testing.T) {
	for _, args := range [][]string{{"exec"}, {"exec", "abc234"}, {"exec", "abc234", "--"}, {"exec", "abc234", "--", " "}} {
		if _, errOut, code := runCLI(t, filepath.Join(t.TempDir(), "c"), args...); code != exitUsage || !strings.Contains(errOut, "Usage: gocron exec") {
			t.Errorf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
}

func TestExecNoRecordTee(t *testing.T) {
	out, _, code := runCLI(t, filepath.Join(t.TempDir(), "c"), "exec", "--no-record", "--tee", "-", "--", "echo streamed")
	if code != 0 || out != "streamed\n" {
		t.Errorf("code=%d stdout=%q", code, out)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("GOCRON_STATE_DIR"), "runs.db")); err == nil {
		t.Error("--no-record touched the database")
	}
}

// Ten wrappers starting at once on a fresh state dir: no locking errors,
// every run recorded.
func TestExecConcurrent(t *testing.T) {
	state := t.TempDir()
	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := helper(t, state, "exec", fmt.Sprintf("job%03d", i%3), "--", fmt.Sprintf("echo %d", i))
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("run %d: %v: %s", i, err, out)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if h := history(t, state); len(h) != n {
		t.Errorf("%d runs recorded, want %d", len(h), n)
	} else {
		for _, r := range h {
			if r.Status != runs.StatusSuccess {
				t.Errorf("run %+v", r)
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(state, "gocron-errors.log")); err == nil {
		t.Errorf("error log:\n%s", b)
	}
}

// A reader that goes away (the TUI quitting) mustn't kill exec with
// SIGPIPE: the job finishes and is recorded.
func TestExecSurvivesClosedTee(t *testing.T) {
	state := t.TempDir()
	cmd := helper(t, state, "exec", "--tee", "abc234", "--", "yes | head -c 300000; sleep 0.2; exit 4")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 10)
	io.ReadFull(out, buf)
	out.Close()
	err = cmd.Wait()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 4 {
		t.Fatalf("exec ended with %v, want exit 4", err)
	}
	h := history(t, state)
	if len(h) != 1 || h[0].Status != runs.StatusFailed || *h[0].ExitCode != 4 || h[0].LogBytes != 300000 {
		t.Errorf("history = %+v", h)
	}
}

func TestSpawnLauncher(t *testing.T) {
	state := t.TempDir()
	paths, err := config.ResolveEnv(func(k string) string {
		return map[string]string{"GOCRON_STATE_DIR": state, "GOCRON_CONFIG": filepath.Join(state, "none.toml")}[k]
	}, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	l := runner.SpawnLauncher{Bin: os.Args[0], Paths: paths}
	env := append(os.Environ(), envHelper+"=1")

	h, err := l.Launch(runner.Spec{JobID: "abc234", Record: true, Command: "echo one; echo two >&2; exit 5", Trigger: runs.TriggerManual, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(h.Output)
	res := <-h.Done
	if string(got) != "one\ntwo\n" || res.Status != runs.StatusFailed || res.ExitCode != 5 || res.RunID == 0 {
		t.Errorf("output %q, result %+v", got, res)
	}
	if hist := history(t, state); len(hist) != 1 || hist[0].ID != res.RunID || hist[0].Trigger != runs.TriggerManual {
		t.Errorf("history = %+v", hist)
	}

	// Stop ends the job through exec, which records it.
	h, err = l.Launch(runner.Spec{JobID: "abc234", Record: true, Command: "sleep 30", Trigger: runs.TriggerTest, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	h.Stop()
	io.Copy(io.Discard, h.Output)
	select {
	case res = <-h.Done:
	case <-time.After(10 * time.Second):
		t.Fatal("stop didn't end the run")
	}
	if res.Status != runs.StatusFailed || res.ExitCode != 143 {
		t.Errorf("stopped = %+v", res)
	}

	// The status pipe stays out of the job's process. The job should see
	// the same descriptors as a plain child of this process: the test may
	// itself inherit stray ones (CI runners leak some), and those pass
	// through to every child.
	base, err := exec.Command("ls", "/proc/self/fd").Output()
	if err != nil {
		t.Fatal(err)
	}
	h, _ = l.Launch(runner.Spec{Command: "ls /proc/self/fd", Trigger: runs.TriggerTest, Env: env})
	got, _ = io.ReadAll(h.Output)
	<-h.Done
	if fds, want := strings.Fields(string(got)), strings.Fields(string(base)); !slices.Equal(fds, want) {
		t.Errorf("the job's descriptors = %v, want %v", fds, want)
	}
}

func TestRunAndLogs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "crontab")
	tab := "FOO=from-crontab\n# gocron:job id=abc234 name=A\n0 0 * * * echo \"$FOO\"; echo \"$0\"; exit 2\n"
	if err := os.WriteFile(file, []byte(tab), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runCLI(t, file, "run", "abc234")
	if code != 2 || out != "from-crontab\n/bin/sh\n" || !strings.Contains(errOut, "✗ failed") || !strings.Contains(errOut, "exit 2") ||
		!strings.Contains(errOut, "gocron logs abc234 --run 1") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	state := os.Getenv("GOCRON_STATE_DIR")

	// logs reads the same state dir; runCLI would make a new one.
	logs := func(args ...string) (string, string, int) {
		var o, e bytes.Buffer
		a := &app{stdout: &o, stderr: &e, now: func() time.Time { return time.Now() }}
		t.Setenv("GOCRON_STATE_DIR", state)
		code := a.run(context.Background(), append([]string{"logs"}, args...))
		return o.String(), e.String(), code
	}
	out, errOut, code = logs("abc234")
	if code != 0 || !strings.Contains(out, "RUN") || !regexp.MustCompile(`\n1 +\S+ \d+ \S+ [\d:]+ +manual +✗ failed +2 +`).MatchString(out) {
		t.Errorf("logs: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if out, _, code = logs("--run", "last", "abc234"); code != 0 || out != "from-crontab\n/bin/sh\n" {
		t.Errorf("logs --run last: code=%d stdout=%q", code, out)
	}
	if out, _, code = logs("--run", "1", "abc234"); code != 0 || out != "from-crontab\n/bin/sh\n" {
		t.Errorf("logs --run 1: code=%d stdout=%q", code, out)
	}
	if _, errOut, code = logs("--run", "1", "zzzzzz"); code != exitError || !strings.Contains(errOut, "has no run 1") {
		t.Errorf("wrong job: code=%d stderr=%q", code, errOut)
	}
	if _, _, code = logs("--run", "x", "abc234"); code != exitUsage {
		t.Errorf("bad --run: code=%d", code)
	}
	if out, _, code = logs("nobody"); code != 0 || out != "No runs of nobody yet.\n" {
		t.Errorf("no runs: code=%d stdout=%q", code, out)
	}

	if _, errOut, code = runCLI(t, file, "run", "zzzzzz"); code != exitError || !strings.Contains(errOut, `no job with id "zzzzzz"`) {
		t.Errorf("unknown job: code=%d stderr=%q", code, errOut)
	}
}

// The acceptance check from the CLI: a command that only exists in the
// user's own bin directory fails a cron-env run, and gocron says why.
func TestRunCronEnvSuggestsPATH(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "only-in-local-bin"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	file := filepath.Join(t.TempDir(), "crontab")
	if err := os.WriteFile(file, []byte("# gocron:job id=abc234 name=A\n0 0 * * * only-in-local-bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, code := runCLI(t, file, "run", "abc234"); code != 0 || out != "ok\n" {
		t.Errorf("run now: code=%d stdout=%q", code, out)
	}
	_, errOut, code := runCLI(t, file, "run", "--cron-env", "abc234")
	if code != 127 || !strings.Contains(errOut, "only-in-local-bin is in "+bin+", which isn't on this run's PATH") {
		t.Errorf("cron-env: code=%d stderr=%q", code, errOut)
	}
}

func TestExecNotifies(t *testing.T) {
	var (
		mu       sync.Mutex
		payloads []notify.Payload
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p notify.Payload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		mu.Lock()
		payloads = append(payloads, p)
		mu.Unlock()
	}))
	defer srv.Close()

	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	fake := filepath.Join(dir, "bin")
	os.Mkdir(fake, 0o700)
	script := "#!/bin/sh\nprintf '%s|' \"$@\" >> " + calls + "\necho >> " + calls + "\n"
	if err := os.WriteFile(filepath.Join(fake, "notify-send"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[notify]\non = \"change\"\ndesktop = true\nwebhook = \""+srv.URL+"\"\n"), 0o600)
	tab := filepath.Join(dir, "crontab")
	os.WriteFile(tab, []byte("# gocron:job id=abc234 name=\"Nightly backup\"\n0 1 * * * /usr/local/bin/gocron exec abc234 -- 'x'\n"), 0o600)

	state := t.TempDir()
	run := func(command string) int {
		t.Helper()
		cmd := helper(t, state, "exec", "abc234", "--", command)
		cmd.Env = append(cmd.Env, "GOCRON_CONFIG="+cfg, "GOCRON_CRONTAB_FILE="+tab,
			"PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"))
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0
	}
	if code := run("echo boom; exit 3"); code != 3 {
		t.Fatalf("first run exit %d", code)
	}
	if code := run("exit 3"); code != 3 { // still failing: no news
		t.Fatalf("second run exit %d", code)
	}
	if code := run("echo fine"); code != 0 {
		t.Fatalf("third run exit %d", code)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 2 {
		t.Fatalf("payloads = %+v", payloads)
	}
	if p := payloads[0]; p.Job != "Nightly backup" || p.JobID != "abc234" || p.Status != "failed" || p.ExitCode != 3 ||
		p.Trigger != "cron" || p.LogTail != "boom" || p.PrevStatus != "" {
		t.Errorf("first payload = %+v", p)
	}
	if p := payloads[1]; p.Status != "success" || p.PrevStatus != "failed" || p.LogTail != "fine" {
		t.Errorf("second payload = %+v", p)
	}
	if runtime.GOOS == "linux" {
		got, _ := os.ReadFile(calls)
		want := "-a|gocron|-u|critical|gocron: Nightly backup failed|exit 3 after 0s\nboom|\n" +
			"-a|gocron|-u|normal|gocron: Nightly backup is working again|after 0s\nfine|\n"
		if string(got) != want {
			t.Errorf("notify-send calls:\n%s\nwant:\n%s", got, want)
		}
	}
}

func TestExecNotifyFailureKeepsExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[notify]\ndesktop = false\nwebhook = \""+srv.URL+"\"\n"), 0o600)
	state := t.TempDir()
	cmd := helper(t, state, "exec", "abc234", "--", "exit 7")
	cmd.Env = append(cmd.Env, "GOCRON_CONFIG="+cfg, "GOCRON_CRONTAB_FILE="+filepath.Join(dir, "missing"))
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("exec ended with %v, want exit 7", err)
	}
	errLog, _ := os.ReadFile(filepath.Join(state, "gocron-errors.log"))
	if !strings.Contains(string(errLog), "webhook notification") || !strings.Contains(string(errLog), "502") {
		t.Errorf("error log = %q", errLog)
	}
}
