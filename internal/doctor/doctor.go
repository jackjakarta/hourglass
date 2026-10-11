// Package doctor runs health checks on the cron setup and the crontab.
package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackjakarta/gocron/internal/crontab"
)

// Status is a check's outcome.
type Status int

const (
	OK Status = iota
	Info
	Warn
	Fail
)

// Check is one health check result.
type Check struct {
	Name   string
	Status Status
	Detail string   // one-line summary
	More   []string // extra lines, e.g. one per problem
	Hint   string   // what to do next
}

// Daemon identifies the cron implementation.
type Daemon string

const (
	DaemonNone    Daemon = ""
	DaemonDebian  Daemon = "Debian cron"
	DaemonCronie  Daemon = "cronie"
	DaemonUnknown Daemon = "cron"
)

// HonorsCronTZ reports whether the daemon applies CRON_TZ: cronie does,
// Debian's cron ignores it, and for others we can't tell.
func (d Daemon) HonorsCronTZ() (honors, known bool) {
	switch d {
	case DaemonCronie:
		return true, true
	case DaemonDebian:
		return false, true
	}
	return false, false
}

// Env is everything the checks touch, so tests can fake it.
type Env struct {
	Source   crontab.Source
	LookPath func(file string) (string, error)
	// Run runs a command and returns its trimmed stdout and exit code.
	Run func(ctx context.Context, name string, args ...string) (stdout string, code int, err error)
	// Processes returns the names of running processes.
	Processes func() []string
	// Stat reports whether a path exists.
	Exists func(path string) bool
	// SelfPath is the running gocron binary, symlinks resolved.
	SelfPath string
}

// DefaultEnv probes the real system.
func DefaultEnv(src crontab.Source) Env {
	self, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(self); err == nil {
		self = r
	}
	return Env{
		Source:    src,
		LookPath:  exec.LookPath,
		Run:       runCommand,
		Processes: procNames,
		Exists:    func(p string) bool { _, err := os.Stat(p); return err == nil },
		SelfPath:  self,
	}
}

func runCommand(ctx context.Context, name string, args ...string) (string, int, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &out
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return strings.TrimSpace(out.String()), exit.ExitCode(), nil
	}
	if err != nil {
		return "", -1, err
	}
	return strings.TrimSpace(out.String()), 0, nil
}

func procNames() []string {
	paths, _ := filepath.Glob("/proc/[0-9]*/comm")
	var out []string
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			out = append(out, strings.TrimSpace(string(b)))
		}
	}
	return out
}

// Run runs every check.
func Run(ctx context.Context, env Env) []Check {
	_, isFile := env.Source.(crontab.FileSource)
	var checks []Check
	if !isFile {
		checks = append(checks, checkBinary(env))
	}
	access, content := checkAccess(ctx, env)
	checks = append(checks, access)
	daemonCheck, daemon := checkDaemon(ctx, env)
	checks = append(checks, daemonCheck)
	if content == nil && access.Status == Fail {
		return checks
	}
	tab := crontab.Parse(content)
	checks = append(checks, checkProblems(tab))
	if c, ok := checkCronTZ(tab, daemon); ok {
		checks = append(checks, c)
	}
	if c, ok := checkWrappedPaths(tab, env); ok {
		checks = append(checks, c)
	}
	checks = append(checks, checkMailto(tab))
	return checks
}

