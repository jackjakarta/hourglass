// Package store keeps run history in SQLite: one row per run, with the
// captured output in a log file beside the database.
//
// Many gocron exec processes write at once, so every write transaction is
// BEGIN IMMEDIATE (a deferred one that reads first fails at once in WAL
// mode instead of waiting) and short; nothing holds a transaction while a
// job runs.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackjakarta/gocron/internal/lockfile"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// SchemaVersion is the schema this binary creates and understands.
const SchemaVersion = 1

// Run statuses.
const (
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusTimeout = "timeout"
	StatusSkipped = "skipped"
	StatusError   = "error"
	// StatusLost is reported, never stored: a running row whose process is
	// gone.
	StatusLost = "unknown"
)

// Triggers say what started a run.
const (
	TriggerCron   = "cron"
	TriggerManual = "manual"
	TriggerTest   = "test"
)

// ErrNotFound means no run has the requested id.
var ErrNotFound = errors.New("run not found")

// Run is one execution of a job.
type Run struct {
	ID        int64
	JobID     string
	Trigger   string
	Status    string
	StartedAt time.Time
	EndedAt   *time.Time
	ExitCode  *int
	PID       int
	LogPath   string
	LogBytes  int64
	Truncated bool
}

// Duration is how long the run took, or has taken so far at now. A lost
// run has no known duration.
func (r Run) Duration(now time.Time) time.Duration {
	switch {
	case r.EndedAt != nil:
		return r.EndedAt.Sub(r.StartedAt)
	case r.Status == StatusRunning:
		return now.Sub(r.StartedAt)
	}
	return 0
}

// Failed reports whether the run counts as a failure.
func (r Run) Failed() bool {
	switch r.Status {
	case StatusFailed, StatusTimeout, StatusError, StatusLost:
		return true
	}
	return false
}

// Options configures Open.
type Options struct {
	Path     string // runs.db
	LogsDir  string // where Insert puts a run's log
	LocksDir string // holds the lock that serializes creating the schema
	// Alive reports whether a process exists; the default asks the kernel.
	// Tests stub it so lost-run detection is deterministic.
	Alive func(pid int) bool
	// MaxConns caps open connections; 0 means a few.
	MaxConns int
}

// DB is the run history.
type DB struct {
	db      *sql.DB
	opts    Options
	journal string
	version int
}

// Open opens the run history, creating and migrating it if needed. The
// directories must exist.
func Open(ctx context.Context, opts Options) (*DB, error) {
	if opts.Alive == nil {
		opts.Alive = ProcessAlive
	}
	if opts.MaxConns <= 0 {
		opts.MaxConns = 4
	}
	if fi, err := os.Stat(opts.Path); err == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a file", opts.Path)
	}
	db, err := sql.Open("sqlite", "file:"+opts.Path+"?_busy_timeout=5000&_txlock=immediate&_sync=NORMAL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(opts.MaxConns)
	d := &DB{db: db, opts: opts}
	if err := d.setup(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening run history %s: %w", opts.Path, err)
	}
	return d, nil
}

// setup creates the schema and switches to WAL once, under a file lock:
// changing the journal mode needs an exclusive lock that SQLite's busy
// handler doesn't always wait for, so concurrent first runs would race.
func (d *DB) setup(ctx context.Context) error {
	if d.opts.LocksDir != "" {
		l, err := lockfile.Acquire(filepath.Join(d.opts.LocksDir, ".db.lock"), 5*time.Second)
		if err != nil {
			return fmt.Errorf("waiting for the database lock: %w", err)
		}
		defer l.Release()
	}
	if err := d.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&d.journal); err != nil {
		return err
	}
	if !strings.EqualFold(d.journal, "wal") {
		// Some filesystems can't do WAL; the rollback journal still works.
		_ = d.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&d.journal)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&d.version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, SchemaVersion); err != nil {
			return err
		}
		d.version = SchemaVersion
	case err != nil:
		return err
	}
	// A newer schema was made by a newer gocron. Carry on: the runs table
	// only ever gains columns, and exec must run the job regardless.
	return tx.Commit()
}

