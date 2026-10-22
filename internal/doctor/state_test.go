package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackjakarta/hourglass/internal/config"
	"github.com/jackjakarta/hourglass/internal/store"
)

func statePaths(t *testing.T) config.Paths {
	t.Helper()
	dir := t.TempDir()
	p, err := config.ResolveEnv(func(k string) string {
		return map[string]string{"HOURGLASS_STATE_DIR": filepath.Join(dir, "state"), "HOURGLASS_CONFIG": filepath.Join(dir, "config.toml")}[k]
	}, "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStateChecksFresh(t *testing.T) {
	env := debianBox.env(fakeSource{content: ""})
	env.State = &State{Paths: statePaths(t), Home: "/home/u"}
	checks := Run(context.Background(), env)
	if c := find(t, checks, "config"); c.Status != Info || !strings.Contains(c.Detail, "defaults") {
		t.Errorf("config = %+v", c)
	}
	if c := find(t, checks, "state directory"); c.Status != OK {
		t.Errorf("state = %+v", c)
	}
	if c := find(t, checks, "run history"); c.Status != Info || !strings.Contains(c.Detail, "no runs yet") {
		t.Errorf("history = %+v", c)
	}
	if _, err := os.Stat(env.State.Paths.DB); err == nil {
		t.Error("doctor created the database")
	}
	if c := find(t, checks, "run tracking"); c.Status != OK {
		t.Errorf("tracking = %+v", c)
	}
	if Failed(checks) {
		t.Errorf("a fresh install fails: %+v", checks)
	}
}

func TestStateChecksProblems(t *testing.T) {
	p := statePaths(t)
	if err := p.EnsureStateDirs(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), store.Options{Path: p.DB, LogsDir: p.LogsDir, LocksDir: p.LocksDir})
	if err != nil {
		t.Fatal(err)
	}
	db.Insert(context.Background(), "abc234", store.TriggerCron, time.Unix(1_790_000_000, 0), 1)
	db.Close()
	os.WriteFile(p.ConfigFile, []byte("x = 1\n"), 0o600)

	env := debianBox.env(fakeSource{content: "# hourglass:job id=abc234\n0 0 * * * /usr/local/bin/hourglass exec abc234 -- 'x'\n"})
	env.SelfPath = filepath.Join(os.TempDir(), "go-build1/exe/hourglass")
	// The shell points hourglass at p; cron, without that variable, would use
	// ~/.local/state/hourglass.
	env.State = &State{Paths: p, Home: "/home/u", ConfigWarnings: []string{"unknown key \"x\""}}
	checks := Run(context.Background(), env)
	if c := find(t, checks, "config"); c.Status != Warn || len(c.More) != 1 {
		t.Errorf("config = %+v", c)
	}
	if c := find(t, checks, "run history"); c.Status != OK || !strings.HasPrefix(c.Detail, "1 run ·") {
		t.Errorf("history = %+v", c)
	}
	if c := find(t, checks, "run tracking"); c.Status != Warn || !strings.Contains(c.Detail, "temporary build") {
		t.Errorf("tracking = %+v", c)
	}
	c := find(t, checks, "state seen by cron")
	if c.Status != Warn || !strings.Contains(c.Detail, "/home/u/.local/state/hourglass") || !strings.Contains(c.Hint, "HOURGLASS_STATE_DIR="+p.StateDir) {
		t.Errorf("cron state = %+v", c)
	}

	env.State.ConfigErr = errors.New("config.toml: bad")
	if c := find(t, Run(context.Background(), env), "config"); c.Status != Warn || c.Detail != "config.toml: bad" {
		t.Errorf("bad config = %+v", c)
	}
}

func TestStateChecksCronSeesSameDir(t *testing.T) {
	p := statePaths(t)
	env := debianBox.env(fakeSource{content: "HOURGLASS_STATE_DIR=" + p.StateDir + "\n"})
	env.State = &State{Paths: p, Home: "/home/u"}
	for _, c := range Run(context.Background(), env) {
		if c.Name == "state seen by cron" {
			t.Errorf("unexpected %+v", c)
		}
	}
}

func TestStateDirNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	p := statePaths(t)
	if err := os.MkdirAll(p.StateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p.StateDir, 0o700) })
	env := debianBox.env(fakeSource{})
	env.State = &State{Paths: p}
	if c := find(t, Run(context.Background(), env), "state directory"); c.Status != Fail {
		t.Errorf("state = %+v", c)
	}
}

func TestNotifyCheck(t *testing.T) {
	for name, tt := range map[string]struct {
		notify config.Notify
		goos   string
		status Status
		detail string
	}{
		"off":          {config.Notify{On: "never", Desktop: true}, "linux", Info, "off"},
		"no targets":   {config.Notify{On: "failure"}, "linux", Info, "off"},
		"missing tool": {config.Notify{On: "failure", Desktop: true}, "linux", Warn, "need notify-send"},
		"webhook only": {config.Notify{On: "change", Webhook: "https://x"}, "linux", OK, "webhook, on change"},
		"mac":          {config.Notify{On: "always", Desktop: true, Webhook: "https://x"}, "darwin", OK, "desktop and webhook, on always"},
		"other os":     {config.Notify{On: "always", Desktop: true}, "freebsd", Warn, "need notify-send"},
	} {
		t.Run(name, func(t *testing.T) {
			sys := debianBox
			sys.paths = map[string]string{"osascript": "/usr/bin/osascript"}
			env := sys.env(fakeSource{})
			env.GOOS = tt.goos
			env.State = &State{Paths: statePaths(t), Notify: tt.notify}
			c := find(t, Run(context.Background(), env), "notifications")
			if c.Status != tt.status || !strings.Contains(c.Detail, tt.detail) {
				t.Errorf("check = %+v", c)
			}
		})
	}
}
