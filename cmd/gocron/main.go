// Command gocron manages your crontab. With no arguments it opens the TUI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/jackjakarta/gocron/internal/crontab"
)

const (
	exitOK       = 0
	exitError    = 1
	exitUsage    = 2
	exitConflict = 3 // crontab changed outside gocron
)

type app struct {
	stdin          io.Reader // default os.Stdin
	stdout, stderr io.Writer
	now            func() time.Time
	source         func() (crontab.Source, error)
	interactive    func() bool   // stdin and stdout are terminals
	bin            func() string // the gocron binary written into wrapped lines; default selfPath
}

func main() {
	a := &app{stdout: os.Stdout, stderr: os.Stderr, now: time.Now, source: crontab.SourceFromEnv, interactive: func() bool {
		return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
	}}
	os.Exit(a.run(context.Background(), os.Args[1:]))
}

func (a *app) binPath() string {
	if a.bin != nil {
		return a.bin()
	}
	return selfPath()
}

const usage = `gocron — a home for your cron jobs

Usage:
  gocron                      open the TUI
  gocron list [--json] [--all]
                              list jobs (--all includes unmanaged entries)
  gocron add [--name N] --schedule S [--tag T]… [--timeout D] [--no-overlap] [--raw] -- CMD
                              add a job (quote a pipeline as one argument)
  gocron run [--cron-env] ID  run a job now, streaming its output
  gocron logs [-n N] [--run R] ID
                              show a job's recent runs, or one run's output
  gocron exec …               the wrapper cron runs tracked jobs through
  gocron export [--file F]    write the managed jobs as YAML
  gocron import [--dry-run] F import jobs from an export (- reads stdin), merging by id
  gocron describe [-n N] EXPR describe a schedule and show its next runs
  gocron doctor               check your cron setup and crontab
  gocron version              print version information

Environment:
  GOCRON_CRONTAB_FILE=PATH    use a plain file instead of your real crontab
  GOCRON_STATE_DIR=PATH       override the state directory
  GOCRON_CONFIG=PATH          override the config file
`

func (a *app) run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		if a.interactive != nil && a.interactive() {
			return a.tui(ctx)
		}
		fmt.Fprint(a.stdout, "The gocron TUI needs a terminal.\n\n"+usage)
		return exitOK
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list":
		return a.list(ctx, rest)
	case "add":
		return a.add(ctx, rest)
	case "exec":
		return a.exec(ctx, rest)
	case "run":
		return a.runJob(ctx, rest)
	case "logs":
		return a.logs(ctx, rest)
	case "export":
		return a.export(ctx, rest)
	case "import":
		return a.importJobs(ctx, rest)
	case "describe":
		return a.describe(rest)
	case "doctor":
		return a.doctor(ctx, rest)
	case "version", "--version":
		return a.version(rest)
	case "help", "-h", "--help":
		fmt.Fprint(a.stdout, usage)
		return exitOK
	}
	fmt.Fprintf(a.stderr, "gocron: unknown command %q\n\n%s", cmd, usage)
	return exitUsage
}

// flags returns a FlagSet that reports errors to stderr with a usage line.
func (a *app) flags(name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprintf(a.stderr, "Usage: gocron %s\n", synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parse parses flags, returning an exit code when the command should stop.
func parse(fs *flag.FlagSet, args []string) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK, false
		}
		return exitUsage, false
	}
	return 0, true
}

// fail prints an error and returns the matching exit code.
func (a *app) fail(err error) int {
	fmt.Fprintf(a.stderr, "gocron: %v\n", err)
	if errors.Is(err, crontab.ErrConflict) {
		return exitConflict
	}
	return exitError
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