const schemaV1 = `
CREATE TABLE runs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id      TEXT    NOT NULL,
  trigger     TEXT    NOT NULL,
  status      TEXT    NOT NULL,
  started_at  INTEGER NOT NULL,
  ended_at    INTEGER,
  exit_code   INTEGER,
  pid         INTEGER,
  log_path    TEXT,
  log_bytes   INTEGER DEFAULT 0,
  truncated   INTEGER DEFAULT 0
);
CREATE INDEX runs_job_started ON runs(job_id, started_at DESC);
CREATE INDEX runs_started     ON runs(started_at DESC);
`

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// Version is the schema version found in the file.
func (d *DB) Version() int { return d.version }

// JournalMode is the journal mode in effect ("wal" normally).
func (d *DB) JournalMode() string { return strings.ToLower(d.journal) }

// LogPath is where the log of run id of job goes.
func (d *DB) LogPath(jobID string, id int64) string {
	return filepath.Join(d.opts.LogsDir, jobID, fmt.Sprintf("%d.log", id))
}

// Insert records a run that is starting and returns it with its id and
// log path.
func (d *DB) Insert(ctx context.Context, jobID, trigger string, started time.Time, pid int) (Run, error) {
	r := Run{JobID: jobID, Trigger: trigger, Status: StatusRunning, StartedAt: started, PID: pid}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx,
		`INSERT INTO runs (job_id, trigger, status, started_at, pid) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		jobID, trigger, StatusRunning, started.UnixMilli(), pid).Scan(&r.ID)
	if err != nil {
		return Run{}, err
	}
	r.LogPath = d.LogPath(jobID, r.ID)
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET log_path = ? WHERE id = ?`, r.LogPath, r.ID); err != nil {
		return Run{}, err
	}
	return r, tx.Commit()
}

