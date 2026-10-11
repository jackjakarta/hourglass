package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackjakarta/gocron/internal/crontab"
)

// fakeSource is a non-file Source, standing in for the real crontab.
type fakeSource struct {
	content string
	err     error
}

func (f fakeSource) Read(context.Context) ([]byte, error) { return []byte(f.content), f.err }
func (f fakeSource) Write(context.Context, []byte) error  { return errors.New("read-only") }
func (f fakeSource) Name() string                         { return "user crontab" }

type fakeSystem struct {
	paths   map[string]string // LookPath results
	active  map[string]bool   // systemd units
	procs   []string
	files   map[string]bool
	runErrs bool
}

func (f fakeSystem) env(src crontab.Source) Env {
	return Env{
		Source: src,
		LookPath: func(name string) (string, error) {
			if p, ok := f.paths[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) (string, int, error) {
			if name == "systemctl" && len(args) == 2 && f.active[args[1]] {
				return "active", 0, nil
			}
			return "inactive", 3, nil
		},
		Processes: func() []string { return f.procs },
		Exists:    func(p string) bool { return f.files[p] },
		SelfPath:  "/usr/local/bin/gocron",
	}
}

var debianBox = fakeSystem{
	paths:  map[string]string{"crontab": "/usr/bin/crontab", "systemctl": "/usr/bin/systemctl", "gocron": "/usr/local/bin/gocron"},
	active: map[string]bool{"cron": true},
	files:  map[string]bool{"/usr/local/bin/gocron": true, "/old/gocron": true},
}

func find(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q check in %+v", name, checks)
	return Check{}
}

func TestHealthySystem(t *testing.T) {
	tab := "MAILTO=\"\"\n# gocron:job id=abc234\n0 0 * * * /usr/local/bin/gocron exec abc234 -- 'x'\n*/5 * * * * y\n"
	checks := Run(context.Background(), debianBox.env(fakeSource{content: tab}))
	if Failed(checks) {
		t.Fatalf("healthy system failed: %+v", checks)
	}
	for name, want := range map[string]string{
		"crontab command":     "/usr/bin/crontab",
		"crontab access":      "user crontab · 4 lines",
		"cron daemon":         "Debian cron is active (systemd unit cron)",
		"crontab contents":    "1 managed job, 1 unmanaged entry",
		"gocron path in jobs": "1 wrapped job uses this gocron",
		"MAILTO":              "empty: cron mail is off",
	} {
		if got := find(t, checks, name).Detail; got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

func TestAccessDenied(t *testing.T) {
	src := fakeSource{err: errors.New("crontab -l: You (jack) are not allowed to use this program (crontab)")}
	checks := Run(context.Background(), debianBox.env(src))
	c := find(t, checks, "crontab access")
	if c.Status != Fail || !strings.Contains(c.Hint, "cron.allow") {
		t.Errorf("access check = %+v", c)
	}
	if !Failed(checks) {
		t.Error("Failed = false")
	}
}

func TestNoCrontabBinaryOrDaemon(t *testing.T) {
	sys := fakeSystem{}
	checks := Run(context.Background(), sys.env(fakeSource{}))
	if c := find(t, checks, "crontab command"); c.Status != Fail {
		t.Errorf("binary check = %+v", c)
	}
	if c := find(t, checks, "cron daemon"); c.Status != Fail || !strings.Contains(c.Hint, "systemctl enable") {
		t.Errorf("daemon check = %+v", c)
	}
	if c := find(t, checks, "crontab access"); c.Detail != "user crontab · empty (no crontab yet)" {
		t.Errorf("access = %+v", c)
	}
}

func TestDaemonFromProcesses(t *testing.T) {
	tests := []struct {
		procs  []string
		files  map[string]bool
		want   string
		daemon Daemon
	}{
		{[]string{"bash", "crond"}, nil, "cronie is running (process crond)", DaemonCronie},
		{[]string{"cron"}, map[string]bool{"/etc/debian_version": true}, "Debian cron is running (process cron)", DaemonDebian},
		{[]string{"cron"}, nil, "cron is running (process cron)", DaemonUnknown},
	}
	for _, tt := range tests {
		c, d := checkDaemon(context.Background(), fakeSystem{procs: tt.procs, files: tt.files}.env(fakeSource{}))
		if c.Detail != tt.want || d != tt.daemon || c.Status != OK {
			t.Errorf("procs %v: %+v %q", tt.procs, c, d)
		}
	}
}

func TestCronTZ(t *testing.T) {
	tab := crontab.Parse([]byte("CRON_TZ=Europe/Berlin\n0 0 * * * x\n"))
	if c, _ := checkCronTZ(tab, DaemonDebian); c.Status != Warn || !strings.Contains(c.Detail, "ignores it") {
		t.Errorf("debian: %+v", c)
	}
	if c, _ := checkCronTZ(tab, DaemonCronie); c.Status != OK {
		t.Errorf("cronie: %+v", c)
	}
	if c, _ := checkCronTZ(tab, DaemonUnknown); c.Status != Info {
		t.Errorf("unknown: %+v", c)
	}
	if _, ok := checkCronTZ(crontab.Parse([]byte("0 0 * * * x\n")), DaemonDebian); ok {
		t.Error("CRON_TZ check shown without CRON_TZ")
	}
}

func TestProblemsAndPaths(t *testing.T) {
	tab := strings.Join([]string{
		"# gocron:job id=aaa222",
		"0 0 * * * /old/gocron exec aaa222 -- 'x'",
		"# gocron:job id=aaa222",
		"0 1 * * * /gone/gocron exec aaa222 -- 'y'",
		"# gocron:job id=orphan",
		"",
		"bogus line",
	}, "\n") + "\n"
	checks := Run(context.Background(), debianBox.env(fakeSource{content: tab}))
	c := find(t, checks, "crontab contents")
	if c.Status != Warn || len(c.More) != 3 {
		t.Errorf("problems = %+v", c)
	}
	p := find(t, checks, "gocron path in jobs")
	if p.Status != Fail || strings.Join(p.More, "|") != "/gone/gocron doesn't exist (jobs aaa222)|/old/gocron isn't this gocron (jobs aaa222)" {
		t.Errorf("paths = %+v", p)
	}
}

func TestMailto(t *testing.T) {
	for tab, want := range map[string]string{
		"":                        "not set: cron mails job output to your local user",
		"MAILTO=me@example.com\n": "cron mails job output to me@example.com",
	} {
		if c := checkMailto(crontab.Parse([]byte(tab))); c.Detail != want {
			t.Errorf("%q: %q", tab, c.Detail)
		}
	}
}

func TestFileSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crontab")
	if err := os.WriteFile(path, []byte("0 0 * * * x"), 0o600); err != nil {
		t.Fatal(err)
	}
	checks := Run(context.Background(), debianBox.env(crontab.FileSource{Path: path}))
	for _, c := range checks {
		if c.Name == "crontab command" {
			t.Error("crontab binary checked for a file source")
		}
	}
	if c := find(t, checks, "crontab access"); c.Status != OK || c.Detail != "file: "+path+" · 1 line" {
		t.Errorf("access = %+v", c)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 {
		if c := find(t, Run(context.Background(), debianBox.env(crontab.FileSource{Path: path})), "crontab access"); c.Status != Warn {
			t.Errorf("read-only file: %+v", c)
		}
	}
}
