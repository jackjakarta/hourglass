package main

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/jackjakarta/gocron/internal/doctor"
)

func (a *app) doctor(ctx context.Context, args []string) int {
	fs := a.flags("doctor", "doctor")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	src, err := a.source()
	if err != nil {
		return a.fail(err)
	}
	checks := doctor.Run(ctx, doctor.DefaultEnv(src))
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
