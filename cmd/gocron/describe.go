package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/jackjakarta/gocron/internal/cronexpr"
	"github.com/jackjakarta/gocron/internal/humanize"
)

func (a *app) describe(args []string) int {
	fs := a.flags("describe", `describe [-n N] "<expr>"`)
	n := fs.Int("n", 5, "number of upcoming runs to show")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	expr := strings.Join(fs.Args(), " ")
	if expr == "" || *n < 0 {
		fs.Usage()
		return exitUsage
	}
	e, err := cronexpr.Parse(expr)
	if err != nil {
		return a.fail(fmt.Errorf("invalid schedule %q: %w", expr, err))
	}

	fmt.Fprintf(a.stdout, "%s\n%s\n", e.Raw, e.Describe())
	now := a.now()
	switch next := e.Next(now, *n); {
	case e.Reboot:
		fmt.Fprintln(a.stdout, "\nRuns once each time the system boots.")
	case *n == 0:
	case len(next) == 0:
		fmt.Fprintln(a.stdout, "\nNever runs.")
	default:
		fmt.Fprintln(a.stdout, "\nNext runs (local time):")
		w := tabwriter.NewWriter(a.stdout, 0, 0, 3, ' ', 0)
		for _, t := range next {
			fmt.Fprintf(w, "  %s\tin %s\n", humanize.Absolute(t, now), humanize.Duration(t.Sub(now)))
		}
		w.Flush()
	}

	if issues := cronexpr.Lint(expr); len(issues) > 0 {
		fmt.Fprintln(a.stdout)
		for _, is := range issues {
			glyph := "·"
			if is.Severity == cronexpr.Warn {
				glyph = "⚠"
			}
			fmt.Fprintf(a.stdout, "%s %s\n", glyph, is.Message)
		}
	}
	return exitOK
}
