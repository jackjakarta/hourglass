package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingIsDefault(t *testing.T) {
	cfg, warnings, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || len(warnings) > 0 {
		t.Fatalf("Load = %v, %v", warnings, err)
	}
	if cfg != Default() {
		t.Errorf("cfg = %+v, want defaults", cfg)
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	p := writeConfig(t, `
theme = "mono"
[runs]
retention = 7
mail_output = "on_failure"
[defaults]
wrap = false
timeout = "1h30m"
`)
	cfg, warnings, err := Load(p)
	if err != nil || len(warnings) > 0 {
		t.Fatalf("Load = %v, %v", warnings, err)
	}
	if cfg.Theme != "mono" || cfg.Runs.Retention != 7 || cfg.Runs.MailOutput != MailOnFailure {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.Defaults.Wrap || !cfg.Defaults.NoOverlap || time.Duration(cfg.Defaults.Timeout) != 90*time.Minute {
		t.Errorf("defaults = %+v", cfg.Defaults)
	}
	if cfg.Runs.MaxLogBytes != 1<<20 || cfg.Backups.Keep != 50 {
		t.Errorf("unset keys lost their defaults: %+v", cfg)
	}
}

func TestLoadUnknownKeysWarn(t *testing.T) {
	cfg, warnings, err := Load(writeConfig(t, "colour = 1\n[runs]\nretension = 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[1], `"runs.retension"`) {
		t.Errorf("warnings = %q", warnings)
	}
	if cfg.Runs.Retention != 100 {
		t.Errorf("retention = %d", cfg.Runs.Retention)
	}
}

func TestLoadInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"syntax":    "theme = ",
		"enum":      `[runs]` + "\n" + `mail_output = "sometimes"`,
		"range":     "[runs]\nretention = 0",
		"duration":  `[defaults]` + "\n" + `timeout = "soon"`,
		"negative":  `[defaults]` + "\n" + `timeout = "-1m"`,
		"wrongtype": "[runs]\nretention = \"many\"",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, _, err := Load(writeConfig(t, body))
			if err == nil {
				t.Fatal("want an error")
			}
			if cfg != Default() {
				t.Errorf("an invalid config must fall back to the defaults, got %+v", cfg)
			}
		})
	}
}

func TestResolveEnvForCron(t *testing.T) {
	env := map[string]string{"HOME": "/home/u"}
	p, err := ResolveEnv(func(k string) string { return env[k] }, "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	if p.StateDir != "/home/u/.local/state/gocron" {
		t.Errorf("StateDir = %q", p.StateDir)
	}
	if _, err := ResolveEnv(func(string) string { return "" }, ""); err == nil {
		t.Error("no home and no override should fail")
	}
}
