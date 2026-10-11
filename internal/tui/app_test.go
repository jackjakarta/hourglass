package tui

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/jackjakarta/gocron/internal/config"
	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/doctor"
	"github.com/jackjakarta/gocron/internal/jobs"
	"github.com/jackjakarta/gocron/internal/runner"
)

// pkgDir is the package directory; tests chdir away from it.
var pkgDir string

// updating reports -update, which teatest's golden package defines.
func updating() bool {
	f := flag.Lookup("update")
	return f != nil && f.Value.String() == "true"
}

func TestMain(m *testing.M) {
	time.Local = time.UTC
	pkgDir, _ = os.Getwd()
	os.Exit(m.Run())
}

// requireGolden compares a screen with testdata/<test name>.golden.
func requireGolden(t *testing.T, screen string) {
	t.Helper()
	path := filepath.Join(pkgDir, "testdata", t.Name()+".golden")
	if updating() {
		if err := os.WriteFile(path, []byte(screen), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if screen != string(want) {
		t.Errorf("screen doesn't match %s (go test -update rewrites it)\ngot:\n%s\nwant:\n%s", path, screen, want)
	}
}

// Sun 11 Oct 2026 14:32:07 UTC
var testNow = time.Date(2026, 10, 11, 14, 32, 7, 0, time.UTC)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type harness struct {
	t       *testing.T
	path    string // the crontab file
	backups string
	tm      *teatest.TestModel
	screenV *atomic.Value // the latest rendered screen, unstyled
}

// spy publishes every rendered screen so tests can wait for UI states
// rather than parse the renderer's cursor-addressed output.
type spy struct {
	Model
	screen *atomic.Value
}

func (s spy) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := s.Model.Update(msg)
	s.Model = m.(Model)
	return s, cmd
}

func (s spy) View() tea.View {
	v := s.Model.View()
	s.screen.Store(ansi.Strip(v.Content))
	return v
}

// start runs the TUI against a copy of fixture (or an empty crontab when
// fixture is "") at w×h.
func start(t *testing.T, fixture string, w, h int) *harness {
	t.Helper()
	return startWith(t, fixture, w, h, nil)
}

// startWith is start with a hook to adjust the options.
func startWith(t *testing.T, fixture string, w, h int, adjust func(*Options)) *harness {
	t.Helper()
	return startFull(t, fixture, w, h, nil, adjust)
}

// startFull is start with hooks for the service's options and the TUI's.
func startFull(t *testing.T, fixture string, w, h int, svcOpts func(*jobs.Options), adjust func(*Options)) *harness {
	t.Helper()
	var content []byte
	if c, ok := strings.CutPrefix(fixture, "inline:"); ok {
		content, fixture = []byte(c), ""
	}
	if fixture != "" {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "crontabs", fixture+".crontab"))
		if err != nil {
			t.Fatal(err)
		}
		content = b
	}
	// A relative path keeps the source name ("file: crontab") stable.
	t.Chdir(t.TempDir())
	hs := &harness{t: t, path: "crontab", backups: "backups"}
	if content != nil {
		hs.write(string(content))
	}
	store, err := crontab.Open(context.Background(), crontab.FileSource{Path: hs.path}, crontab.Options{BackupDir: hs.backups})
	if err != nil {
		t.Fatal(err)
	}
	jo := jobs.Options{
		Bin:      "/usr/bin/gocron",
		Defaults: config.Default().Defaults,
		Identity: &runner.Identity{Name: "jack", Home: "/home/jack"},
		Environ:  []string{"HOME=/home/jack", "PATH=/usr/local/bin:/usr/bin:/bin"},
	}
	if svcOpts != nil {
		svcOpts(&jo)
	}
	opts := Options{Service: jobs.New(store, jo)}
	if adjust != nil {
		adjust(&opts)
	}
	hs.run(opts, w, h)
	return hs
}

func (hs *harness) run(opts Options, w, h int) {
	opts.Clock = fixedClock{testNow}
	if opts.PathLint == nil { // never look at this machine's filesystem
		opts.PathLint = func(string, jobs.CronEnv) []jobs.Issue { return nil }
	}
	opts.Daemon = func(context.Context) doctor.DaemonStatus {
		return doctor.DaemonStatus{Daemon: doctor.DaemonCronie, Running: true, Via: "process crond"}
	}
	hs.screenV = &atomic.Value{}
	hs.screenV.Store("")
	hs.tm = teatest.NewTestModel(hs.t, spy{New(context.Background(), opts), hs.screenV}, teatest.WithInitialTermSize(w, h))
}

func (hs *harness) write(s string) {
	hs.t.Helper()
	if err := os.WriteFile(hs.path, []byte(s), 0o600); err != nil {
		hs.t.Fatal(err)
	}
}

