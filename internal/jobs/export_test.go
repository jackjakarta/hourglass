package jobs

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

const wrappedTab = `CRON_TZ=Europe/Berlin
# gocron:job id=abc234 name=A
0 0 * * * /opt/gocron exec abc234 -- a
`

func TestExportParseRoundTrip(t *testing.T) {
	e := newEnv(t, wrappedTab)
	var buf bytes.Buffer
	if err := EncodeExport(&buf, e.svc.Export()); err != nil {
		t.Fatal(err)
	}
	doc, err := ParseExport(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Jobs) != 1 || doc.Jobs[0].TZ != "Europe/Berlin" || !doc.Jobs[0].Wrapped {
		t.Errorf("doc = %+v", doc)
	}
	if _, err := ParseExport(strings.NewReader("")); err == nil {
		t.Error("an empty file parsed")
	}
}

func TestImportKeepsBinAndWarnsAboutTZ(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, wrappedTab)
	doc := e.svc.Export()
	doc.Jobs[0].Name = "Renamed"
	doc.Jobs = append(doc.Jobs, ExportJob{ID: "new234", Schedule: "@daily", Command: "n", Enabled: true, TZ: "Asia/Tokyo"})
	plan, res, err := e.svc.Import(ctx, doc)
	if err != nil || res.Backup == "" {
		t.Fatalf("import: %+v, %v", res, err)
	}
	if len(plan.Create) != 1 || len(plan.Update) != 1 || plan.Update[0].Fields[0] != "name" {
		t.Errorf("plan = %+v", plan)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "new234 was exported under CRON_TZ=Asia/Tokyo but lands under CRON_TZ=Europe/Berlin") {
		t.Errorf("warnings = %q", plan.Warnings)
	}
	got := e.read(t)
	if !strings.Contains(got, "/opt/gocron exec abc234 -- 'a'") || !strings.Contains(got, "name=Renamed") {
		t.Errorf("the wrapped job lost its binary path:\n%s", got)
	}
	// One change: one undo step reverts the whole import.
	if _, err := e.svc.Undo(ctx); err != nil || e.read(t) != wrappedTab {
		t.Errorf("undo: %v\n%s", err, e.read(t))
	}
}

func TestPlanImportRefuses(t *testing.T) {
	e := newEnv(t, tab+"# gocron:job id=abc234 name=Copy\n0 0 * * * a\n")
	doc := ExportDoc{Version: 1, Jobs: []ExportJob{{ID: "abc234", Schedule: "@daily", Command: "a", Enabled: true}}}
	if _, err := e.svc.PlanImport(doc); err == nil || !strings.Contains(err.Error(), "used twice") {
		t.Errorf("duplicate id in the crontab: %v", err)
	}
	e = newEnv(t, tab)
	e.svc.opts.Bin = "relative/gocron"
	doc.Jobs[0] = ExportJob{ID: "zzz234", Schedule: "@daily", Command: "z", Enabled: true, Wrapped: true}
	if _, err := e.svc.PlanImport(doc); err == nil || !strings.Contains(err.Error(), "tracks its runs") {
		t.Errorf("wrapped job without a usable binary: %v", err)
	}
}
