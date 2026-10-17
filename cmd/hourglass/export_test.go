package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackjakarta/hourglass/internal/crontab"
)

func copyFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(fixture(name))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "crontab")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExportGolden(t *testing.T) {
	out, errOut, code := runCLI(t, fixture("realistic_mixed"), "export")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	golden := filepath.Join("testdata", "export_realistic_mixed.yaml")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := readFile(t, golden); out != want {
		t.Errorf("export:\n%s\nwant:\n%s", out, want)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	src := copyFixture(t, "realistic_mixed")
	file := filepath.Join(t.TempDir(), "jobs.yaml")
	out, errOut, code := runCLI(t, src, "export", "--file", file)
	if code != 0 || !strings.Contains(out, "Exported 6 jobs") {
		t.Fatalf("export --file: code=%d out=%q stderr=%q", code, out, errOut)
	}
	exported := readFile(t, file)

	// Into the same crontab, nothing changes.
	before := readFile(t, src)
	out, errOut, code = runCLI(t, src, "import", "--dry-run", file)
	if code != 0 || !strings.Contains(out, "Dry run: 0 to create, 0 to update, 6 unchanged") || errOut != "" {
		t.Errorf("dry run on the source: code=%d out=%q stderr=%q", code, out, errOut)
	}
	out, _, code = runCLI(t, src, "import", file)
	if code != 0 || !strings.Contains(out, "Nothing to import: 6 unchanged") || readFile(t, src) != before {
		t.Errorf("import into the source: code=%d out=%q", code, out)
	}

	// Into an empty crontab: a dry run writes nothing, then the import
	// recreates every job with the same id.
	dst := filepath.Join(t.TempDir(), "crontab")
	if err := os.WriteFile(dst, []byte("MAILTO=\"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code = runCLI(t, dst, "import", "--dry-run", file)
	if code != 0 || !strings.Contains(out, "6 to create") || !strings.Contains(out, "+ k3f9a2 \"Nightly backup\"") || readFile(t, dst) != "MAILTO=\"\"\n" {
		t.Errorf("dry run into empty: code=%d out=%q", code, out)
	}
	out, errOut, code = runCLI(t, dst, "import", file)
	if code != 0 || !strings.Contains(out, "Imported: 6 created, 0 updated, 0 unchanged · backup:") {
		t.Fatalf("import into empty: code=%d out=%q stderr=%q", code, out, errOut)
	}
	if got := readFile(t, dst); !strings.Contains(got, "#hourglass:off 0 9 * * 1 "+testBin+" exec wkrp7t") ||
		!strings.Contains(got, `desc="Dump app DB to /bk"`) {
		t.Errorf("imported crontab:\n%s", got)
	}
	again, _, _ := runCLI(t, dst, "export")
	if again != exported {
		t.Errorf("re-export differs:\n%s\nwant:\n%s", again, exported)
	}
}

func TestImportUpdatesOnlyChangedJobs(t *testing.T) {
	src := copyFixture(t, "realistic_mixed")
	out, _, _ := runCLI(t, src, "export")
	edited := strings.Replace(out, "name: Prune docker", "name: Prune images", 1)
	file := filepath.Join(t.TempDir(), "jobs.yaml")
	if err := os.WriteFile(file, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, src)
	out, errOut, code := runCLI(t, src, "import", file)
	if code != 0 || !strings.Contains(out, `~ pdk4r2 "Prune images" (name)`) || !strings.Contains(out, "0 created, 1 updated, 5 unchanged") {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, errOut)
	}
	want := strings.Replace(before, `name="Prune docker"`, `name="Prune images"`, 1)
	if got := readFile(t, src); got != want {
		t.Errorf("crontab after import:\n%s\nwant:\n%s", got, want)
	}
}

func TestImportRejects(t *testing.T) {
	head := "version: 1\njobs:\n"
	job := "  - id: abc234\n    schedule: \"0 3 * * *\"\n    command: backup\n    enabled: true\n    wrapped: false\n"
	tests := []struct {
		name, doc, stderr string
	}{
		{"bad schedule", head + strings.Replace(job, "0 3 * * *", "0 25 * * *", 1), "✗ abc234:"},
		{"unknown field", head + job + "    color: red\n", "field color not found"},
		{"duplicate id", head + job + job, "job id abc234 appears twice"},
		{"version 2", strings.Replace(head, "1", "2", 1) + job, "export version 2 isn't supported"},
		{"no version", "jobs: []\n", "no version"},
		{"bad id", head + strings.Replace(job, "abc234", "../x", 1), `id "../x"`},
		{"bad timeout", head + job + "    timeout: soon\n", `timeout "soon"`},
	}
	for _, tt := range tests {
		dst := filepath.Join(t.TempDir(), "crontab")
		if err := os.WriteFile(dst, []byte("MAILTO=\"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "jobs.yaml")
		if err := os.WriteFile(file, []byte(tt.doc), 0o600); err != nil {
			t.Fatal(err)
		}
		_, errOut, code := runCLI(t, dst, "import", file)
		if code != exitError || !strings.Contains(errOut, tt.stderr) {
			t.Errorf("%s: code=%d stderr=%q", tt.name, code, errOut)
		}
		if readFile(t, dst) != "MAILTO=\"\"\n" {
			t.Errorf("%s: the crontab was written", tt.name)
		}
	}
	if _, _, code := runCLI(t, fixture("empty"), "import"); code != exitUsage {
		t.Errorf("import without a file: code %d", code)
	}
}

// racySource changes the crontab on disk right after hourglass loads it.
type racySource struct {
	crontab.FileSource
	reads int
}

func (s *racySource) Read(ctx context.Context) ([]byte, error) {
	b, err := s.FileSource.Read(ctx)
	if s.reads++; s.reads == 1 && err == nil {
		os.WriteFile(s.Path, append(append([]byte(nil), b...), "# by hand\n"...), 0o600)
	}
	return b, err
}

func TestImportConflict(t *testing.T) {
	src := copyFixture(t, "realistic_mixed")
	out, _, _ := runCLI(t, src, "export")
	file := filepath.Join(t.TempDir(), "jobs.yaml")
	os.WriteFile(file, []byte(strings.Replace(out, "name: Prune docker", "name: Prune images", 1)), 0o600)

	t.Setenv("HOURGLASS_STATE_DIR", t.TempDir())
	var o, e bytes.Buffer
	a := &app{stdout: &o, stderr: &e, now: func() time.Time { return testNow }, bin: func() string { return testBin },
		source: func() (crontab.Source, error) { return &racySource{FileSource: crontab.FileSource{Path: src}}, nil }}
	if code := a.run(context.Background(), []string{"import", file}); code != exitConflict {
		t.Errorf("code = %d, stderr = %q", code, e.String())
	}
	if got := readFile(t, src); strings.Contains(got, "Prune images") || !strings.Contains(got, "# by hand") {
		t.Errorf("the import overwrote the outside change:\n%s", got)
	}
}
