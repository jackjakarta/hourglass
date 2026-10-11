// Package keys defines gocron's key bindings (spec §6.4).
package keys

import "charm.land/bubbles/v2/key"

// Map is every binding the TUI handles.
type Map struct {
	Up, Down, Top, Bottom, PageUp, PageDown              key.Binding
	Open, Back, NextTab, PrevTab, Tab1, Tab2, Tab3, Tab4 key.Binding

	New, Edit, Toggle, Delete, Adopt, Undo, Filter key.Binding
	Run, Test, History                             key.Binding
	Save, TestDraft                                key.Binding // in the editor

	// The History view and log viewer.
	Failures, Trigger, Pager, NextMatch, PrevMatch key.Binding
	// The run pane.
	Stop, Compare key.Binding

	Help, Quit, ForceQuit key.Binding
}

// Default returns the standard bindings.
func Default() Map {
	b := func(keys []string, help, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
	}
	return Map{
		Up:       b([]string{"up", "k"}, "↑/k", "move up"),
		Down:     b([]string{"down", "j"}, "↓/j", "move down"),
		Top:      b([]string{"g", "home"}, "g", "first job"),
		Bottom:   b([]string{"G", "end"}, "G", "last job"),
		PageUp:   b([]string{"pgup"}, "pgup", "page up"),
		PageDown: b([]string{"pgdown"}, "pgdn", "page down"),
		Open:     b([]string{"enter"}, "↵", "open"),
		Back:     b([]string{"esc"}, "esc", "back / clear"),
		NextTab:  b([]string{"tab"}, "tab", "next view"),
		PrevTab:  b([]string{"shift+tab"}, "shift+tab", "previous view"),
		Tab1:     b([]string{"1"}, "1", "jobs"),
		Tab2:     b([]string{"2"}, "2", "timeline"),
		Tab3:     b([]string{"3"}, "3", "history"),
		Tab4:     b([]string{"4"}, "4", "crontab"),

		Toggle:  b([]string{"space"}, "space", "pause/resume"),
		Delete:  b([]string{"x"}, "x", "delete"),
		Adopt:   b([]string{"a"}, "a", "adopt"),
		Undo:    b([]string{"u"}, "u", "undo"),
		Filter:  b([]string{"/"}, "/", "filter"),
		New:     b([]string{"n"}, "n", "new job"),
		Edit:    b([]string{"e"}, "e", "edit"),
		Run:     b([]string{"r"}, "r", "run now"),
		Test:    b([]string{"t"}, "t", "test in cron's env"),
		History: b([]string{"h"}, "h", "job's history"),

		Failures:  b([]string{"f"}, "f", "failures only"),
		Trigger:   b([]string{"c"}, "c", "cycle trigger"),
		Pager:     b([]string{"o"}, "o", "open in $PAGER"),
		NextMatch: b([]string{"n"}, "n", "next match"),
		PrevMatch: b([]string{"N"}, "N", "previous match"),

		Stop:    b([]string{"x"}, "x", "stop the run"),
		Compare: b([]string{"c"}, "c", "rerun in your shell"),

		Save:      b([]string{"ctrl+s"}, "^s", "save (editor)"),
		TestDraft: b([]string{"ctrl+t"}, "^t", "test draft (editor)"),

		Help:      b([]string{"?"}, "?", "help"),
		Quit:      b([]string{"q"}, "q", "quit"),
		ForceQuit: b([]string{"ctrl+c"}, "ctrl+c", "quit now"),
	}
}

// FullHelp groups the bindings for the help overlay.
func (m Map) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.Up, m.Down, m.Top, m.Bottom, m.PageUp, m.PageDown, m.Open, m.Back},
		{m.New, m.Edit, m.Toggle, m.Delete, m.Adopt, m.Run, m.Test, m.History, m.Undo, m.Filter},
		{m.Failures, m.Trigger, m.Pager, m.NextMatch, m.PrevMatch, m.Save, m.TestDraft},
		{m.Tab1, m.Tab2, m.Tab3, m.Tab4, m.NextTab, m.Help, m.Quit, m.ForceQuit},
	}
}

// GroupTitles name the FullHelp columns.
var GroupTitles = []string{"Navigate", "Jobs", "History", "General"}
