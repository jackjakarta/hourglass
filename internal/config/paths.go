// Package config resolves gocron's files and directories and loads
// config.toml.
package config

import (
	"errors"
	"os"
	"path/filepath"
)

// Paths are gocron's files and directories.
type Paths struct {
	ConfigFile string // config.toml
	StateDir   string
	DB         string // run history
	LogsDir    string // captured job output
	BackupsDir string // crontab backups / revisions
	LocksDir   string // overlap-prevention locks
	ErrorLog   string // wrapper errors
}

// Resolve applies, in order: GOCRON_CONFIG / GOCRON_STATE_DIR, then
// XDG_CONFIG_HOME / XDG_STATE_HOME, then ~/.config and ~/.local/state.
func Resolve() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return ResolveEnv(os.Getenv, home)
}

// ResolveEnv is Resolve over another environment, such as the one cron
// gives a job. An empty home means it is unknown.
func ResolveEnv(getenv func(string) string, home string) (Paths, error) {
	dir := func(override, xdg, fallback string) (string, error) {
		if v := getenv(override); override != "" && v != "" {
			return v, nil
		}
		if v := getenv(xdg); filepath.IsAbs(v) { // XDG says relative values are invalid
			return filepath.Join(v, "gocron"), nil
		}
		if home == "" {
			return "", errors.New("can't find your home directory; set " + override)
		}
		return filepath.Join(home, fallback, "gocron"), nil
	}

	var p Paths
	if v := getenv("GOCRON_CONFIG"); v != "" {
		p.ConfigFile = v
	} else {
		cfgDir, err := dir("", "XDG_CONFIG_HOME", ".config")
		if err != nil {
			return Paths{}, err
		}
		p.ConfigFile = filepath.Join(cfgDir, "config.toml")
	}
	state, err := dir("GOCRON_STATE_DIR", "XDG_STATE_HOME", filepath.Join(".local", "state"))
	if err != nil {
		return Paths{}, err
	}
	p.StateDir = state
	p.DB = filepath.Join(state, "runs.db")
	p.LogsDir = filepath.Join(state, "logs")
	p.BackupsDir = filepath.Join(state, "backups")
	p.LocksDir = filepath.Join(state, "locks")
	p.ErrorLog = filepath.Join(state, "gocron-errors.log")
	return p, nil
}

// EnsureStateDirs creates the state directories, private to the user.
func (p Paths) EnsureStateDirs() error {
	for _, d := range []string{p.StateDir, p.LogsDir, p.BackupsDir, p.LocksDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}