// Failed reports whether any check failed.
func Failed(checks []Check) bool {
	for _, c := range checks {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

func checkBinary(env Env) Check {
	c := Check{Name: "crontab command"}
	p, err := env.LookPath("crontab")
	if err != nil {
		c.Status, c.Detail = Fail, "crontab isn't installed or isn't on your PATH"
		c.Hint = "install cron, e.g. sudo apt install cron (Debian/Ubuntu) or sudo dnf install cronie (Fedora/RHEL)"
		return c
	}
	c.Status, c.Detail = OK, p
	return c
}

func checkAccess(ctx context.Context, env Env) (Check, []byte) {
	c := Check{Name: "crontab access"}
	b, err := env.Source.Read(ctx)
	if err != nil {
		c.Status, c.Detail = Fail, err.Error()
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "not allowed") || strings.Contains(msg, "cron.deny") || strings.Contains(msg, "cron.allow") {
			c.Detail = "you're not allowed to use cron"
			c.Hint = "ask an administrator to add you to /etc/cron.allow (or remove you from /etc/cron.deny)"
		}
		return c, nil
	}
	lines := bytes.Count(b, []byte("\n"))
	if len(b) > 0 && b[len(b)-1] != '\n' {
		lines++
	}
	c.Status = OK
	switch {
	case len(b) == 0:
		c.Detail = env.Source.Name() + " · empty (no crontab yet)"
	default:
		c.Detail = fmt.Sprintf("%s · %d %s", env.Source.Name(), lines, plural(lines, "line"))
	}
	if fs, ok := env.Source.(crontab.FileSource); ok {
		if err := writable(fs.Path); err != nil {
			c.Status, c.Hint = Warn, "gocron can read but not write this file: "+err.Error()
		}
	}
	return c, b
}

// writable checks that path (or, if missing, its directory) can be written
// without modifying it.
func writable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err == nil {
		return f.Close()
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gocron-doctor-*")
	if err != nil {
		return err
	}
	tmp.Close()
	return os.Remove(tmp.Name())
}

var units = []struct {
	name   string
	daemon Daemon
}{
	{"cron", DaemonDebian},
	{"cronie", DaemonCronie},
	{"crond", DaemonCronie},
}

func checkDaemon(ctx context.Context, env Env) (Check, Daemon) {
	c := Check{Name: "cron daemon"}
	if _, err := env.LookPath("systemctl"); err == nil {
		for _, u := range units {
			if out, code, err := env.Run(ctx, "systemctl", "is-active", u.name); err == nil && code == 0 && out == "active" {
				c.Status, c.Detail = OK, fmt.Sprintf("%s is active (systemd unit %s)", u.daemon, u.name)
				return c, u.daemon
			}
		}
	}
	for _, p := range env.Processes() {
		var d Daemon
		switch p {
		case "crond":
			d = DaemonCronie
		case "cron":
			d = DaemonUnknown
			if env.Exists("/etc/debian_version") {
				d = DaemonDebian
			}
		default:
			continue
		}
		c.Status, c.Detail = OK, fmt.Sprintf("%s is running (process %s)", d, p)
		return c, d
	}
	c.Status, c.Detail = Fail, "no cron daemon is running, so no jobs will run"
	c.Hint = "start it, e.g. sudo systemctl enable --now cron (or cronie / crond)"
	return c, DaemonNone
}

func checkProblems(tab *crontab.Crontab) Check {
	c := Check{Name: "crontab contents"}
	managed, unmanaged := 0, 0
	for _, j := range tab.Jobs() {
		if j.Managed {
			managed++
		} else {
			unmanaged++
		}
	}
	summary := fmt.Sprintf("%d managed %s, %d unmanaged %s", managed, plural(managed, "job"), unmanaged, plural(unmanaged, "entry"))
	problems := tab.Problems()
	if len(problems) == 0 {
		c.Status, c.Detail = OK, summary
		return c
	}
	c.Status = Warn
	c.Detail = fmt.Sprintf("%s · %d %s", summary, len(problems), plural(len(problems), "problem"))
	for _, p := range problems {
		c.More = append(c.More, p.String())
	}
	c.Hint = "cron skips invalid lines; gocron keeps them untouched until you fix them"
	return c
}

func checkCronTZ(tab *crontab.Crontab, d Daemon) (Check, bool) {
	var zones []string
	for _, e := range tab.Env() {
		if e.Name == "CRON_TZ" {
			zones = append(zones, e.Value)
		}
	}
	if len(zones) == 0 {
		return Check{}, false
	}
	c := Check{Name: "CRON_TZ"}
	switch honors, known := d.HonorsCronTZ(); {
	case known && honors:
		c.Status, c.Detail = OK, fmt.Sprintf("%s honors CRON_TZ (%s)", d, strings.Join(zones, ", "))
	case known:
		c.Status = Warn
		c.Detail = fmt.Sprintf("your crontab sets CRON_TZ=%s, but %s ignores it", strings.Join(zones, ", "), d)
		c.Hint = "jobs run in the system time zone; convert their schedules to it"
	default:
		c.Status, c.Detail = Info, "your crontab sets CRON_TZ; whether your cron daemon honors it is unknown"
	}
	return c, true
}

func checkWrappedPaths(tab *crontab.Crontab, env Env) (Check, bool) {
	c := Check{Name: "gocron path in jobs"}
	wrapped, stale, missing := 0, map[string][]string{}, map[string][]string{}
	for _, j := range tab.Jobs() {
		if !j.Wrapped {
			continue
		}
		wrapped++
		bin := j.Bin
		if !strings.Contains(bin, "/") {
			if p, err := env.LookPath(bin); err == nil {
				bin = p
			}
		}
		if r, err := filepath.EvalSymlinks(bin); err == nil {
			bin = r
		}
		switch {
		case !env.Exists(bin):
			missing[j.Bin] = append(missing[j.Bin], j.ID)
		case bin != env.SelfPath:
			stale[j.Bin] = append(stale[j.Bin], j.ID)
		}
	}
	if wrapped == 0 {
		return c, false
	}
	for _, bin := range sortedKeys(missing) {
		c.More = append(c.More, fmt.Sprintf("%s doesn't exist (jobs %s)", bin, strings.Join(missing[bin], ", ")))
	}
	for _, bin := range sortedKeys(stale) {
		c.More = append(c.More, fmt.Sprintf("%s isn't this gocron (jobs %s)", bin, strings.Join(stale[bin], ", ")))
	}
	switch {
	case len(missing) > 0:
		c.Status, c.Detail = Fail, "some jobs run a gocron binary that doesn't exist, so they fail"
		c.Hint = "reinstall gocron at that path, or update the jobs to " + env.SelfPath
	case len(stale) > 0:
		c.Status, c.Detail = Warn, "some jobs run a different gocron binary than this one"
		c.Hint = "fine if intended; otherwise update them to " + env.SelfPath
	default:
		verb := "use"
		if wrapped == 1 {
			verb = "uses"
		}
		c.Status, c.Detail = OK, fmt.Sprintf("%d wrapped %s %s this gocron", wrapped, plural(wrapped, "job"), verb)
	}
	return c, true
}

func checkMailto(tab *crontab.Crontab) Check {
	c := Check{Name: "MAILTO", Status: Info}
	var set *crontab.EnvVar
	for _, e := range tab.Env() {
		if e.Name == "MAILTO" {
			e := e
			set = &e
		}
	}
	switch {
	case set == nil:
		c.Detail = "not set: cron mails job output to your local user"
	case set.Value == "":
		c.Detail = "empty: cron mail is off"
	default:
		c.Detail = "cron mails job output to " + set.Value
	}
	return c
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	if strings.HasSuffix(word, "y") {
		return word[:len(word)-1] + "ies"
	}
	return word + "s"
}
