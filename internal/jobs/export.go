package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jackjakarta/hourglass/internal/crontab"
	"github.com/jackjakarta/hourglass/internal/runner"
)

// ExportVersion is the export format's version.
const ExportVersion = 1

// ExportDoc is the managed jobs as hourglass export writes them.
type ExportDoc struct {
	Version int         `yaml:"version"`
	Jobs    []ExportJob `yaml:"jobs"`
}

// ExportJob is one exported job.
type ExportJob struct {
	ID        string   `yaml:"id"`
	Name      string   `yaml:"name,omitempty"`
	Desc      string   `yaml:"desc,omitempty"`
	Tags      []string `yaml:"tags,omitempty,flow"`
	Schedule  string   `yaml:"schedule"`
	Command   string   `yaml:"command"`
	Enabled   bool     `yaml:"enabled"`
	Wrapped   bool     `yaml:"wrapped"`
	Timeout   string   `yaml:"timeout,omitempty"`
	NoOverlap bool     `yaml:"no_overlap,omitempty"`
	// TZ is the CRON_TZ line in effect where the job was exported. It's
	// informational: import warns when the job would land under another.
	TZ string `yaml:"tz,omitempty"`
}

// Export returns the loaded crontab's managed jobs.
func (s *Service) Export() ExportDoc {
	doc := ExportDoc{Version: ExportVersion, Jobs: []ExportJob{}}
	for _, j := range s.Snapshot().Jobs {
		if j.Managed {
			doc.Jobs = append(doc.Jobs, exportJob(j.Job))
		}
	}
	return doc
}

func exportJob(j crontab.Job) ExportJob {
	e := ExportJob{
		ID: j.ID, Name: j.Name, Desc: j.Desc, Tags: j.Tags, Schedule: j.Schedule, Command: j.Command,
		Enabled: j.Enabled, Wrapped: j.Wrapped, NoOverlap: j.NoOverlap, TZ: j.TZ,
	}
	if j.Timeout > 0 {
		e.Timeout = crontab.FormatDuration(j.Timeout)
	}
	return e
}

// EncodeExport writes doc as YAML.
func EncodeExport(w io.Writer, doc ExportDoc) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return enc.Close()
}

// ParseExport reads an export, refusing unknown fields, other versions,
// and ids that are missing, malformed or repeated.
func ParseExport(r io.Reader) (ExportDoc, error) {
	var doc ExportDoc
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return doc, errors.New("the file is empty")
		}
		return doc, err
	}
	switch {
	case doc.Version == 0:
		return doc, errors.New("not a hourglass export: no version")
	case doc.Version != ExportVersion:
		return doc, fmt.Errorf("export version %d isn't supported (this hourglass reads version %d)", doc.Version, ExportVersion)
	}
	seen := map[string]bool{}
	for i, j := range doc.Jobs {
		switch {
		case j.ID == "":
			return doc, fmt.Errorf("job %d has no id", i+1)
		case !runner.ValidID(j.ID):
			return doc, fmt.Errorf("job %d: id %q must be letters, digits, - and _", i+1, j.ID)
		case seen[j.ID]:
			return doc, fmt.Errorf("job id %s appears twice", j.ID)
		}
		seen[j.ID] = true
		if _, err := j.timeout(); err != nil {
			return doc, fmt.Errorf("job %s: %w", j.ID, err)
		}
	}
	return doc, nil
}

func (j ExportJob) timeout() (time.Duration, error) {
	if j.Timeout == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(j.Timeout)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("timeout %q isn't a duration like 30m or 1h", j.Timeout)
	}
	return d, nil
}

// Draft is the job as a draft, for linting.
func (j ExportJob) Draft() Draft {
	d, _ := j.timeout()
	return Draft{Name: j.Name, Schedule: j.Schedule, Command: j.Command, Tags: j.Tags, Wrapped: j.Wrapped, Timeout: d, NoOverlap: j.NoOverlap}
}

func (j ExportJob) crontabJob() crontab.Job {
	d, _ := j.timeout()
	cj := crontab.Job{
		ID: j.ID, Name: j.Name, Desc: j.Desc, Tags: j.Tags, Schedule: j.Schedule, Command: j.Command,
		Enabled: j.Enabled, Wrapped: j.Wrapped, Managed: true,
	}
	if j.Wrapped {
		cj.Timeout, cj.NoOverlap = d, j.NoOverlap
	}
	return cj
}

