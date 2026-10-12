package jobs

import (
	"context"
	"path/filepath"

	"github.com/jackjakarta/gocron/internal/crontab"
)

// FixReport is what Fix changed, or would change.
type FixReport struct {
	// ReIDs are jobs that shared an ID with an earlier job and got a new one.
	ReIDs []ReID
	// Rebinned are wrapped jobs moved to this gocron binary.
	Rebinned []Rebin
	// BinErr says why wrapped jobs weren't moved to this binary, when there
	// were some to move but this binary can't be written into job lines.
	BinErr error
	Result Result
}

// ReID is a job given a new ID.
type ReID struct {
	Line          int // the job's cron line (1-based)
	Name, Old, ID string
}

// Rebin is a wrapped job whose gocron path was rewritten.
type Rebin struct {
	ID, Name, From string
}

// Changed reports whether Fix changed anything.
func (r FixReport) Changed() bool { return len(r.ReIDs) > 0 || len(r.Rebinned) > 0 }

// Fix repairs what doctor can repair on its own, in one change (one
// backup, one undo step): duplicate job IDs get new IDs, and wrapped jobs
// that run another gocron binary are moved to this one. Orphaned meta
// lines are left for a human.
func (s *Service) Fix(ctx context.Context) (FixReport, error) {
	var rep FixReport
	binErr := s.WrapError()
	res, err := s.apply(ctx, func(c *crontab.Crontab) (string, error) {
		rep = FixReport{}
		for _, p := range c.Problems() {
			if p.Code != "duplicate-id" {
				continue
			}
			meta := c.Lines[p.Line-1].Meta
			id := c.NewID()
			if err := c.ReID(p.Line, id); err != nil {
				return "", err
			}
			rep.ReIDs = append(rep.ReIDs, ReID{Line: p.Line + 1, Name: meta.Get("name"), Old: meta.Get("id"), ID: id})
		}
		self := resolve(s.opts.Bin)
		for _, j := range c.Jobs() {
			if !j.Wrapped || resolve(j.Bin) == self {
				continue
			}
			if binErr != nil {
				rep.BinErr = binErr
				continue
			}
			if err := c.UpdateJob(j, s.opts.Bin); err != nil {
				return "", err
			}
			rep.Rebinned = append(rep.Rebinned, Rebin{ID: j.ID, Name: j.Name, From: j.Bin})
		}
		return "", nil
	})
	rep.Result = res
	return rep, err
}

// resolve follows symlinks in path, so two names for one binary compare
// equal.
func resolve(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}
