package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackjakarta/hourglass/internal/config"
	"github.com/jackjakarta/hourglass/internal/cronexpr"
	"github.com/jackjakarta/hourglass/internal/crontab"
	"github.com/jackjakarta/hourglass/internal/jobs"
	runs "github.com/jackjakarta/hourglass/internal/store"
)

// tagsFlag collects repeated --tag flags; each may hold several tags
// separated by commas.
type tagsFlag []string

func (t *tagsFlag) String() string { return strings.Join(*t, ",") }

func (t *tagsFlag) Set(s string) error {
	*t = append(*t, jobs.ParseTags(s)...)
	return nil
}

const addSynopsis = `add [--name N] --schedule S [--tag T]… [--timeout D] [--no-overlap] [--raw] -- CMD

Quote a command with pipes or redirects as one argument:
  hourglass add --name backup --schedule "30 2 * * *" -- 'pg_dump app | gzip > /bk/app.sql.gz'`

func (a *app) add(ctx context.Context, args []string) int {
	fs := a.flags("add", addSynopsis)
	name := fs.String("name", "", "job name (default: the program's name)")
	schedule := fs.String("schedule", "", `cron schedule, e.g. "*/15 * * * *" or @daily`)
	var tags tagsFlag
	fs.Var(&tags, "tag", "tag the job (repeatable, or comma-separated)")
	timeout := fs.Duration("timeout", 0, "kill the job after this long, e.g. 30m (default from config)")
	noOverlap := fs.Bool("no-overlap", false, "skip a run while the previous one is still going (default from config)")
	raw := fs.Bool("raw", false, "write a plain cron line without run tracking")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	cmd := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *schedule == "" || cmd == "" {
		fs.Usage()
		return exitUsage
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *raw && (set["timeout"] || set["no-overlap"]) {
		fmt.Fprintln(a.stderr, "hourglass: --timeout and --no-overlap need run tracking, which --raw turns off")
		return exitUsage
	}
	if *timeout < 0 {
		fmt.Fprintln(a.stderr, "hourglass: --timeout can't be negative")
		return exitUsage
	}

	svc, _, cfg, err := a.service(ctx)
	if err != nil {
		return a.fail(err)
	}
	// Tracking follows the config's default; asking for a timeout or for
	// overlap prevention asks for tracking too.
	wrap := !*raw && (cfg.Defaults.Wrap || set["timeout"] || set["no-overlap"])
	if !set["timeout"] {
		*timeout = time.Duration(cfg.Defaults.Timeout)
	}
	if !set["no-overlap"] {
		*noOverlap = cfg.Defaults.NoOverlap
	}
	if wrap {
		if err := svc.WrapError(); err != nil {
			fmt.Fprintf(a.stderr, "hourglass: %v.\nInstall hourglass (go install, or a release binary), or pass --raw to add the job without run tracking.\n", err)
			return exitError
		}
	}

	d := jobs.Draft{
		Name: strings.TrimSpace(*name), Schedule: *schedule, Command: cmd, Tags: tags,
		Wrapped: wrap, Timeout: *timeout, NoOverlap: *noOverlap,
	}
	if d.Name == "" {
		d.Name = crontab.CommandName(cmd)
	}

	issues := jobs.LintDraft(d)
	issues = append(issues, jobs.LintPath(cmd, svc.Snapshot().CronEnv(0), os.Getenv("PATH"), jobs.NewPathCache())...)
	for _, is := range issues {
		switch is.Severity {
		case cronexpr.Error:
			fmt.Fprintf(a.stderr, "✗ %s\n", is.Message)
		case cronexpr.Warn:
			fmt.Fprintf(a.stderr, "⚠ %s\n", is.Message)
		default:
			fmt.Fprintf(a.stderr, "· %s\n", is.Message)
		}
	}
	if jobs.HasErrors(issues) {
		fmt.Fprintln(a.stderr, "hourglass: the job wasn't added")
		return exitError
	}

	res, err := svc.Create(ctx, d)
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.stdout, "Created job %s %q", res.ID, d.Name)
	if res.Backup != "" {
		fmt.Fprintf(a.stdout, " · backup: %s", res.Backup)
	}
	fmt.Fprintln(a.stdout)
	return exitOK
}

// setup resolves hourglass's paths and loads its config. A broken config
// falls back to the defaults with a warning, so it never blocks a command.
func (a *app) setup() (config.Paths, config.Config, error) {
	paths, err := config.Resolve()
	if err != nil {
		return paths, config.Default(), err
	}
	if err := paths.EnsureStateDirs(); err != nil {
		return paths, config.Default(), err
	}
	cfg, warnings, err := config.Load(paths.ConfigFile)
	if err != nil {
		fmt.Fprintf(a.stderr, "hourglass: %v (using the defaults)\n", err)
	}
	for _, w := range warnings {
		fmt.Fprintf(a.stderr, "hourglass: %s\n", w)
	}
	return paths, cfg, nil
}

// service opens the crontab for changes through the safe-write path.
func (a *app) service(ctx context.Context) (*jobs.Service, config.Paths, config.Config, error) {
	paths, cfg, err := a.setup()
	if err != nil {
		return nil, paths, cfg, err
	}
	src, err := a.source()
	if err != nil {
		return nil, paths, cfg, err
	}
	store, err := crontab.Open(ctx, src, crontab.Options{BackupDir: paths.BackupsDir, Keep: cfg.Backups.Keep})
	if err != nil {
		return nil, paths, cfg, err
	}
	return jobs.New(store, jobs.Options{
		Bin:      a.binPath(),
		Defaults: cfg.Defaults,
		OpenRuns: openRuns(paths),
	}), paths, cfg, nil
}

// openRuns opens the run history at paths.
func openRuns(paths config.Paths) func(context.Context) (*runs.DB, error) {
	return func(ctx context.Context) (*runs.DB, error) {
		return runs.Open(ctx, runs.Options{Path: paths.DB, LogsDir: paths.LogsDir, LocksDir: paths.LocksDir})
	}
}
