package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is config.toml. Every key is optional; Default fills the gaps.
type Config struct {
	Theme    string   `toml:"theme"`
	UI       UI       `toml:"ui"`
	Runs     Runs     `toml:"runs"`
	Backups  Backups  `toml:"backups"`
	Defaults Defaults `toml:"defaults"`
	Notify   Notify   `toml:"notify"`
}

// UI holds display preferences.
type UI struct {
	RelativeTimes       bool   `toml:"relative_times"`
	TimeFormat          string `toml:"time_format"`
	WeekStarts          string `toml:"week_starts"`
	DefaultSort         string `toml:"default_sort"`
	CongestionThreshold int    `toml:"congestion_threshold"`
}

// Runs configures run history and gocron exec.
type Runs struct {
	Retention   int    `toml:"retention"`     // runs kept per job
	MaxLogBytes int64  `toml:"max_log_bytes"` // per run
	MailOutput  string `toml:"mail_output"`   // never | on_failure | always
}

// Mail output modes: what gocron exec echoes to stdout, which cron mails.
const (
	MailNever     = "never"
	MailOnFailure = "on_failure"
	MailAlways    = "always"
)

// Backups configures crontab backups.
type Backups struct {
	Keep int `toml:"keep"`
}

// Defaults apply to new and adopted jobs.
type Defaults struct {
	Wrap      bool     `toml:"wrap"`
	NoOverlap bool     `toml:"no_overlap"`
	Timeout   Duration `toml:"timeout"`
}

// Notify is read for forward compatibility; notifications arrive later.
type Notify struct {
	On      string `toml:"on"`
	Desktop bool   `toml:"desktop"`
	Webhook string `toml:"webhook"`
}

// Duration is a time.Duration written as "1h30m"; "" means none.
type Duration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("invalid duration %q (use e.g. 30m or 1h)", b)
	}
	if v < 0 {
		return fmt.Errorf("invalid duration %q: it can't be negative", b)
	}
	*d = Duration(v)
	return nil
}

// Default returns the configuration used when config.toml sets nothing.
func Default() Config {
	return Config{
		Theme: "nightshift",
		UI: UI{
			RelativeTimes:       true,
			TimeFormat:          "15:04",
			WeekStarts:          "monday",
			DefaultSort:         "next",
			CongestionThreshold: 3,
		},
		Runs:     Runs{Retention: 100, MaxLogBytes: 1 << 20, MailOutput: MailNever},
		Backups:  Backups{Keep: 50},
		Defaults: Defaults{Wrap: true, NoOverlap: true},
		Notify:   Notify{On: "failure", Desktop: true},
	}
}

// Load reads the config file at path over the defaults. A missing file is
// not an error. Keys gocron doesn't know come back as warnings. On error
// the defaults are returned, so callers can carry on.
func Load(path string) (Config, []string, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil, nil
	}
	if err != nil {
		return Default(), nil, err
	}
	md, err := toml.Decode(string(b), &cfg)
	if err != nil {
		return Default(), nil, fmt.Errorf("%s: %w", path, err)
	}
	var warnings []string
	for _, k := range md.Undecoded() {
		warnings = append(warnings, fmt.Sprintf("%s: unknown key %q", path, k.String()))
	}
	if err := cfg.validate(); err != nil {
		return Default(), warnings, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, warnings, nil
}

func (c Config) validate() error {
	var problems []string
	oneOf := func(key, v string, allowed ...string) {
		for _, a := range allowed {
			if v == a {
				return
			}
		}
		problems = append(problems, fmt.Sprintf("%s must be one of %s, not %q", key, strings.Join(allowed, ", "), v))
	}
	oneOf("theme", c.Theme, "nightshift", "daybreak", "mono")
	oneOf("ui.week_starts", c.UI.WeekStarts, "monday", "sunday")
	oneOf("ui.default_sort", c.UI.DefaultSort, "next", "name", "crontab")
	oneOf("runs.mail_output", c.Runs.MailOutput, MailNever, MailOnFailure, MailAlways)
	oneOf("notify.on", c.Notify.On, "never", "failure", "always", "change")
	if c.Runs.Retention < 1 {
		problems = append(problems, "runs.retention must be at least 1")
	}
	if c.Runs.MaxLogBytes < 1024 {
		problems = append(problems, "runs.max_log_bytes must be at least 1024")
	}
	if c.Backups.Keep < 1 {
		problems = append(problems, "backups.keep must be at least 1")
	}
	if c.UI.CongestionThreshold < 2 {
		problems = append(problems, "ui.congestion_threshold must be at least 2")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