// ImportChange is an imported job that differs from the crontab's.
type ImportChange struct {
	Job    ExportJob
	Fields []string // what differs: "name", "schedule", …
}

// ImportPlan is what an import does.
type ImportPlan struct {
	Create   []ExportJob
	Update   []ImportChange
	Same     []ExportJob
	Warnings []string
}

// PlanImport works out what importing doc into the loaded crontab would
// do, without changing anything.
func (s *Service) PlanImport(doc ExportDoc) (ImportPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.planImport(s.store.Crontab(), doc)
}

// Import merges doc into the crontab by id: new ids are added, jobs that
// differ are updated, and jobs missing from doc are left alone. It's one
// change: one backup, one undo step.
func (s *Service) Import(ctx context.Context, doc ExportDoc) (ImportPlan, Result, error) {
	var plan ImportPlan
	res, err := s.apply(ctx, func(c *crontab.Crontab) (string, error) {
		var err error
		if plan, err = s.planImport(c, doc); err != nil {
			return "", err
		}
		for _, j := range plan.Create {
			bin := ""
			if j.Wrapped {
				bin = s.opts.Bin
			}
			if _, err := c.AddJob(j.crontabJob(), bin); err != nil {
				return "", fmt.Errorf("job %s: %w", j.ID, err)
			}
		}
		for _, ch := range plan.Update {
			cur, _ := c.Job(ch.Job.ID)
			next := ch.Job.crontabJob()
			next.Bin = cur.Bin
			bin := "" // keep the path a wrapped line already has
			if next.Wrapped && cur.Bin == "" {
				bin = s.opts.Bin
			}
			if err := c.UpdateJob(next, bin); err != nil {
				return "", fmt.Errorf("job %s: %w", ch.Job.ID, err)
			}
		}
		return "", nil
	})
	return plan, res, err
}

func (s *Service) planImport(c *crontab.Crontab, doc ExportDoc) (ImportPlan, error) {
	var plan ImportPlan
	dup := map[string]bool{}
	for _, p := range c.Problems() {
		if p.Code == "duplicate-id" {
			dup[c.Lines[p.Line-1].Meta.Get("id")] = true
		}
	}
	// New jobs go at the end, under the last CRON_TZ line.
	endTZ := ""
	for _, e := range c.Env() {
		if e.Name == "CRON_TZ" {
			endTZ = e.Value
		}
	}
	for _, j := range doc.Jobs {
		if dup[j.ID] {
			return plan, fmt.Errorf("job id %s is used twice in the crontab; run hourglass doctor first", j.ID)
		}
		cur, ok := c.Job(j.ID)
		needsBin := j.Wrapped && (!ok || cur.Bin == "")
		if needsBin {
			if err := s.WrapError(); err != nil {
				return plan, fmt.Errorf("job %s tracks its runs, but %w", j.ID, err)
			}
		}
		tz := endTZ
		switch {
		case !ok:
			plan.Create = append(plan.Create, j)
		default:
			tz = cur.TZ
			if fields := diffFields(exportJob(cur), j); len(fields) > 0 {
				plan.Update = append(plan.Update, ImportChange{Job: j, Fields: fields})
			} else {
				plan.Same = append(plan.Same, j)
			}
		}
		if j.TZ != tz {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("job %s was exported under CRON_TZ=%s but lands under CRON_TZ=%s here", j.ID, orNone(j.TZ), orNone(tz)))
		}
	}
	return plan, nil
}

func orNone(tz string) string {
	if tz == "" {
		return "(none)"
	}
	return tz
}

// diffFields names the fields of b that differ from a, ignoring tz and
// the binary path.
func diffFields(a, b ExportJob) []string {
	ta, _ := a.timeout()
	tb, _ := b.timeout()
	if !b.Wrapped { // options only exist on wrapped lines
		tb, b.NoOverlap = 0, false
	}
	var out []string
	add := func(differs bool, name string) {
		if differs {
			out = append(out, name)
		}
	}
	add(a.Name != b.Name, "name")
	add(a.Desc != b.Desc, "desc")
	add(!slices.Equal(a.Tags, b.Tags), "tags")
	add(strings.Join(strings.Fields(a.Schedule), " ") != strings.Join(strings.Fields(b.Schedule), " "), "schedule")
	add(a.Command != b.Command, "command")
	add(a.Enabled != b.Enabled, "enabled")
	add(a.Wrapped != b.Wrapped, "wrapped")
	add(ta != tb, "timeout")
	add(a.NoOverlap != b.NoOverlap, "no_overlap")
	return out
}
