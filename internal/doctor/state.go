package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jackjakarta/hourglass/internal/config"
	"github.com/jackjakarta/hourglass/internal/crontab"
	"github.com/jackjakarta/hourglass/internal/jobs"
	"github.com/jackjakarta/hourglass/internal/store"
)

// State is hourglass's own files, for the checks about run tracking.
type State struct {
	Paths          config.Paths
	ConfigErr      error    // config.toml didn't load
	ConfigWarnings []string // keys it didn't know
	Notify         config.Notify
	// Home is the user's home directory, where cron starts jobs and where
	// hourglass exec looks for its state when cron runs it.
	Home string
}

// stateChecks check config, state directories, the run history and
// whether jobs can be wrapped. tab is nil when the crontab couldn't be
// read.
func stateChecks(ctx context.Context, env Env, tab *crontab.Crontab) []Check {
	st := env.State
	out := []Check{checkConfig(st), checkStateDirs(st), checkRunHistory(ctx, st), checkTracking(env), checkNotify(env)}
	if tab != nil {
		if c, ok := checkCronState(st, tab); ok {
			out = append(out, c)
		}
	}
	return out
}

func checkConfig(st *State) Check {
	c := Check{Name: "config"}
	_, err := os.Stat(st.Paths.ConfigFile)
	switch {
	case st.ConfigErr != nil:
		c.Status, c.Detail = Warn, st.ConfigErr.Error()
		c.Hint = "hourglass uses the defaults until it's fixed"
	case errors.Is(err, fs.ErrNotExist):
		c.Status, c.Detail = Info, "no config file, using the defaults ("+st.Paths.ConfigFile+")"
	case len(st.ConfigWarnings) > 0:
		c.Status, c.Detail, c.More = Warn, st.Paths.ConfigFile, st.ConfigWarnings
		c.Hint = "check the spelling of these keys"
	default:
		c.Status, c.Detail = OK, st.Paths.ConfigFile
	}
	return c
}

func checkNotify(env Env) Check {
	n := env.State.Notify
	c := Check{Name: "notifications"}
	var targets []string
	if n.Desktop {
		targets = append(targets, "desktop")
	}
	if n.Webhook != "" {
		targets = append(targets, "webhook")
	}
	if n.On == config.NotifyNever || n.On == "" || len(targets) == 0 {
		c.Status, c.Detail = Info, "off"
		return c
	}
	c.Status, c.Detail = OK, fmt.Sprintf("%s, on %s", strings.Join(targets, " and "), n.On)
	if n.Desktop {
		tool := map[string]string{"darwin": "osascript"}[env.GOOS]
		if tool == "" {
			tool = "notify-send"
		}
		if _, err := env.LookPath(tool); err != nil {
			c.Status = Warn
			c.Detail = fmt.Sprintf("desktop notifications need %s, which isn't installed", tool)
			c.Hint = "install it (e.g. libnotify-bin), or set desktop = false under [notify]"
		}
	}
	return c
}

func checkStateDirs(st *State) Check {
	c := Check{Name: "state directory"}
	p := st.Paths
	for _, d := range []string{p.StateDir, p.LogsDir, p.LocksDir, p.BackupsDir} {
		err := dirWritable(d)
		if errors.Is(err, fs.ErrNotExist) {
			continue // created when first needed
		}
		if err != nil {
			c.Status, c.Detail = Fail, fmt.Sprintf("%s isn't writable: %v", d, err)
			c.Hint = "hourglass exec can't record runs or logs; fix the permissions, or set HOURGLASS_STATE_DIR"
			return c
		}
	}
	c.Status, c.Detail = OK, p.StateDir
	return c
}

func dirWritable(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("not a directory")
	}
	return syscall.Access(dir, 2|1) // W_OK|X_OK
}

func checkRunHistory(ctx context.Context, st *State) Check {
	c := Check{Name: "run history"}
	p := st.Paths
	if _, err := os.Stat(p.DB); errors.Is(err, fs.ErrNotExist) {
		c.Status, c.Detail = Info, "no runs yet · "+p.DB+" is created by the first tracked run"
		return c
	}
	db, err := store.Open(ctx, store.Options{Path: p.DB, LogsDir: p.LogsDir, LocksDir: p.LocksDir})
	if err != nil {
		c.Status, c.Detail = Fail, err.Error()
		c.Hint = "jobs still run, but nothing is recorded; move the file aside to start a fresh history"
		return c
	}
	defer db.Close()
	n, err := db.Count(ctx)
	if err != nil {
		c.Status, c.Detail = Fail, "can't read "+p.DB+": "+err.Error()
		return c
	}
	c.Status, c.Detail = OK, fmt.Sprintf("%d %s · %s", n, plural(n, "run"), p.DB)
	if v := db.Version(); v > store.SchemaVersion {
		c.Status = Warn
		c.More = append(c.More, fmt.Sprintf("schema v%d was made by a newer hourglass (this one knows v%d)", v, store.SchemaVersion))
		c.Hint = "update hourglass"
	}
	if mode := db.JournalMode(); mode != "wal" {
		c.Status = max(c.Status, Info)
		c.More = append(c.More, "journal mode is "+mode+", not WAL; concurrent runs may wait on each other")
	}
	return c
}

func checkTracking(env Env) Check {
	c := Check{Name: "run tracking"}
	if err := jobs.CheckBin(env.SelfPath); err != nil {
		c.Status, c.Detail = Warn, err.Error()
		c.Hint = "install hourglass (go install, or a release binary) and run that one to track new jobs"
		return c
	}
	c.Status, c.Detail = OK, "new jobs can run through "+env.SelfPath
	return c
}

// checkCronState warns when cron's jobs would record somewhere other
// than where hourglass reads: an XDG_STATE_HOME or HOURGLASS_STATE_DIR set in
// the shell but not in the crontab.
func checkCronState(st *State, tab *crontab.Crontab) (Check, bool) {
	c := Check{Name: "state seen by cron"}
	if st.Home == "" {
		return c, false
	}
	vars := map[string]string{"HOME": st.Home}
	wrapped := 0
	for _, l := range tab.Lines {
		if l.Kind == crontab.Env {
			vars[l.Env.Name] = l.Env.Value
		}
	}
	for _, j := range tab.Jobs() {
		if j.Wrapped {
			wrapped++
		}
	}
	cron, err := config.ResolveEnv(func(k string) string { return vars[k] }, vars["HOME"])
	if err != nil || filepath.Clean(cron.StateDir) == filepath.Clean(st.Paths.StateDir) {
		return c, false
	}
	c.Status = Warn
	if wrapped == 0 {
		c.Status = Info
	}
	c.Detail = fmt.Sprintf("cron's jobs record to %s, but hourglass here reads %s", cron.StateDir, st.Paths.StateDir)
	var set []string
	for _, k := range []string{"HOURGLASS_STATE_DIR", "XDG_STATE_HOME"} {
		if v := os.Getenv(k); v != "" {
			set = append(set, k)
		}
	}
	c.Hint = "add HOURGLASS_STATE_DIR=" + st.Paths.StateDir + " to the top of your crontab"
	if len(set) > 0 {
		c.Hint += ", or unset " + strings.Join(set, " and ") + " in your shell"
	}
	return c, true
}
