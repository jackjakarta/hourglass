package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackjakarta/gocron/internal/config"
	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/store"
)

// Spec is a run to launch on the user's behalf (run now, test runs).
type Spec struct {
	JobID     string // "" for a job with no id (an unmanaged entry, a new draft)
	Record    bool
	Command   string
	Timeout   time.Duration
	NoOverlap bool
	Trigger   string
	Env       []string // the command's full environment
	Dir       string
}

// Handle is a launched run.
type Handle struct {
	// Output streams the command's output and ends when the run does.
	// Read it to the end: an unread stream can hold the run up.
	Output io.Reader
	// Done delivers the result once, after Output ends.
	Done <-chan Result
	stop func()
}

// NewHandle returns a handle over a run's output and result; stop may be
// nil. Launchers outside this package (fakes in tests) use it.
func NewHandle(output io.Reader, done <-chan Result, stop func()) *Handle {
	return &Handle{Output: output, Done: done, stop: stop}
}

// Stop asks the run to end: SIGTERM to its process group, SIGKILL after
// the grace period.
func (h *Handle) Stop() {
	if h != nil && h.stop != nil {
		h.stop()
	}
}

// Launcher starts runs.
type Launcher interface {
	Launch(Spec) (*Handle, error)
}

// SpawnLauncher runs each job through "gocron exec" in its own session,
// so a run started from the TUI outlives it and still gets recorded.
type SpawnLauncher struct {
	Bin   string       // the gocron binary
	Paths config.Paths // passed explicitly so the run writes where the TUI reads
}

// Launch implements Launcher.
func (l SpawnLauncher) Launch(s Spec) (*Handle, error) {
	args := []string{"exec", "--trigger=" + s.Trigger, "--tee", "--status-fd=3"}
	if !s.Record {
		args = append(args, "--no-record")
	}
	if s.Timeout > 0 {
		args = append(args, "--timeout="+crontab.FormatDuration(s.Timeout))
	}
	if s.NoOverlap {
		args = append(args, "--no-overlap")
	}
	id := s.JobID
	if id == "" {
		id = "-"
	}
	args = append(args, id, "--", s.Command)

	cmd := exec.Command(l.Bin, args...)
	env := s.Env
	if env == nil {
		env = os.Environ()
	}
	env = Set(env, "GOCRON_STATE_DIR", l.Paths.StateDir)
	if l.Paths.ConfigFile != "" {
		env = Set(env, "GOCRON_CONFIG", l.Paths.ConfigFile)
	}
	cmd.Env, cmd.Dir = env, s.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	statR, statW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdout = outW
	cmd.ExtraFiles = []*os.File{statW}
	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		statR.Close()
		statW.Close()
		return nil, err
	}
	outW.Close()
	statW.Close()

	// exec says "ready" once it handles signals; a stop asked for before
	// that would kill exec itself, so it waits.
	var mu sync.Mutex
	ready, pending := false, false
	pid := cmd.Process.Pid
	kill := func() { syscall.Kill(pid, syscall.SIGTERM) } // exec forwards it to the job
	onReady := func() {
		mu.Lock()
		defer mu.Unlock()
		ready = true
		if pending {
			kill()
		}
	}
	done := make(chan Result, 1)
	go func() {
		res, ok := readStatus(statR, onReady)
		statR.Close()
		err := cmd.Wait()
		if !ok {
			// exec died before reporting; say what we can.
			res = Result{Status: store.StatusError, ExitCode: cmd.ProcessState.ExitCode()}
			if err != nil && res.ExitCode == 0 {
				res.ExitCode = 1
			}
		}
		done <- res
	}()
	return &Handle{
		Output: outR,
		Done:   done,
		stop: func() {
			mu.Lock()
			defer mu.Unlock()
			if ready {
				kill()
			} else {
				pending = true
			}
		},
	}, nil
}

// readStatus reads exec's status lines: "ready", "start <run-id>" when
// the run is recorded, then "done <status> <exit> <duration-ms>".
func readStatus(r io.Reader, onReady func()) (Result, bool) {
	var res Result
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch {
		case len(f) == 1 && f[0] == "ready":
			if onReady != nil {
				onReady()
			}
		case len(f) == 2 && f[0] == "start":
			res.RunID, _ = strconv.ParseInt(f[1], 10, 64)
		case len(f) == 4 && f[0] == "done":
			res.Status = f[1]
			res.ExitCode, _ = strconv.Atoi(f[2])
			ms, _ := strconv.ParseInt(f[3], 10, 64)
			res.Duration = time.Duration(ms) * time.Millisecond
			return res, true
		}
	}
	return res, false
}

// StatusReporter writes the status lines readStatus expects.
type StatusReporter struct{ W io.Writer }

// Ready reports that exec handles signals, so stopping it is safe.
func (s StatusReporter) Ready() {
	if s.W != nil {
		fmt.Fprintln(s.W, "ready")
	}
}

// Started reports the run id.
func (s StatusReporter) Started(id int64) {
	if s.W != nil {
		fmt.Fprintf(s.W, "start %d\n", id)
	}
}

// Done reports the result.
func (s StatusReporter) Done(r Result) {
	if s.W != nil {
		fmt.Fprintf(s.W, "done %s %d %d\n", r.Status, r.ExitCode, r.Duration.Milliseconds())
	}
}

// InProcessLauncher runs jobs with Exec in a goroutine. The runs are real
// (a real shell, a real database) but end with the process; tests use it
// where there's no gocron binary to spawn.
type InProcessLauncher struct {
	Paths     config.Paths
	Config    config.Config
	KillGrace time.Duration
}

// Launch implements Launcher.
func (l InProcessLauncher) Launch(s Spec) (*Handle, error) {
	pr, pw := io.Pipe()
	sig := make(chan os.Signal, 1)
	done := make(chan Result, 1)
	go func() {
		res := Exec(context.Background(), Options{
			JobID: s.JobID, Command: s.Command, Timeout: s.Timeout, NoOverlap: s.NoOverlap,
			Trigger: s.Trigger, Record: s.Record, Tee: pw, Env: s.Env, Dir: s.Dir,
			Paths: l.Paths, Config: l.Config, KillGrace: l.KillGrace, Signals: sig,
		})
		pw.Close()
		done <- res
	}()
	var once sync.Once
	return &Handle{Output: pr, Done: done, stop: func() {
		once.Do(func() { sig <- syscall.SIGTERM })
	}}, nil
}

// ErrNoLauncher means runs can't be started from here.
var ErrNoLauncher = errors.New("runs can't be started: no launcher")
