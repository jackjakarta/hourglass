// Package notify tells people how a cron run ended: a desktop
// notification and a webhook POST. Sending is best-effort; callers log
// the errors and carry on.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jackjakarta/gocron/internal/config"
	"github.com/jackjakarta/gocron/internal/store"
)

// Event is a finished run.
type Event struct {
	JobID     string
	Name      string // the job's name; JobID when it has none
	Status    string // a store.Status*
	ExitCode  int
	Duration  time.Duration
	Trigger   string
	StartedAt time.Time
	Host      string
	LogTail   string // the end of the run's output
	Prev      string // the previous finished run's status, "" if none
}

// Should reports whether a run that ended with status, after one that
// ended with prev, deserves a notification under the notify.on setting.
func Should(on, status, prev string) bool {
	if status == store.StatusSkipped || status == store.StatusRunning {
		return false
	}
	switch on {
	case config.NotifyFailure:
		return failed(status)
	case config.NotifyAlways:
		return true
	case config.NotifyChange:
		if prev == "" {
			return failed(status)
		}
		return failed(status) != failed(prev)
	}
	return false
}

func failed(status string) bool {
	switch status {
	case store.StatusFailed, store.StatusTimeout, store.StatusError, store.StatusLost:
		return true
	}
	return false
}

// Title is the notification's headline, e.g. "Nightly backup failed".
func (e Event) Title() string {
	name := e.Name
	if name == "" {
		name = e.JobID
	}
	switch e.Status {
	case store.StatusSuccess:
		if failed(e.Prev) {
			return name + " is working again"
		}
		return name + " succeeded"
	case store.StatusTimeout:
		return name + " timed out"
	case store.StatusError:
		return name + " couldn't start"
	}
	return name + " failed"
}

// Body is the notification's detail: exit code, duration and the last
// line of output.
func (e Event) Body() string {
	parts := []string{}
	if e.Status != store.StatusSuccess {
		parts = append(parts, fmt.Sprintf("exit %d", e.ExitCode))
	}
	parts = append(parts, "after "+e.Duration.Round(time.Second).String())
	s := strings.Join(parts, " ")
	if last := lastLine(e.LogTail); last != "" {
		s += "\n" + last
	}
	return s
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	if r := []rune(l); len(r) > 200 {
		l = string(r[:199]) + "…"
	}
	return l
}

// Sender delivers notifications. The zero value sends nothing.
type Sender struct {
	Desktop bool
	Webhook string
	// UserAgent identifies gocron to the webhook.
	UserAgent string
	// Client posts to the webhook; nil uses a client with a 10s timeout.
	Client *http.Client
	// Command runs a desktop notifier; nil runs it for real. env is the
	// complete environment for the command.
	Command func(ctx context.Context, env []string, name string, args ...string) error
	// GOOS picks the desktop notifier; "" means runtime.GOOS.
	GOOS string
	// Environ is the environment the desktop notifier starts from; nil
	// means os.Environ().
	Environ []string
}

// Send delivers e to every configured target at once and returns what
// failed.
func (s Sender) Send(ctx context.Context, e Event) []error {
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	do := func(what string, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s notification: %w", what, err))
				mu.Unlock()
			}
		}()
	}
	if s.Desktop {
		do("desktop", func() error { return s.desktop(ctx, e) })
	}
	if s.Webhook != "" {
		do("webhook", func() error { return s.webhook(ctx, e) })
	}
	wg.Wait()
	return errs
}

func (s Sender) desktop(ctx context.Context, e Event) error {
	run := s.Command
	if run == nil {
		run = runCommand
	}
	env := s.Environ
	if env == nil {
		env = os.Environ()
	}
	goos := s.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	switch goos {
	case "darwin":
		script := fmt.Sprintf("display notification %s with title %s", appleString(e.Body()), appleString("gocron: "+e.Title()))
		return run(ctx, env, "osascript", "-e", script)
	default:
		urgency := "normal"
		if failed(e.Status) {
			urgency = "critical"
		}
		return run(ctx, sessionBus(env), "notify-send", "-a", "gocron", "-u", urgency, "gocron: "+e.Title(), e.Body())
	}
}

// sessionBus adds the user's D-Bus session address to env when it's
// missing, as it is under cron, so notify-send can reach the desktop.
func sessionBus(env []string) []string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "DBUS_SESSION_BUS_ADDRESS=") {
			return env
		}
	}
	bus := fmt.Sprintf("/run/user/%d/bus", os.Getuid())
	if _, err := os.Stat(bus); err != nil {
		return env
	}
	return append(env[:len(env):len(env)], "DBUS_SESSION_BUS_ADDRESS=unix:path="+bus)
}

// appleString quotes s as an AppleScript string literal.
func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func runCommand(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil && len(bytes.TrimSpace(out)) > 0 {
		return fmt.Errorf("%s: %w: %s", name, err, bytes.TrimSpace(out))
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Payload is the JSON body POSTed to the webhook.
type Payload struct {
	Job        string `json:"job"`
	JobID      string `json:"job_id"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	Duration   string `json:"duration"`
	DurationMS int64  `json:"duration_ms"`
	Trigger    string `json:"trigger"`
	StartedAt  string `json:"started_at"`
	Host       string `json:"host"`
	PrevStatus string `json:"prev_status,omitempty"`
	LogTail    string `json:"log_tail"`
}

func (s Sender) webhook(ctx context.Context, e Event) error {
	body, err := json.Marshal(Payload{
		Job:        e.Name,
		JobID:      e.JobID,
		Status:     e.Status,
		ExitCode:   e.ExitCode,
		Duration:   e.Duration.Round(time.Millisecond).String(),
		DurationMS: e.Duration.Milliseconds(),
		Trigger:    e.Trigger,
		StartedAt:  e.StartedAt.UTC().Format(time.RFC3339),
		Host:       e.Host,
		PrevStatus: e.Prev,
		LogTail:    e.LogTail,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("POST %s: %s", s.Webhook, resp.Status)
	}
	return nil
}

// Tail returns up to the last n lines of the file at path, reading at most
// max bytes from its end. A missing file gives "".
func Tail(path string, n int, max int64) (string, error) {
	if path == "" {
		return "", nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	off := st.Size() - max
	if off < 0 {
		off = 0
	}
	b := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if off > 0 {
		// Drop the partial first line.
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	lines := strings.Split(strings.TrimRight(strings.ToValidUTF8(string(b), "�"), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}
