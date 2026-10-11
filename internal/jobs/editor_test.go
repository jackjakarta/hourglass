package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackjakarta/gocron/internal/crontab"
)

func reloadedJob(t *testing.T, e *env, id string) crontab.Job {
	t.Helper()
	if err := e.svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, j := range e.svc.Snapshot().Jobs {
		if j.Managed && j.ID == id {
			return j.Job
		}
	}
	t.Fatalf("job %s not found in\n%s", id, e.read(t))
	return crontab.Job{}
}

func TestCreate(t *testing.T) {
	e := newEnv(t, tab)
	res, err := e.svc.Create(context.Background(), Draft{
		Name: "Nightly backup", Schedule: "30  2 * * *", Command: "pg_dump app", Tags: []string{"backup", "db"},
		Wrapped: true, Timeout: time.Hour, NoOverlap: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID == "" || res.Backup == "" {
		t.Errorf("result = %+v", res)
	}
	want := tab + "# gocron:job id=" + res.ID + ` name="Nightly backup" tags=backup,db` + "\n30 2 * * * pg_dump app\n"
	if got := e.read(t); got != want {
		t.Errorf("crontab:\n%s\nwant:\n%s", got, want)
	}
	if !e.svc.CanUndo() {
		t.Error("create isn't undoable")
	}
	if _, err := e.svc.Create(context.Background(), Draft{Schedule: "61 * * * *", Command: "x"}); err == nil {
		t.Error("bad schedule saved")
	}
}

// Acceptance: commands with quotes, pipes, redirects, $( ) and % survive
// save → reload → edit → save unchanged.
func TestCommandRoundTrip(t *testing.T) {
	cmds := []string{
		`pg_dump app | gzip > /bk/app-$(date +%F).sql.gz`,
		`echo 'it'\''s "quoted"' >> ~/log 2>&1`,
		`printf "%s\n" "$HOME" | tee -a /tmp/x`,
		`sh -c 'echo "a|b" && echo $((1 + 2))' > /dev/null`,
		`date +%Y-%m-%d\ %H:%M`,
		`echo 100%% done; echo '%'`,
	}
	for _, cmd := range cmds {
		for _, wrapped := range []bool{false, true} {
			e := newEnv(t, "")
			res, err := e.svc.Create(context.Background(), Draft{Name: "x", Schedule: "@daily", Command: cmd})
			if err != nil {
				t.Fatal(err)
			}
			if wrapped { // as if gocron exec wrapped it (WrapNew is off until Phase 4)
				e.write(t, "# gocron:job id="+res.ID+" name=x\n@daily "+
					crontab.EscapePercent(crontab.Wrapped{Bin: "/usr/bin/gocron", ID: res.ID, Command: cmd}.Encode())+"\n")
			}
			j := reloadedJob(t, e, res.ID)
			if j.Command != cmd || j.Wrapped != wrapped {
				t.Errorf("after save: command %q (wrapped %v), want %q (wrapped %v)", j.Command, j.Wrapped, cmd, wrapped)
			}
			// Edit something else; the command must come back untouched.
			d := Draft{Name: "renamed", Schedule: j.Schedule, Command: j.Command, Wrapped: j.Wrapped}
			if _, err := e.svc.Update(context.Background(), res.ID, d); err != nil {
				t.Fatal(err)
			}
			j = reloadedJob(t, e, res.ID)
			if j.Command != cmd || j.Name != "renamed" || j.Wrapped != wrapped {
				t.Errorf("after edit: %+v, want command %q", j, cmd)
			}
		}
	}
}

// Acceptance: saving an edit touches only that job's two lines.
func TestUpdateMinimalDiff(t *testing.T) {
	const before = "MAILTO=\"\"\r\n" + // CRLF on an unrelated line stays
		"# my notes   \n" +
		"PATH=/usr/local/bin:/usr/bin:/bin\n" +
		"*/5 * * * *    ~/bin/sync.sh\n" +
		"# gocron:job id=abc234 name=A future=kept\n" +
		"0  0 * * *\ta\n" +
		"# gocron:job id=def567 name=B\n" +
		"#gocron:off 0 1 * * * b\n" +
		"\n" +
		"# trailing comment\n" // Save always ends the file with a newline
	e := newEnv(t, before)
	_, err := e.svc.Update(context.Background(), "def567", Draft{Name: "Bee", Schedule: "*/15 9-17 * * 1-5", Command: "b --flag 'x y'", Tags: []string{"t"}})
	if err != nil {
		t.Fatal(err)
	}
	got, want := strings.Split(e.read(t), "\n"), strings.Split(before, "\n")
	if len(got) != len(want) {
		t.Fatalf("line count %d, want %d:\n%s", len(got), len(want), e.read(t))
	}
	var changed []int
	for i := range want {
		if got[i] != want[i] {
			changed = append(changed, i+1)
		}
	}
	if len(changed) != 2 || changed[0] != 7 || changed[1] != 8 {
		t.Errorf("changed lines %v, want [7 8]:\n%s", changed, e.read(t))
	}
	if got[6] != "# gocron:job id=def567 name=Bee tags=t" || got[7] != "#gocron:off */15 9-17 * * 1-5 b --flag 'x y'" {
		t.Errorf("job lines:\n%s\n%s", got[6], got[7])
	}

	// Unknown meta keys survive an edit of that job.
	if _, err := e.svc.Update(context.Background(), "abc234", Draft{Name: "A", Schedule: "0 0 * * *", Command: "a2"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.read(t), "# gocron:job id=abc234 name=A future=kept\n0 0 * * * a2\n") {
		t.Errorf("crontab:\n%s", e.read(t))
	}
}

func TestUpdateNoChange(t *testing.T) {
	const before = "# gocron:job id=abc234 name=A tags=x\n0  0 * * *\ta\n"
	e := newEnv(t, before)
	res, err := e.svc.Update(context.Background(), "abc234", Draft{Name: "A", Schedule: "0 0 * * *", Command: "a", Tags: []string{"x"}})
	if err != nil || res.Backup != "" {
		t.Errorf("Update = %+v, %v; want a no-op", res, err)
	}
	if e.read(t) != before || e.backupCount(t) != 0 {
		t.Errorf("crontab rewritten:\n%s", e.read(t))
	}
}

// A Reapply after a conflict keeps the job paused if it was paused
// meanwhile: the draft only carries what the editor edits.
func TestUpdateKeepsPausedStateAndDesc(t *testing.T) {
	e := newEnv(t, "# gocron:job id=abc234 name=A desc=\"keep me\"\n0 0 * * * a\n")
	e.write(t, "# gocron:job id=abc234 name=A desc=\"keep me\"\n#gocron:off 0 0 * * * a\n")
	_, err := e.svc.Update(context.Background(), "abc234", Draft{Name: "A2", Schedule: "0 0 * * *", Command: "a"})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if _, err := conflict.Retry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := "# gocron:job id=abc234 name=A2 desc=\"keep me\"\n#gocron:off 0 0 * * * a\n"; e.read(t) != want {
		t.Errorf("crontab:\n%s\nwant:\n%s", e.read(t), want)
	}
}

func TestUpdateWrapping(t *testing.T) {
	const wrapped = "# gocron:job id=abc234 name=A\n0 0 * * * /opt/gocron exec --timeout=30m abc234 -- 'a | b'\n"
	e := newEnv(t, wrapped)
	// Keeps the binary path, edits the options.
	if _, err := e.svc.Update(context.Background(), "abc234", Draft{Name: "A", Schedule: "0 0 * * *", Command: "a | b", Wrapped: true, NoOverlap: true}); err != nil {
		t.Fatal(err)
	}
	if want := "# gocron:job id=abc234 name=A\n0 0 * * * /opt/gocron exec --no-overlap abc234 -- 'a | b'\n"; e.read(t) != want {
		t.Errorf("crontab:\n%s", e.read(t))
	}
	// Turning tracking off unwraps.
	if _, err := e.svc.Update(context.Background(), "abc234", Draft{Name: "A", Schedule: "0 0 * * *", Command: "a | b"}); err != nil {
		t.Fatal(err)
	}
	if want := "# gocron:job id=abc234 name=A\n0 0 * * * a | b\n"; e.read(t) != want {
		t.Errorf("crontab:\n%s", e.read(t))
	}
	// Turning it back on waits for gocron exec.
	if _, err := e.svc.Update(context.Background(), "abc234", Draft{Name: "A", Schedule: "0 0 * * *", Command: "a | b", Wrapped: true}); err != nil {
		t.Fatal(err)
	}
	if j := reloadedJob(t, e, "abc234"); j.Wrapped != WrapNew {
		t.Errorf("wrapped = %v, want %v", j.Wrapped, WrapNew)
	}
}

func TestUpdateMissing(t *testing.T) {
	e := newEnv(t, tab)
	_, err := e.svc.Update(context.Background(), "zzzzzz", Draft{Name: "A", Schedule: "0 0 * * *", Command: "a"})
	if !errors.Is(err, crontab.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCronEnv(t *testing.T) {
	e := newEnv(t, "PATH=/a\n0 0 * * * x\nCRON_TZ=Europe/Berlin\nPATH=/b\n# gocron:job id=abc234\n0 0 * * * y\n")
	snap := e.svc.Snapshot()
	tests := []struct {
		line int
		want CronEnv
	}{
		{1, CronEnv{PATH: DefaultCronPATH}},
		{2, CronEnv{PATH: "/a"}},
		{6, CronEnv{PATH: "/b", TZ: "Europe/Berlin"}},
		{0, CronEnv{PATH: "/b", TZ: "Europe/Berlin"}},
	}
	for _, tt := range tests {
		if got := snap.CronEnv(tt.line); got != tt.want {
			t.Errorf("CronEnv(%d) = %+v, want %+v", tt.line, got, tt.want)
		}
	}
}

func TestParseTags(t *testing.T) {
	got := ParseTags(" backup, #db,, ops ")
	if strings.Join(got, "|") != "backup|db|ops" {
		t.Errorf("ParseTags = %q", got)
	}
	if ParseTags("  ") != nil {
		t.Error("blank tags aren't empty")
	}
}
