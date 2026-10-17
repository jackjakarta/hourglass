package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCrontab(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "crontab")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestEnableDisableRm(t *testing.T) {
	const initial = "# hourglass:job id=abc234 name=A\n0 1 * * * a\n# hourglass:job id=def567 name=B\n0 2 * * * b\n"
	file := writeCrontab(t, initial)

	out, errOut, code := runCLI(t, file, "disable", "abc234")
	if code != 0 || !strings.HasPrefix(out, "Paused job abc234 · backup: ") {
		t.Fatalf("disable: code=%d out=%q err=%q", code, out, errOut)
	}
	if got := readFile(t, file); !strings.Contains(got, "#hourglass:off 0 1 * * * a\n") {
		t.Errorf("after disable:\n%s", got)
	}
	out, _, code = runCLI(t, file, "disable", "abc234")
	if code != 0 || out != "Job abc234 was already paused\n" {
		t.Errorf("disable again: code=%d out=%q", code, out)
	}
	out, _, code = runCLI(t, file, "enable", "abc234")
	if code != 0 || !strings.HasPrefix(out, "Resumed job abc234") || readFile(t, file) != initial {
		t.Errorf("enable: code=%d out=%q\n%s", code, out, readFile(t, file))
	}
	out, _, code = runCLI(t, file, "rm", "def567")
	if code != 0 || !strings.HasPrefix(out, "Deleted job def567") {
		t.Errorf("rm: code=%d out=%q", code, out)
	}
	if got := readFile(t, file); got != "# hourglass:job id=abc234 name=A\n0 1 * * * a\n" {
		t.Errorf("after rm:\n%s", got)
	}

	_, errOut, code = runCLI(t, file, "rm", "zzz999")
	if code != 1 || !strings.Contains(errOut, "no managed job with id zzz999") {
		t.Errorf("unknown id: code=%d err=%q", code, errOut)
	}
	if _, _, code = runCLI(t, file, "enable"); code != 2 {
		t.Errorf("no id: code=%d", code)
	}
}

func TestDoctorFix(t *testing.T) {
	file := writeCrontab(t, "# hourglass:job id=dup222 name=First\n0 1 * * * a\n"+
		"# hourglass:job id=dup222 name=Second\n0 2 * * * /old/hourglass exec dup222 -- 'b'\n")
	out, _, _ := runCLI(t, file, "doctor", "--fix")
	for _, want := range []string{
		`Fixed: job "Second" on line 4 had duplicate id dup222`,
		"ran /old/hourglass; it now runs " + testBin,
		"Backup: ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	got := readFile(t, file)
	if strings.Count(got, "dup222") != 1 || !strings.Contains(got, testBin+" exec ") {
		t.Errorf("crontab after fix:\n%s", got)
	}
	out, _, _ = runCLI(t, file, "doctor", "--fix")
	if !strings.Contains(out, "Nothing to fix.") {
		t.Errorf("second fix:\n%s", out)
	}
}
