package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackjakarta/hourglass/internal/crontab"
	"github.com/jackjakarta/hourglass/internal/jobs"
)

// change runs enable, disable or rm on one managed job.
func (a *app) change(ctx context.Context, cmd string, args []string) int {
	fs := a.flags(cmd, cmd+" ID")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return exitUsage
	}
	id := fs.Arg(0)
	svc, _, _, err := a.service(ctx)
	if err != nil {
		return a.fail(err)
	}
	var res jobs.Result
	var done string
	switch cmd {
	case "enable":
		res, err = svc.SetEnabled(ctx, id, true)
		done = "Resumed"
	case "disable":
		res, err = svc.SetEnabled(ctx, id, false)
		done = "Paused"
	case "rm":
		res, err = svc.Delete(ctx, id)
		done = "Deleted"
	}
	if errors.Is(err, crontab.ErrNotFound) {
		return a.fail(fmt.Errorf("no managed job with id %s (hourglass list shows the ids)", id))
	}
	if err != nil {
		return a.fail(err)
	}
	if res.Backup == "" {
		fmt.Fprintf(a.stdout, "Job %s was already %s\n", id, map[string]string{"enable": "enabled", "disable": "paused"}[cmd])
		return exitOK
	}
	fmt.Fprintf(a.stdout, "%s job %s · backup: %s\n", done, id, res.Backup)
	return exitOK
}
