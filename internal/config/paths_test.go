package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name                  string
		env                   map[string]string
		wantConfig, wantState string
	}{
		{"defaults", nil, "/home/u/.config/gocron/config.toml", "/home/u/.local/state/gocron"},
		{"xdg", map[string]string{"XDG_CONFIG_HOME": "/x/cfg", "XDG_STATE_HOME": "/x/state"},
			"/x/cfg/gocron/config.toml", "/x/state/gocron"},
		{"relative xdg is ignored", map[string]string{"XDG_STATE_HOME": "rel"}, "/home/u/.config/gocron/config.toml", "/home/u/.local/state/gocron"},
		{"overrides win", map[string]string{"XDG_STATE_HOME": "/x/state", "GOCRON_STATE_DIR": "/o/state", "GOCRON_CONFIG": "/o/c.toml"},
			"/o/c.toml", "/o/state"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", "/home/u")
			for _, k := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "GOCRON_STATE_DIR", "GOCRON_CONFIG"} {
				t.Setenv(k, tt.env[k])
			}
			p, err := Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if p.ConfigFile != tt.wantConfig || p.StateDir != tt.wantState {
				t.Errorf("got config=%s state=%s", p.ConfigFile, p.StateDir)
			}
			if p.BackupsDir != filepath.Join(tt.wantState, "backups") || p.DB != filepath.Join(tt.wantState, "runs.db") ||
				p.LocksDir != filepath.Join(tt.wantState, "locks") || p.LogsDir != filepath.Join(tt.wantState, "logs") {
				t.Errorf("derived paths wrong: %+v", p)
			}
		})
	}
}

func TestEnsureStateDirs(t *testing.T) {
	t.Setenv("GOCRON_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureStateDirs(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.StateDir, p.LogsDir, p.BackupsDir, p.LocksDir} {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi, err)
		}
	}
}
