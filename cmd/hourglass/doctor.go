package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/jackjakarta/hourglass/internal/config"
	"github.com/jackjakarta/hourglass/internal/doctor"
)

func (a *app) doctor(ctx context.Context, args []string) int {
	fs := a.flags("doctor", "doctor [--fix]")
	fix := fs.Bool("fix", false, "give duplicate job IDs new IDs and point wrapped jobs at this hourglass")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if *fix {
		if code := a.fix(ctx); code != exitOK {
			return code
		}
	}
	src, err := a.source()
	if err != nil {
		return a.fail(err)
	}
	env := doctor.DefaultEnv(src)
	env.SelfPath = a.binPath()
	if paths, err := config.Resolve(); err == nil {
		cfg := &doctor.State{Paths: paths}
		var c config.Config
		c, cfg.ConfigWarnings, cfg.ConfigErr = config.Load(paths.ConfigFile)
		cfg.Notify = c.Notify
		cfg.Home, _ = os.UserHomeDir()
		env.State = cfg
	}
	checks := doctor.Run(ctx, env)
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	for _, c := range checks {
		glyph := map[doctor.Status]string{doctor.OK: "✓", doctor.Info: "·", doctor.Warn: "⚠", doctor.Fail: "✗"}[c.Status]
		fmt.Fprintf(w, "%s %s\t%s\n", glyph, c.Name, c.Detail)
		for _, m := range c.More {
			fmt.Fprintf(w, "\t  %s\n", m)
		}
		if c.Hint != "" {
			fmt.Fprintf(w, "\t  → %s\n", c.Hint)
		}
	}
	w.Flush()
	if doctor.Failed(checks) {
		return exitError
	}
	return exitOK
}

// fix repairs what doctor can in one change and says what it did.
func (a *app) fix(ctx context.Context) int {
	svc, _, _, err := a.service(ctx)
	if err != nil {
		return a.fail(err)
	}
	rep, err := svc.Fix(ctx)
	if err != nil {
		return a.fail(err)
	}
	for _, r := range rep.ReIDs {
		fmt.Fprintf(a.stdout, "Fixed: job %q on line %d had duplicate id %s; it is now %s\n", r.Name, r.Line, r.Old, r.ID)
	}
	for _, r := range rep.Rebinned {
		fmt.Fprintf(a.stdout, "Fixed: job %s %q ran %s; it now runs %s\n", r.ID, r.Name, r.From, a.binPath())
	}
	if rep.BinErr != nil {
		fmt.Fprintf(a.stdout, "Not fixed: wrapped jobs run another hourglass, but %v\n", rep.BinErr)
	}
	switch {
	case rep.Changed() && rep.Result.Backup != "":
		fmt.Fprintf(a.stdout, "Backup: %s\n\n", rep.Result.Backup)
	case !rep.Changed():
		fmt.Fprint(a.stdout, "Nothing to fix.\n\n")
	default:
		fmt.Fprintln(a.stdout)
	}
	return exitOK
}
