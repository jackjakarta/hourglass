package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackjakarta/hourglass/internal/config"
	"github.com/jackjakarta/hourglass/internal/crontab"
	"github.com/jackjakarta/hourglass/internal/notify"
	"github.com/jackjakarta/hourglass/internal/runner"
	"github.com/jackjakarta/hourglass/internal/store"
	"github.com/jackjakarta/hourglass/internal/version"
)

const execSynopsis = "exec [--timeout=D] [--no-overlap] [--trigger=cron|manual|test] [--tee] ID -- COMMAND"

// exec is the wrapper cron runs tracked jobs through. It must run the
// command whatever else is wrong, so it parses its arguments leniently:
// anything it doesn't understand is logged, and the command still runs.
func (a *app) exec(ctx context.Context, args []string) int {
	// Handle signals first, so a stop that comes early reaches the job
	// instead of killing exec. Notify, not Ignore, for SIGPIPE: an ignored
	// signal would be inherited by the job, while a caught one isn't, and
	// catching it turns a closed --tee pipe into a write error instead of
	// killing exec.
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE)
	defer signal.Stop(sig)

	o := runner.Options{Trigger: store.TriggerCron, Record: true, Stdout: a.stdout, Signals: sig}
	var problems []string
	statusFD := -1
	i := 0
	for ; i < len(args) && args[i] != "--"; i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			if o.JobID == "" {
				o.JobID = arg
			} else {
				problems = append(problems, fmt.Sprintf("unexpected argument %q", arg))
			}
			continue
		}
		name, val, _ := strings.Cut(arg[2:], "=")
		switch name {
		case "timeout":
			d, err := time.ParseDuration(val)
			if err != nil || d < 0 {
				problems = append(problems, fmt.Sprintf("ignoring --timeout=%s: not a duration", val))
				continue
			}
			o.Timeout = d
		case "no-overlap":
			o.NoOverlap = true
		case "trigger":
			switch val {
			case store.TriggerCron, store.TriggerManual, store.TriggerTest:
				o.Trigger = val
			default:
				problems = append(problems, fmt.Sprintf("ignoring --trigger=%s", val))
			}
		case "tee":
			o.Tee = a.stdout
		case "no-record":
			o.Record = false
		case "status-fd":
			if n, err := strconv.Atoi(val); err == nil && n > 2 {
				statusFD = n
			}
		default:
			problems = append(problems, fmt.Sprintf("ignoring unknown option %s", arg))
		}
	}
	if i >= len(args) || strings.TrimSpace(strings.Join(args[i+1:], " ")) == "" {
		fmt.Fprintf(a.stderr, "Usage: hourglass %s\n", execSynopsis)
		return exitUsage
	}
	// A hand-edited line may split the command into several words; exec
	// joins them, as the shell would have seen them.
	o.Command = strings.Join(args[i+1:], " ")

	paths, err := config.Resolve()
	if err != nil {
		problems = append(problems, err.Error())
		o.Record = false
	} else {
		o.Paths = paths
		cfg, _, err := config.Load(paths.ConfigFile)
		if err != nil {
			problems = append(problems, "config: "+err.Error()+" (using defaults)")
		}
		o.Config = cfg
	}
	for _, p := range problems {
		runner.LogError(o.Paths.ErrorLog, time.Now(), o.JobID, fmt.Errorf("exec: %s", p))
	}

	var status runner.StatusReporter
	if statusFD > 0 {
		// The status pipe is for hourglass, not the job: keep it out of the
		// command's process.
		syscall.CloseOnExec(statusFD)
		f := os.NewFile(uintptr(statusFD), "status")
		defer f.Close()
		status.W = f
		status.Ready()
		o.Started = status.Started
	}

	res := runner.Exec(ctx, o)
	status.Done(res)
	a.notify(ctx, o, res)
	return res.ExitCode
}

// notify sends the notifications config.toml asks for when a cron run
// ends. Manual and test runs are watched as they happen, so they don't
// notify. Failures are logged; they never change the job's exit code.
func (a *app) notify(ctx context.Context, o runner.Options, res runner.Result) {
	n := o.Config.Notify
	if o.Trigger != store.TriggerCron || (!n.Desktop && n.Webhook == "") || !notify.Should(n.On, res.Status, res.Prev) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	e := notify.Event{
		JobID:     o.JobID,
		Name:      a.jobName(ctx, o.JobID),
		Status:    res.Status,
		ExitCode:  res.ExitCode,
		Duration:  res.Duration,
		Trigger:   o.Trigger,
		StartedAt: time.Now().Add(-res.Duration),
		Prev:      res.Prev,
	}
	e.Host, _ = os.Hostname()
	var err error
	if e.LogTail, err = notify.Tail(res.LogPath, 20, 4<<10); err != nil {
		runner.LogError(o.Paths.ErrorLog, time.Now(), o.JobID, fmt.Errorf("notify: reading the log: %w", err))
	}
	s := notify.Sender{Desktop: n.Desktop, Webhook: n.Webhook, UserAgent: "hourglass/" + version.Get().Version}
	for _, err := range s.Send(ctx, e) {
		runner.LogError(o.Paths.ErrorLog, time.Now(), o.JobID, err)
	}
}

// notifyTimeout bounds all of a run's notifications together.
const notifyTimeout = 10 * time.Second

// jobName is the name of the managed job id, or id when the crontab can't
// be read or the job has no name.
func (a *app) jobName(ctx context.Context, id string) string {
	src, err := a.source()
	if err != nil {
		return id
	}
	b, err := src.Read(ctx)
	if err != nil {
		return id
	}
	if j, ok := crontab.Parse(b).Job(id); ok && j.Name != "" {
		return j.Name
	}
	return id
}
