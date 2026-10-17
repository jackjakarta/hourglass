package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jackjakarta/hourglass/internal/crontab"
	"github.com/jackjakarta/hourglass/internal/jobs"
	"github.com/jackjakarta/hourglass/internal/runner"
	"github.com/jackjakarta/hourglass/internal/store"
	"github.com/jackjakarta/hourglass/internal/tui/components"
	"github.com/jackjakarta/hourglass/internal/tui/views/editor"
	"github.com/jackjakarta/hourglass/internal/tui/views/runpane"
)

// Messages about runs started from the TUI and run history.
type (
	statsMsg struct {
		stats map[string]store.JobStats
		err   error
	}
	runLaunchedMsg struct {
		id       int
		launched *jobs.Launched
		err      error
	}
	runOutputMsg struct {
		id    int
		chunk []byte
	}
	runDoneMsg struct {
		id   int
		res  runner.Result
		tips []string
	}
)

// session is a run started from the TUI. Its pane lives on while the run
// does, shown or not.
type session struct {
	id       int
	job      crontab.Job
	kind     jobs.RunKind
	pane     runpane.Model
	launched *jobs.Launched
	pump     *pump
	done     bool
	since    time.Time
}

// coalesce is how long output gathers before the pane redraws, so a
// chatty job can't flood the UI with messages.
const coalesce = 100 * time.Millisecond

// tailMax is how much of the end of the output Diagnose sees.
const tailMax = 16 << 10

// pump reads a run's output in the background. The tail belongs to the
// reading goroutine until ch closes.
type pump struct {
	ch   chan []byte
	tail []byte
}

func startPump(l *jobs.Launched) *pump {
	p := &pump{ch: make(chan []byte, 64)}
	go func() {
		defer close(p.ch)
		buf := make([]byte, 32<<10)
		for {
			n, err := l.Output.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				p.tail = append(p.tail, chunk...)
				if over := len(p.tail) - tailMax; over > 0 {
					p.tail = p.tail[over:]
				}
				p.ch <- chunk
			}
			if err != nil {
				return
			}
		}
	}()
	return p
}

// next waits for more output, gathering what arrives within coalesce, or
// for the end of the run.
func (m Model) next(s *session) tea.Cmd {
	id, p, l, svc := s.id, s.pump, s.launched, m.opts.Service
	return func() tea.Msg {
		chunk, ok := <-p.ch
		if !ok {
			res := <-l.Done
			tips := jobs.Diagnose(l.Spec.Command, res, string(p.tail), l.Spec.Env, svc.UserEnv(), jobs.NewPathCache())
			return runDoneMsg{id: id, res: res, tips: tips}
		}
		deadline := time.After(coalesce)
	gather:
		for len(chunk) < 256<<10 {
			select {
			case more, ok := <-p.ch:
				if !ok {
					break gather // the next read sees the end
				}
				chunk = append(chunk, more...)
			case <-deadline:
				break gather
			}
		}
		return runOutputMsg{id, chunk}
	}
}

func (m Model) loadStats() tea.Cmd {
	svc, ctx := m.opts.Service, m.ctx
	if svc == nil {
		return nil
	}
	return func() tea.Msg {
		stats, err := svc.Stats(ctx)
		return statsMsg{stats, err}
	}
}

// startRun opens a pane for j and launches it in the background.
func (m Model) startRun(j crontab.Job, kind jobs.RunKind) (tea.Model, tea.Cmd) {
	svc := m.opts.Service
	if svc == nil {
		return m, nil
	}
	if kind == jobs.RunManual && j.Managed {
		for _, s := range m.sessions {
			if !s.done && s.job.Managed && s.job.ID == j.ID && s.kind == kind {
				m.paneID = s.id // already running: show it again
				return m, nil
			}
		}
	}
	m.runSeq++
	why := ""
	switch {
	case !j.Managed && j.Line == 0:
		why = "this job isn't saved yet"
	case !j.Managed:
		why = "it's an unmanaged entry; press a to adopt it"
	}
	title := j.Title()
	if title == "" {
		title = "New job"
	}
	pane := runpane.New(m.th, m.keys, runpane.Run{
		Title: title, Command: j.Command, Kind: kind, JobID: j.ID, NotRecorded: why, Started: m.now,
	})
	pane.SetSize(m.width, m.bodyHeight())
	s := &session{id: m.runSeq, job: j, kind: kind, pane: pane, since: m.now}
	m.sessions[s.id] = s
	m.paneID = s.id
	m.syncRuns()
	id := s.id
	return m, func() tea.Msg {
		l, err := svc.Launch(j, kind)
		return runLaunchedMsg{id: id, launched: l, err: err}
	}
}

