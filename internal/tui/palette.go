package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/jackjakarta/gocron/internal/store"
	"github.com/jackjakarta/gocron/internal/tui/components"
	"github.com/jackjakarta/gocron/internal/tui/theme"
	jobsview "github.com/jackjakarta/gocron/internal/tui/views/jobs"
	"github.com/jackjakarta/gocron/internal/tui/views/timeline"
)

// paletteAction is a palette item and what choosing it does.
type paletteAction struct {
	item components.PaletteItem
	run  func(Model) (tea.Model, tea.Cmd)
}

// paletteState is an open palette: the command palette or the tag picker.
type paletteState struct {
	p       *components.Palette
	actions []paletteAction
}

func newPaletteState(title, placeholder string, actions []paletteAction) *paletteState {
	items := make([]components.PaletteItem, len(actions))
	for i, a := range actions {
		items[i] = a.item
	}
	return &paletteState{p: components.NewPalette(title, placeholder, items), actions: actions}
}

func (m Model) openPalette() (tea.Model, tea.Cmd) {
	m.palette = newPaletteState("Commands", "Type a command or a job…", m.paletteItems())
	return m, nil
}

// updatePalette passes a key to the open palette and runs its choice.
func (m Model) updatePalette(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	chosen, done := m.palette.p.Update(msg)
	if !done {
		return m, nil
	}
	actions := m.palette.actions
	m.palette = nil
	if chosen < 0 {
		return m, nil
	}
	return actions[chosen].run(m)
}

// hintOf is a binding's first help key, so palette hints follow the key map.
func hintOf(b key.Binding) string { return b.Help().Key }

