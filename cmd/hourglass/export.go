package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jackjakarta/hourglass/internal/cronexpr"
	"github.com/jackjakarta/hourglass/internal/jobs"
)

const exportHeader = `# hourglass export: your managed jobs. Import with hourglass import FILE.
# Environment lines (PATH, MAILTO, CRON_TZ, …) aren't included.
`

func (a *app) export(ctx context.Context, args []string) int {
	fs := a.flags("export [--file F]", "export [--file F]")
	file := fs.String("file", "", "write to this file instead of stdout")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return exitUsage
	}
	svc, _, _, err := a.service(ctx)
	if err != nil {
		return a.fail(err)
	}
	doc := svc.Export()
	var buf bytes.Buffer
	buf.WriteString(exportHeader)
	if err := jobs.EncodeExport(&buf, doc); err != nil {
		return a.fail(err)
	}
	if *file == "" {
		_, err := a.stdout.Write(buf.Bytes())
		if err != nil {
			return a.fail(err)
		}
		return exitOK
	}
	if err := os.WriteFile(*file, buf.Bytes(), 0o600); err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.stdout, "Exported %d %s to %s\n", len(doc.Jobs), plural(len(doc.Jobs), "job", "jobs"), *file)
	return exitOK
}

const importSynopsis = `import [--dry-run] FILE

Merges the jobs in FILE (an export; - reads stdin) into your crontab by id:
new ids are added, jobs that differ are updated, and jobs that aren't in
FILE are left alone.`

func (a *app) importJobs(ctx context.Context, args []string) int {
	fs := a.flags("import", importSynopsis)
	dryRun := fs.Bool("dry-run", false, "show what would change without writing anything")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return exitUsage
	}
	var r io.Reader
	if name := fs.Arg(0); name == "-" {
		r = a.stdin
		if r == nil {
			r = os.Stdin
		}
	} else {
		f, err := os.Open(name)
		if err != nil {
			return a.fail(err)
		}
		defer f.Close()
		r = f
	}
	doc, err := jobs.ParseExport(r)
	if err != nil {
		fmt.Fprintf(a.stderr, "hourglass: %s: %v\n", fs.Arg(0), err)
		return exitError
	}

	// Every job must be valid before anything is written.
	bad := false
	for _, j := range doc.Jobs {
		for _, is := range jobs.LintDraft(j.Draft()) {
			switch is.Severity {
			case cronexpr.Error:
				bad = true
				fmt.Fprintf(a.stderr, "✗ %s: %s\n", j.ID, is.Message)
			case cronexpr.Warn:
				fmt.Fprintf(a.stderr, "⚠ %s: %s\n", j.ID, is.Message)
			}
		}
	}
	if bad {
		fmt.Fprintln(a.stderr, "hourglass: nothing was imported")
		return exitError
	}

	svc, _, _, err := a.service(ctx)
	if err != nil {
		return a.fail(err)
	}
	var plan jobs.ImportPlan
	var res jobs.Result
	if *dryRun {
		plan, err = svc.PlanImport(doc)
	} else {
		plan, res, err = svc.Import(ctx, doc)
	}
	if err != nil {
		if !*dryRun {
			fmt.Fprintln(a.stderr, "hourglass: nothing was imported")
		}
		return a.fail(err)
	}
	for _, j := range plan.Create {
		fmt.Fprintf(a.stdout, "+ %s %s\n", j.ID, title(j))
	}
	for _, ch := range plan.Update {
		fmt.Fprintf(a.stdout, "~ %s %s (%s)\n", ch.Job.ID, title(ch.Job), strings.Join(ch.Fields, ", "))
	}
	for _, w := range plan.Warnings {
		fmt.Fprintf(a.stderr, "⚠ %s\n", w)
	}
	counts := fmt.Sprintf("%d to create, %d to update, %d unchanged", len(plan.Create), len(plan.Update), len(plan.Same))
	switch {
	case *dryRun:
		fmt.Fprintf(a.stdout, "Dry run: %s. Nothing was written.\n", counts)
	case res.Backup == "":
		fmt.Fprintf(a.stdout, "Nothing to import: %d unchanged.\n", len(plan.Same))
	default:
		fmt.Fprintf(a.stdout, "Imported: %d created, %d updated, %d unchanged · backup: %s\n", len(plan.Create), len(plan.Update), len(plan.Same), res.Backup)
	}
	return exitOK
}

func title(j jobs.ExportJob) string {
	if j.Name != "" {
		return fmt.Sprintf("%q", j.Name)
	}
	return fmt.Sprintf("%q", truncate(j.Command, 40))
}