// Finish records how run id ended.
func (d *DB) Finish(ctx context.Context, id int64, status string, exitCode *int, ended time.Time, logBytes int64, truncated bool) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, exit_code = ?, ended_at = ?, log_bytes = ?, truncated = ? WHERE id = ?`,
		status, exitCode, ended.UnixMilli(), logBytes, truncated, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	return nil
}

// Prune deletes the finished runs of job beyond the newest keep, and
// their logs. Running rows are never pruned.
func (d *DB) Prune(ctx context.Context, jobID string, keep int) (int, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		DELETE FROM runs WHERE job_id = ? AND status != ? AND id NOT IN (
			SELECT id FROM runs WHERE job_id = ? ORDER BY started_at DESC, id DESC LIMIT ?
		) RETURNING log_path`, jobID, StatusRunning, jobID, keep)
	if err != nil {
		return 0, err
	}
	var logs []string
	for rows.Next() {
		var p sql.NullString
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		if p.Valid && p.String != "" {
			logs = append(logs, p.String)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, p := range logs {
		os.Remove(p)
	}
	return len(logs), nil
}

const runColumns = `id, job_id, trigger, status, started_at, ended_at, exit_code, pid, log_path, log_bytes, truncated`

type scanner interface{ Scan(...any) error }

func (d *DB) scan(s scanner) (Run, error) {
	var (
		r                Run
		started          int64
		ended, exit, pid sql.NullInt64
		logPath          sql.NullString
		logBytes         sql.NullInt64
		truncated        sql.NullBool
	)
	if err := s.Scan(&r.ID, &r.JobID, &r.Trigger, &r.Status, &started, &ended, &exit, &pid, &logPath, &logBytes, &truncated); err != nil {
		return Run{}, err
	}
	r.StartedAt = time.UnixMilli(started)
	if ended.Valid {
		t := time.UnixMilli(ended.Int64)
		r.EndedAt = &t
	}
	if exit.Valid {
		c := int(exit.Int64)
		r.ExitCode = &c
	}
	r.PID, r.LogPath, r.LogBytes, r.Truncated = int(pid.Int64), logPath.String, logBytes.Int64, truncated.Bool
	if r.Status == StatusRunning && !d.opts.Alive(r.PID) {
		r.Status = StatusLost
	}
	return r, nil
}

// Get returns run id.
func (d *DB) Get(ctx context.Context, id int64) (Run, error) {
	r, err := d.scan(d.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	return r, err
}

// Filter selects runs for List.
type Filter struct {
	JobIDs       []string // empty means every job
	FailuresOnly bool     // failed, timeout, error, and lost runs
	Trigger      string   // "" means any
	Limit        int      // 0 means 200
}

// List returns runs newest first.
func (d *DB) List(ctx context.Context, f Filter) ([]Run, error) {
	var where []string
	var args []any
	if len(f.JobIDs) > 0 {
		where = append(where, "job_id IN (?"+strings.Repeat(", ?", len(f.JobIDs)-1)+")")
		for _, id := range f.JobIDs {
			args = append(args, id)
		}
	}
	if f.FailuresOnly {
		// Running rows may turn out lost; they're filtered after scanning.
		where = append(where, "status IN (?, ?, ?, ?)")
		args = append(args, StatusFailed, StatusTimeout, StatusError, StatusRunning)
	}
	if f.Trigger != "" {
		where = append(where, "trigger = ?")
		args = append(args, f.Trigger)
	}
	if f.Limit <= 0 {
		f.Limit = 200
	}
	q := `SELECT ` + runColumns + ` FROM runs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY started_at DESC, id DESC LIMIT ?"
	args = append(args, f.Limit)
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := d.scan(rows)
		if err != nil {
			return nil, err
		}
		if f.FailuresOnly && !r.Failed() {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastFinished returns the status of the job's newest finished run that
// cron started, or "" when there is none. Skipped runs don't count: they
// say nothing about whether the job works.
func (d *DB) LastFinished(ctx context.Context, jobID string) (string, error) {
	var status string
	err := d.db.QueryRowContext(ctx, `SELECT status FROM runs
		WHERE job_id = ? AND trigger = ? AND status IN (?, ?, ?, ?)
		ORDER BY started_at DESC, id DESC LIMIT 1`,
		jobID, TriggerCron, StatusSuccess, StatusFailed, StatusTimeout, StatusError).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return status, err
}

// RecentRuns is how many runs JobStats keeps per job.
const RecentRuns = 20

// JobStats summarizes a job's recent runs.
type JobStats struct {
	Last       *Run  // newest run that isn't running
	Running    *Run  // newest run still in progress
	Recent     []Run // up to RecentRuns, oldest first
	SuccessPct float64
	AvgDur     time.Duration
	Counted    int // runs behind SuccessPct (skipped and running ones aren't)
	OK         int // successful runs among them
}

// Stats summarizes each job's recent runs.
func (d *DB) Stats(ctx context.Context) (map[string]JobStats, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT `+runColumns+` FROM (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY job_id ORDER BY started_at DESC, id DESC) AS rn FROM runs
		) WHERE rn <= ? ORDER BY job_id, started_at, id`, RecentRuns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byJob := map[string][]Run{}
	for rows.Next() {
		r, err := d.scan(rows)
		if err != nil {
			return nil, err
		}
		byJob[r.JobID] = append(byJob[r.JobID], r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]JobStats, len(byJob))
	for id, runs := range byJob {
		out[id] = Summarize(runs)
	}
	return out, nil
}

// Summarize computes stats over runs, oldest first.
func Summarize(runs []Run) JobStats {
	s := JobStats{Recent: runs}
	var total time.Duration
	var timed int
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.Status == StatusRunning {
			if s.Running == nil {
				s.Running = &runs[i]
			}
			continue
		}
		if s.Last == nil {
			s.Last = &runs[i]
		}
		if r.Status == StatusSkipped {
			continue
		}
		s.Counted++
		if r.Status == StatusSuccess {
			s.OK++
		}
		if r.EndedAt != nil {
			total += r.EndedAt.Sub(r.StartedAt)
			timed++
		}
	}
	if s.Counted > 0 {
		s.SuccessPct = 100 * float64(s.OK) / float64(s.Counted)
	}
	if timed > 0 {
		s.AvgDur = total / time.Duration(timed)
	}
	return s
}

// ProcessAlive reports whether pid exists. A process owned by someone
// else (EPERM) exists too.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Count is how many runs the history holds.
func (d *DB) Count(ctx context.Context) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM runs`).Scan(&n)
	return n, err
}
