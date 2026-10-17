package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"

	"github.com/jackjakarta/hourglass/internal/crontab"
	"github.com/jackjakarta/hourglass/internal/jobs"
	"github.com/jackjakarta/hourglass/internal/tui/components"
	"github.com/jackjakarta/hourglass/internal/tui/theme"
)

type (
	// editorDoneMsg reports that the editor on a crontab copy exited.
	editorDoneMsg struct {
		path string // the copy
		base []byte // the crontab it was copied from
		err  error
	}
	// crontabEditMsg reports saving an edited crontab.
	crontabEditMsg struct {
		path string
		res  jobs.Result
		err  error
		snap jobs.Snapshot
	}
)

// maxProblemsShown caps the problems a dialog lists.
const maxProblemsShown = 8

// confirmRestore asks before restoring a revision.
func (m Model) confirmRestore(rev jobs.Revision) (tea.Model, tea.Cmd) {
	svc := m.opts.Service
	if svc == nil {
		return m, nil
	}
	if bytes.Equal(rev.Content, m.snap.Content) {
		m.toast(components.ToastInfo, "", "This revision is the same as the current crontab.")
		return m, nil
	}
	when := rev.At.In(m.now.Location()).Format("Mon 2 Jan 15:04")
	body := []string{
		"Your crontab goes back to how it was before",
		"the change at " + when + ".",
		"",
		"Restoring would: " + jobs.Summarize(m.snap.Content, rev.Content).String() + ".",
		"A backup is kept, and u undoes it.",
	}
	body = append(body, problemLines("This revision has", crontab.Parse(rev.Content).Problems())...)
	path := rev.Path
	m.confirm = &components.Confirm{
		Title:  "Restore this revision?",
		Body:   body,
		Safe:   "Cancel",
		Action: "Restore",
		OnOK: m.mutate("Restored", "revision from "+when, func(ctx context.Context) (jobs.Result, error) {
			return svc.Restore(ctx, path)
		}),
	}
	return m, nil
}

// problemLines lists problems for a dialog body, after a blank line.
func problemLines(lead string, problems []crontab.Problem) []string {
	if len(problems) == 0 {
		return nil
	}
	out := []string{"", fmt.Sprintf("%s %s %d %s:", theme.GlyphWarn, lead, len(problems), plural(len(problems), "problem"))}
	for i, p := range problems {
		if i == maxProblemsShown {
			out = append(out, fmt.Sprintf("  …and %d more", len(problems)-i))
			break
		}
		out = append(out, "  "+clip(p.String(), 70))
	}
	return out
}

// editCrontab opens a copy of the crontab in the user's editor.
func (m Model) editCrontab() (tea.Model, tea.Cmd) {
	if m.opts.Service == nil {
		return m, nil
	}
	base := m.snap.Content
	f, err := os.CreateTemp("", "hourglass-crontab-*.txt") // 0600
	if err == nil {
		_, err = f.Write(base)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		m.toast(components.ToastFail, "", "Couldn't make a copy to edit: "+err.Error())
		return m, nil
	}
	return m, m.runEditor(f.Name(), base)
}

func (m Model) runEditor(path string, base []byte) tea.Cmd {
	done := func(err error) tea.Msg { return editorDoneMsg{path: path, base: base, err: err} }
	if m.opts.RunEditor != nil {
		return m.opts.RunEditor(path, done)
	}
	return tea.ExecProcess(editorCommand(path), done)
}

// editorCommand runs $VISUAL, else $EDITOR, else vi on path.
func editorCommand(path string) *exec.Cmd {
	name := os.Getenv("VISUAL")
	if name == "" {
		name = os.Getenv("EDITOR")
	}
	words, err := crontab.ShellWords(name)
	if err != nil || len(words) == 0 {
		words = []string{"vi"}
	}
	return exec.Command(words[0], append(words[1:], path)...)
}

// handleEditorDone checks the edited copy and saves it when it's valid.
func (m Model) handleEditorDone(msg editorDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		os.Remove(msg.path)
		m.toast(components.ToastFail, "", "The editor failed: "+msg.err.Error()+". Set $EDITOR to the one you use.")
		return m, nil
	}
	edited, err := os.ReadFile(msg.path)
	if err != nil {
		m.toast(components.ToastFail, "", "Couldn't read your edit: "+err.Error())
		return m, nil
	}
	if bytes.Equal(edited, msg.base) {
		os.Remove(msg.path)
		m.toast(components.ToastInfo, "", "No changes.")
		return m, nil
	}
	if problems := jobs.ValidateEdit(msg.base, edited); len(problems) > 0 {
		body := append([]string{"Nothing was saved. Fix these and save again:"}, problemLines("Your edit has", problems)[1:]...)
		path := msg.path
		m.confirm = &components.Confirm{
			Title:  "The crontab has problems",
			Body:   body,
			Safe:   "Edit again",
			Action: "Discard",
			Danger: true,
			OnSafe: m.runEditor(path, msg.base),
			OnOK: func() tea.Msg {
				os.Remove(path)
				return nil
			},
		}
		return m, nil
	}
	svc, ctx, path, base := m.opts.Service, m.ctx, msg.path, msg.base
	return m, func() tea.Msg {
		res, err := svc.Replace(ctx, base, edited)
		return crontabEditMsg{path: path, res: res, err: err, snap: svc.Snapshot()}
	}
}

// handleCrontabEdit reports saving an edited crontab. When it couldn't be
// saved, the edit stays in its temp file and the toast says where.
func (m Model) handleCrontabEdit(msg crontabEditMsg) (tea.Model, tea.Cmd) {
	m.setSnapshot(msg.snap)
	var conflict *jobs.ConflictError
	switch {
	case msg.err == nil:
		os.Remove(msg.path)
		if msg.res.Backup == "" {
			m.toast(components.ToastInfo, "", "Nothing changed.")
		} else {
			m.toast(components.ToastOK, "", "Saved the crontab · backup created · u undo")
		}
		return m, m.loadRevisions()
	case errors.As(msg.err, &conflict):
		m.toast(components.ToastFail, "", "The crontab changed while you were editing, so your edit wasn't saved. It's in "+msg.path)
		return m, m.reload("Crontab reloaded (changed outside hourglass)")
	}
	m.toast(components.ToastFail, "", "Couldn't save: "+msg.err.Error()+". Your edit is in "+msg.path)
	return m, nil
}