// paletteItems lists every action the palette offers right now: actions on
// the selected job first, then general ones, then every job by name.
func (m Model) paletteItems() []paletteAction {
	km := m.keys
	act := func(title string, b key.Binding, run func(Model) (tea.Model, tea.Cmd)) paletteAction {
		hint := ""
		if b.Enabled() && len(b.Keys()) > 0 {
			hint = hintOf(b)
		}
		return paletteAction{components.PaletteItem{Title: title, Hint: hint}, run}
	}
	none := key.Binding{}
	var out []paletteAction
	svc := m.opts.Service

	// The selected job.
	if j, ok := m.jobs.Selected(); ok && svc != nil {
		t := clip(j.Title(), 40)
		onJobs := func(req tea.Msg) func(Model) (tea.Model, tea.Cmd) {
			return func(m Model) (tea.Model, tea.Cmd) {
				m.tab = tabJobs
				return m.Update(req)
			}
		}
		if j.Managed {
			toggle := "Pause"
			if !j.Enabled {
				toggle = "Resume"
			}
			out = append(out,
				act("Edit · "+t, km.Edit, onJobs(jobsview.EditMsg{Job: j})),
				act(toggle+" · "+t, km.Toggle, onJobs(jobsview.ToggleMsg{Job: j})),
			)
		} else {
			out = append(out, act("Adopt · "+t, km.Adopt, onJobs(jobsview.AdoptMsg{Job: j})))
		}
		out = append(out,
			act("Run now · "+t, km.Run, onJobs(jobsview.RunMsg{Job: j})),
			act("Test in cron's environment · "+t, km.Test, onJobs(jobsview.TestMsg{Job: j})),
		)
		if j.Managed {
			out = append(out, act("Delete · "+t, km.Delete, onJobs(jobsview.DeleteMsg{Job: j})))
		}
		out = append(out,
			act("Copy command · "+t, km.Copy, onJobs(jobsview.CopyMsg{Text: j.Command, What: "command"})),
			act("Copy schedule · "+t, km.CopySchedule, onJobs(jobsview.CopyMsg{Text: j.Schedule, What: "schedule"})),
		)
	}

	// General actions.
	goTab := func(t tab) func(Model) (tea.Model, tea.Cmd) {
		return func(m Model) (tea.Model, tea.Cmd) { return m.switchTab(t) }
	}
	out = append(out,
		act("Go to Jobs", km.Tab1, goTab(tabJobs)),
		act("Go to Timeline", km.Tab2, goTab(tabTimeline)),
		act("Go to History", km.Tab3, goTab(tabHistory)),
		act("Go to Crontab", km.Tab4, goTab(tabCrontab)),
	)
	if svc != nil {
		out = append(out,
			act("New job", km.New, func(m Model) (tea.Model, tea.Cmd) {
				m.tab = tabJobs
				return m.Update(jobsview.NewMsg{})
			}),
			act("Undo the last change", km.Undo, func(m Model) (tea.Model, tea.Cmd) {
				return m, m.mutate("", "", m.opts.Service.Undo)
			}),
			act("Filter jobs", km.Filter, func(m Model) (tea.Model, tea.Cmd) {
				m.tab = tabJobs
				m.jobs.StartFilter()
				return m, nil
			}),
			act("Filter jobs by tag", km.TagFilter, func(m Model) (tea.Model, tea.Cmd) {
				m.tab = tabJobs
				return m.openTagPicker()
			}),
		)
		for _, s := range []jobsview.Sort{jobsview.SortNext, jobsview.SortName, jobsview.SortCrontab} {
			names := map[jobsview.Sort]string{jobsview.SortNext: "next run", jobsview.SortName: "name", jobsview.SortCrontab: "crontab order"}
			out = append(out, act("Sort jobs by "+names[s], km.Sort, func(m Model) (tea.Model, tea.Cmd) {
				m.tab = tabJobs
				m.jobs.SetSort(s)
				return m, nil
			}))
		}
		onHistory := func(change func(m *Model)) func(Model) (tea.Model, tea.Cmd) {
			return func(m Model) (tea.Model, tea.Cmd) {
				change(&m)
				m.tab = tabHistory
				return m, m.loadHistory()
			}
		}
		failures := "History: failures only"
		if m.history.Filter().FailuresOnly {
			failures = "History: show successes too"
		}
		out = append(out,
			act(failures, km.Failures, onHistory(func(m *Model) { m.history.SetFailures(!m.history.Filter().FailuresOnly) })),
			act("History: only cron runs", km.Trigger, onHistory(func(m *Model) { m.history.SetTrigger(store.TriggerCron) })),
			act("History: only manual runs", km.Trigger, onHistory(func(m *Model) { m.history.SetTrigger(store.TriggerManual) })),
			act("History: only test runs", km.Trigger, onHistory(func(m *Model) { m.history.SetTrigger(store.TriggerTest) })),
			act("History: every run", none, onHistory(func(m *Model) { m.history.ClearFilters() })),
			act("Edit the crontab in $EDITOR", km.EditCrontab, func(m Model) (tea.Model, tea.Cmd) {
				return m.editCrontab()
			}),
			act("Crontab: revisions and restore", km.Restore, goTab(tabCrontab)),
			act("Timeline: next 24 hours (day)", km.Day, func(m Model) (tea.Model, tea.Cmd) {
				m.tab = tabTimeline
				m.timeline.SetMode(timeline.ModeDay)
				return m, nil
			}),
			act("Timeline: next 7 days (week)", km.Week, func(m Model) (tea.Model, tea.Cmd) {
				m.tab = tabTimeline
				m.timeline.SetMode(timeline.ModeWeek)
				return m, nil
			}),
		)
	}
	if !m.opts.NoColor {
		for _, name := range theme.Names {
			if name == m.th.Name {
				continue
			}
			out = append(out, act("Theme: "+strings.ToUpper(name[:1])+name[1:], none, func(m Model) (tea.Model, tea.Cmd) {
				m.themeName = name
				m.applyTheme()
				m.toast(components.ToastInfo, theme.GlyphInfo, "Theme: "+name+" (set theme in config.toml to keep it)")
				return m, nil
			}))
		}
	}
	out = append(out,
		act("Help: all keys", km.Help, func(m Model) (tea.Model, tea.Cmd) {
			m.help = true
			return m, nil
		}),
		act("Quit gocron", km.Quit, func(m Model) (tea.Model, tea.Cmd) { return m, tea.Quit }),
	)

	// Every job by name.
	for _, j := range m.snap.Jobs {
		out = append(out, act("Go to job · "+clip(j.Title(), 50), none, func(m Model) (tea.Model, tea.Cmd) {
			m.tab = tabJobs
			m.jobs.Reveal(j)
			return m, nil
		}))
	}
	if svc != nil {
		for _, j := range m.snap.Jobs {
			if !j.Managed {
				continue
			}
			out = append(out, act("History · "+clip(j.Title(), 50), none, func(m Model) (tea.Model, tea.Cmd) {
				return m.Update(jobsview.HistoryMsg{Job: j})
			}))
		}
	}
	return out
}

// openTagPicker lists the managed jobs' tags with counts; choosing one
// filters the Jobs list.
func (m Model) openTagPicker() (tea.Model, tea.Cmd) {
	counts := map[string]int{}
	for _, j := range m.snap.Jobs {
		for _, t := range j.Tags {
			counts[t]++
		}
	}
	if len(counts) == 0 {
		m.toast(components.ToastInfo, "", "None of your jobs have tags yet. Press e to add some.")
		return m, nil
	}
	tags := make([]string, 0, len(counts))
	for t := range counts {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	setTag := func(tag string) func(Model) (tea.Model, tea.Cmd) {
		return func(m Model) (tea.Model, tea.Cmd) {
			m.jobs.SetTag(tag)
			return m, nil
		}
	}
	var actions []paletteAction
	if m.jobs.Tag() != "" {
		actions = append(actions, paletteAction{components.PaletteItem{Title: "All jobs (clear the tag filter)"}, setTag("")})
	}
	for _, t := range tags {
		actions = append(actions, paletteAction{components.PaletteItem{Title: "#" + t, Hint: fmt.Sprintf("%d %s", counts[t], plural(counts[t], "job"))}, setTag(t)})
	}
	m.palette = newPaletteState("Filter by tag", "Type a tag…", actions)
	return m, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
