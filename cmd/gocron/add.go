package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackjakarta/gocron/internal/config"
	"github.com/jackjakarta/gocron/internal/cronexpr"
	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/jobs"
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
  gocron add --name backup --schedule "30 2 * * *" -- 'pg_dump app | gzip > /bk/app.sql.gz'`

func (a *app) add(ctx context.Context, args []string) int {
	fs := a.flags("add", addSynopsis)
	name := fs.String("name", "", "job name (default: the program's name)")
	schedule := fs.String("schedule", "", `cron schedule, e.g. "*/15 * * * *" or @daily`)
	var tags tagsFlag
	fs.Var(&tags, "tag", "tag the job (repeatable, or comma-separated)")
	timeout := fs.Duration("timeout", 0, "kill the job after this long, e.g. 30m (needs run tracking)")
	noOverlap := fs.Bool("no-overlap", false, "skip a run while the previous one is still going (needs run tracking)")
	raw := fs.Bool("raw", false, "write a plain cron line without run tracking")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	cmd := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *schedule == "" || cmd == "" {
		fs.Usage()
		return exitUsage
	}
	wrap := jobs.WrapNew && !*raw
	if (*timeout != 0 || *noOverlap) && !wrap {
		reason := "they need run tracking, which arrives in a later release"
		if jobs.WrapNew {
			reason = "they need run tracking, which --raw turns off"
		}
		fmt.Fprintf(a.stderr, "gocron: --timeout and --no-overlap can't be used: %s\n", reason)
		return exitUsage
	}
	if *timeout < 0 {
		fmt.Fprintln(a.stderr, "gocron: --timeout can't be negative")
		return exitUsage
	}

	d := jobs.Draft{
		Name: strings.TrimSpace(*name), Schedule: *schedule, Command: cmd, Tags: tags,
		Wrapped: wrap, Timeout: *timeout, NoOverlap: *noOverlap,
	}
	if d.Name == "" {
		d.Name = crontab.CommandName(cmd)
	}

	svc, err := a.service(ctx)
	if err != nil {
		return a.fail(err)
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
		fmt.Fprintln(a.stderr, "gocron: the job wasn't added")
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

// service opens the crontab for changes through the safe-write path.
func (a *app) service(ctx context.Context) (*jobs.Service, error) {
	paths, err := config.Resolve()
	if err != nil {
		return nil, err
	}
	if err := paths.EnsureStateDirs(); err != nil {
		return nil, err
	}
	src, err := a.source()
	if err != nil {
		return nil, err
	}
	store, err := crontab.Open(ctx, src, crontab.Options{BackupDir: paths.BackupsDir})
	if err != nil {
		return nil, err
	}
	return jobs.New(store, selfPath()), nil
}