func (m Model) handleRunLaunched(msg runLaunchedMsg) (tea.Model, tea.Cmd) {
	s, ok := m.sessions[msg.id]
	if !ok {
		return m, nil
	}
	if msg.err != nil {
		s.done = true
		s.pane.LaunchFailed(msg.err)
		m.syncRuns()
		if m.paneID != s.id {
			m.toast(components.ToastFail, "", "Couldn't start "+clip(s.pane.Title(), 28)+": "+msg.err.Error())
		}
		return m, nil
	}
	s.pane.Launched()
	s.launched = msg.launched
	s.pump = startPump(msg.launched)
	return m, m.next(s)
}

func (m Model) handleRunOutput(msg runOutputMsg) (tea.Model, tea.Cmd) {
	s, ok := m.sessions[msg.id]
	if !ok {
		return m, nil
	}
	s.pane.Write(msg.chunk)
	return m, m.next(s)
}

func (m Model) handleRunDone(msg runDoneMsg) (tea.Model, tea.Cmd) {
	s, ok := m.sessions[msg.id]
	if !ok {
		return m, m.loadStats()
	}
	s.done = true
	s.pane.Finish(msg.res, msg.tips)
	m.syncRuns()
	if m.paneID != s.id {
		m.toastResult(s.pane.Title(), msg.res)
		delete(m.sessions, s.id)
	}
	return m, tea.Batch(m.loadStats(), m.loadHistory())
}

func (m *Model) toastResult(title string, res runner.Result) {
	title = clip(title, 28)
	switch res.Status {
	case store.StatusSuccess:
		m.toast(components.ToastOK, "", fmt.Sprintf("%s finished · %s", title, components.Elapsed(res.Duration)))
	case store.StatusSkipped:
		m.toast(components.ToastInfo, "", title+" was skipped: the previous run is still going.")
	default:
		m.toast(components.ToastFail, "", fmt.Sprintf("%s %s · exit %d · 3 shows History", title, res.Status, res.ExitCode))
	}
}

// updatePane passes a key to the visible run pane and acts on its request.
func (m Model) updatePane(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := m.sessions[m.paneID]
	var req tea.Msg
	s.pane, req = s.pane.Update(msg)
	switch req.(type) {
	case runpane.CloseMsg:
		m.paneID = 0
		if s.done {
			delete(m.sessions, s.id)
		} else {
			m.toast(components.ToastInfo, "", clip(s.pane.Title(), 28)+" keeps running · you'll see how it ends")
		}
	case runpane.StopMsg:
		if s.launched != nil {
			s.launched.Stop()
		}
	case runpane.CompareMsg:
		return m.startRun(s.job, jobs.RunUserEnv)
	}
	return m, nil
}

// syncRuns tells the Jobs view which jobs are running: those history
// says are, plus runs started here that history may not show yet.
func (m *Model) syncRuns() {
	running := map[string]time.Time{}
	for id, st := range m.stats {
		if st.Running != nil {
			running[id] = st.Running.StartedAt
		}
	}
	for _, s := range m.sessions {
		if !s.done && s.job.Managed && s.job.ID != "" {
			if _, ok := running[s.job.ID]; !ok {
				running[s.job.ID] = s.since
			}
		}
	}
	m.jobs.SetRuns(m.stats, running)
}

// draftJob is the job a draft in the editor describes, for a test run.
func draftJob(msg editor.TestMsg) crontab.Job {
	d := msg.Draft
	j := crontab.Job{
		ID: msg.ID, Name: d.Name, Schedule: d.Schedule, Command: d.Command, Line: msg.Line,
		Managed: msg.ID != "", Wrapped: d.Wrapped, Timeout: d.Timeout, NoOverlap: d.NoOverlap,
	}
	if j.Name == "" {
		j.Name = crontab.CommandName(d.Command)
	}
	return j
}