func (hs *harness) read() string {
	hs.t.Helper()
	b, err := os.ReadFile(hs.path)
	if err != nil {
		hs.t.Fatal(err)
	}
	return string(b)
}

func (hs *harness) backupCount() int {
	entries, _ := os.ReadDir(hs.backups)
	return len(entries)
}

// waitFor waits until the screen shows s.
func (hs *harness) waitFor(s string) {
	hs.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		screen := hs.screenV.Load().(string)
		if strings.Contains(screen, s) {
			return
		}
		if time.Now().After(deadline) {
			hs.t.Fatalf("screen never showed %q; last screen:\n%s", s, screen)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (hs *harness) keys(keys ...string) {
	for _, k := range keys {
		switch k {
		case "enter":
			hs.tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "esc":
			hs.tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
		case "down":
			hs.tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
		case "up":
			hs.tm.Send(tea.KeyPressMsg{Code: tea.KeyUp})
		case "left":
			hs.tm.Send(tea.KeyPressMsg{Code: tea.KeyLeft})
		case "right":
			hs.tm.Send(tea.KeyPressMsg{Code: tea.KeyRight})
		case "tab":
			hs.tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
		case "backspace":
			hs.tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
		case "ctrl+s", "ctrl+t", "ctrl+u", "ctrl+k":
			hs.tm.Send(tea.KeyPressMsg{Code: rune(k[len(k)-1]), Mod: tea.ModCtrl})
		case "space":
			hs.tm.Send(tea.KeyPressMsg{Code: ' ', Text: " "})
		default:
			hs.tm.Type(k)
		}
	}
}

// screen quits and returns the final screen without styling, trailing
// spaces trimmed.
func (hs *harness) screen() string {
	hs.t.Helper()
	if err := hs.tm.Quit(); err != nil {
		hs.t.Fatal(err)
	}
	m := hs.tm.FinalModel(hs.t, teatest.WithFinalTimeout(5*time.Second)).(spy).Model
	lines := strings.Split(ansi.Strip(m.render()), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestGoldenListWide(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	requireGolden(t, hs.screen())
}

func TestGoldenListNarrow(t *testing.T) {
	hs := start(t, "realistic_mixed", 100, 32)
	hs.waitFor("Nightly backup")
	requireGolden(t, hs.screen())
}

func TestGoldenListCompact(t *testing.T) {
	hs := start(t, "realistic_mixed", 70, 24)
	hs.waitFor("Nightly backup")
	requireGolden(t, hs.screen())
}

func TestGoldenDetailNarrow(t *testing.T) {
	hs := start(t, "realistic_mixed", 100, 32)
	hs.waitFor("Nightly backup")
	hs.keys("down", "down", "enter")
	hs.waitFor("SCHEDULE")
	requireGolden(t, hs.screen())
}

func TestGoldenEmpty(t *testing.T) {
	hs := start(t, "", 100, 24)
	hs.waitFor("Welcome to gocron")
	requireGolden(t, hs.screen())
}

func TestGoldenHelp(t *testing.T) {
	hs := start(t, "realistic_mixed", 100, 32)
	hs.waitFor("Nightly backup")
	hs.keys("?")
	hs.waitFor("? or esc to close")
	requireGolden(t, hs.screen())
}

func TestGoldenTooSmall(t *testing.T) {
	hs := &harness{t: t}
	hs.run(Options{LoadErr: errors.New("unused")}, 50, 12)
	hs.waitFor("Make me a bit bigger")
	requireGolden(t, hs.screen())
}

func TestLoadError(t *testing.T) {
	hs := &harness{t: t}
	hs.run(Options{LoadErr: errors.New("crontab -l: You (jack) are not allowed to use this program")}, 100, 20)
	hs.waitFor("can't read your crontab")
	hs.keys("space", "u") // no service: nothing happens
	s := hs.screen()
	for _, want := range []string{"not allowed to use this program", "gocron doctor", "q quit"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
}

func TestComingSoonTabs(t *testing.T) {
	hs := start(t, "realistic_mixed", 100, 24)
	hs.waitFor("Nightly backup")
	hs.keys("4")
	hs.waitFor("Your raw crontab")
	if s := hs.screen(); !strings.Contains(s, "Coming soon") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestPauseAndUndo(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	orig := hs.read()
	hs.waitFor("Nightly backup")

	// The first row is the next job to run: Sync photos.
	hs.keys("space")
	hs.waitFor(`Paused "Sync photos" · backup created · u undo`)
	if !strings.Contains(hs.read(), "#gocron:off */15 * * * * ") {
		t.Errorf("not paused:\n%s", hs.read())
	}

	hs.keys("u")
	hs.waitFor("Undid the last change")
	if got := hs.read(); got != orig {
		t.Errorf("undo didn't restore the crontab:\n%s", got)
	}
	if n := hs.backupCount(); n != 2 {
		t.Errorf("%d backups, want 2", n)
	}
	hs.keys("u")
	hs.waitFor("Nothing to undo")
	hs.screen()
}

func TestDeleteConfirm(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	orig := hs.read()
	hs.waitFor("Nightly backup")

	hs.keys("x")
	hs.waitFor("Delete this job?")
	hs.keys("enter") // the safe button has focus
	hs.keys("x")
	hs.waitFor("Delete this job?")
	hs.keys("esc")
	if hs.read() != orig || hs.backupCount() != 0 {
		t.Fatal("cancelled delete wrote the crontab")
	}

	hs.keys("x", "right", "enter")
	hs.waitFor(`Deleted "Sync photos"`)
	if strings.Contains(hs.read(), "s7nc0p") {
		t.Errorf("job still in the crontab:\n%s", hs.read())
	}
	if s := hs.screen(); strings.Contains(s, "Sync photos") && !strings.Contains(s, `Deleted "Sync photos"`) {
		t.Errorf("deleted job still listed:\n%s", s)
	}
}

func TestAdopt(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	hs.keys("G", "a") // last row: the unmanaged first-or-monday.sh entry
	hs.waitFor(`Adopted "first-or-monday.sh"`)
	got := hs.read()
	// Adopting wraps the entry to track its runs (defaults.wrap).
	if ok, _ := regexp.MatchString(`# gocron:job id=([a-z2-7]{6}) name=first-or-monday.sh\n0 0 1 \* 1 /usr/bin/gocron exec [a-z2-7]{6} -- '~/bin/first-or-monday.sh'\n$`, got); !ok {
		t.Errorf("adopt:\n%s", got)
	}
	s := hs.screen()
	if !strings.Contains(s, "▌● first-or-monday.sh") || !strings.Contains(s, "UNMANAGED · 2") {
		t.Errorf("adopted job isn't selected among the managed jobs:\n%s", s)
	}
}

func TestSpaceOnUnmanagedAsksToAdopt(t *testing.T) {
	hs := start(t, "simple", 100, 24)
	orig := hs.read()
	hs.waitFor("UNMANAGED · 3")
	hs.keys("space")
	hs.waitFor("Press a to adopt it first")
	hs.screen()
	if hs.read() != orig {
		t.Error("crontab changed")
	}
}

func TestExternalChangeReloads(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	hs.write(hs.read() + "# gocron:job id=zz2345 name=\"Added by hand\"\n0 6 * * * ~/bin/morning.sh\n")
	hs.tm.Send(tea.FocusMsg{})
	hs.waitFor("Crontab reloaded (changed outside gocron)")
	if s := hs.screen(); !strings.Contains(s, "Added by hand") || !strings.Contains(s, "Jobs · 7") {
		t.Errorf("reloaded list:\n%s", s)
	}
}

func TestConflictReapply(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	external := hs.read() + "# by hand\n"
	hs.write(external)
	hs.keys("space")
	hs.waitFor("Crontab changed outside gocron")
	hs.keys("right", "enter")
	hs.waitFor(`Paused "Sync photos"`)
	want := strings.Replace(external, "*/15 * * * * /home", "#gocron:off */15 * * * * /home", 1)
	if got := hs.read(); got != want {
		t.Errorf("reapply:\n%s", got)
	}
	hs.screen()
}

func TestFilterCapturesKeys(t *testing.T) {
	hs := start(t, "realistic_mixed", 140, 32)
	hs.waitFor("Nightly backup")
	hs.keys("/", "q", "x", "z")
	hs.waitFor(`No jobs match "qxz"`)
	hs.keys("esc", "/", "d", "o", "c", "k", "enter")
	hs.waitFor("/ dock")
	s := hs.screen()
	if !strings.Contains(s, "Prune docker") || strings.Contains(s, "Nightly backup") {
		t.Errorf("filtered list:\n%s", s)
	}
}

func TestNoColorUsesMono(t *testing.T) {
	if m := New(context.Background(), Options{NoColor: true}); !m.th.Mono {
		t.Error("NO_COLOR didn't select the mono theme")
	}
	m, _ := New(context.Background(), Options{NoColor: true}).Update(tea.BackgroundColorMsg{})
	if !m.(Model).th.Mono {
		t.Error("background detection replaced the mono theme")
	}
	if m := New(context.Background(), Options{}); m.th.Mono || m.th.Name != "nightshift" {
		t.Errorf("default theme = %s", m.th.Name)
	}
}
