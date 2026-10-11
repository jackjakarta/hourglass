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

	"github.com/jackjakarta/gocron/internal/config"
	"github.com/jackjakarta/gocron/internal/runner"
	"github.com/jackjakarta/gocron/internal/store"
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
		fmt.Fprintf(a.stderr, "Usage: gocron %s\n", execSynopsis)
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
		// The status pipe is for gocron, not the job: keep it out of the
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
	return res.ExitCode
}
