package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackjakarta/hourglass/internal/store"
)

func TestShould(t *testing.T) {
	const (
		ok   = store.StatusSuccess
		fail = store.StatusFailed
		tout = store.StatusTimeout
		skip = store.StatusSkipped
	)
	for _, tt := range []struct {
		on, status, prev string
		want             bool
	}{
		{"never", fail, "", false},
		{"failure", fail, "", true},
		{"failure", tout, ok, true},
		{"failure", store.StatusError, ok, true},
		{"failure", ok, fail, false},
		{"failure", skip, "", false},
		{"always", ok, ok, true},
		{"always", fail, fail, true},
		{"always", skip, ok, false},
		{"change", fail, ok, true},
		{"change", ok, fail, true},
		{"change", ok, tout, true},
		{"change", fail, fail, false},
		{"change", tout, fail, false},
		{"change", ok, ok, false},
		{"change", fail, "", true},
		{"change", ok, "", false},
		{"change", skip, fail, false},
		{"bogus", fail, "", false},
	} {
		if got := Should(tt.on, tt.status, tt.prev); got != tt.want {
			t.Errorf("Should(%q, %q, %q) = %v, want %v", tt.on, tt.status, tt.prev, got, tt.want)
		}
	}
}

func event() Event {
	return Event{
		JobID: "k3f9a2", Name: "Nightly backup", Status: store.StatusFailed, ExitCode: 2,
		Duration: 12*time.Second + 340*time.Millisecond, Trigger: store.TriggerCron,
		StartedAt: time.Date(2026, 10, 11, 2, 30, 0, 0, time.UTC), Host: "box",
		LogTail: "dumping…\npg_dump: connection refused\n", Prev: store.StatusSuccess,
	}
}

func TestTitleAndBody(t *testing.T) {
	e := event()
	if e.Title() != "Nightly backup failed" || e.Body() != "exit 2 after 12s\npg_dump: connection refused" {
		t.Errorf("title %q body %q", e.Title(), e.Body())
	}
	e.Status, e.Prev, e.LogTail = store.StatusSuccess, store.StatusTimeout, ""
	if e.Title() != "Nightly backup is working again" || e.Body() != "after 12s" {
		t.Errorf("recovery: title %q body %q", e.Title(), e.Body())
	}
	e.Status, e.Name = store.StatusTimeout, ""
	if e.Title() != "k3f9a2 timed out" {
		t.Errorf("unnamed timeout: %q", e.Title())
	}
}

type call struct {
	env  []string
	name string
	args []string
}

func recorder(calls *[]call, mu *sync.Mutex) func(context.Context, []string, string, ...string) error {
	return func(_ context.Context, env []string, name string, args ...string) error {
		mu.Lock()
		defer mu.Unlock()
		*calls = append(*calls, call{env, name, args})
		return nil
	}
}

func TestDesktop(t *testing.T) {
	var calls []call
	var mu sync.Mutex
	s := Sender{Desktop: true, Command: recorder(&calls, &mu), GOOS: "linux", Environ: []string{"DBUS_SESSION_BUS_ADDRESS=x"}}
	if errs := s.Send(context.Background(), event()); len(errs) > 0 {
		t.Fatal(errs)
	}
	want := []string{"-a", "hourglass", "-u", "critical", "hourglass: Nightly backup failed", "exit 2 after 12s\npg_dump: connection refused"}
	if len(calls) != 1 || calls[0].name != "notify-send" || strings.Join(calls[0].args, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %+v", calls)
	}

	calls = nil
	s.GOOS = "darwin"
	e := event()
	e.Name = `say "hi"`
	s.Send(context.Background(), e)
	if len(calls) != 1 || calls[0].name != "osascript" ||
		calls[0].args[1] != "display notification \"exit 2 after 12s\npg_dump: connection refused\" with title \"hourglass: say \\\"hi\\\" failed\"" {
		t.Errorf("calls = %q", calls)
	}
}

func TestDesktopError(t *testing.T) {
	s := Sender{Desktop: true, GOOS: "linux", Environ: []string{"PATH=" + t.TempDir()}}
	errs := s.Send(context.Background(), event())
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "desktop notification") {
		t.Errorf("errs = %v", errs)
	}
}

func TestWebhook(t *testing.T) {
	var got Payload
	var ua, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua, ctype = r.Header.Get("User-Agent"), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	s := Sender{Webhook: srv.URL, UserAgent: "hourglass/1.2.3"}
	if errs := s.Send(context.Background(), event()); len(errs) > 0 {
		t.Fatal(errs)
	}
	want := Payload{
		Job: "Nightly backup", JobID: "k3f9a2", Status: "failed", ExitCode: 2, Duration: "12.34s", DurationMS: 12340,
		Trigger: "cron", StartedAt: "2026-10-11T02:30:00Z", Host: "box", PrevStatus: "success",
		LogTail: "dumping…\npg_dump: connection refused\n",
	}
	if got != want {
		t.Errorf("payload = %+v\nwant      %+v", got, want)
	}
	if ua != "hourglass/1.2.3" || ctype != "application/json" {
		t.Errorf("headers: UA %q, Content-Type %q", ua, ctype)
	}
}

func TestWebhookErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	errs := Sender{Webhook: srv.URL}.Send(context.Background(), event())
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "500") {
		t.Errorf("errs = %v", errs)
	}

	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer hang.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	errs = Sender{Webhook: hang.URL}.Send(ctx, event())
	if len(errs) != 1 || time.Since(start) > 5*time.Second {
		t.Errorf("hanging webhook: errs = %v after %s", errs, time.Since(start))
	}
}

func TestTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	var b strings.Builder
	for i := range 30 {
		b.WriteString(strings.Repeat("x", i) + "\n")
	}
	os.WriteFile(p, []byte(b.String()), 0o600)
	got, err := Tail(p, 3, 4096)
	if err != nil || got != strings.Repeat("x", 27)+"\n"+strings.Repeat("x", 28)+"\n"+strings.Repeat("x", 29) {
		t.Errorf("Tail = %q, %v", got, err)
	}
	// Reading only the end drops the cut-off line.
	got, _ = Tail(p, 20, 40)
	if got != strings.Repeat("x", 29) {
		t.Errorf("Tail with a small max = %q", got)
	}
	if got, err := Tail(filepath.Join(t.TempDir(), "missing"), 3, 100); got != "" || err != nil {
		t.Errorf("missing file: %q, %v", got, err)
	}
}
