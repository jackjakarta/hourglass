// Package keys defines gocron's key bindings (spec §6.4).
package keys

import "charm.land/bubbles/v2/key"

// Map is every binding the TUI handles.
type Map struct {
	Up, Down, Top, Bottom, PageUp, PageDown              key.Binding
	Open, Back, NextTab, PrevTab, Tab1, Tab2, Tab3, Tab4 key.Binding

	New, Edit, Toggle, Delete, Adopt, Undo, Filter key.Binding
	Run, Test                                      key.Binding // coming in a later phase
	Save, TestDraft                                key.Binding // in the editor

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

		Toggle: b([]string{"space"}, "space", "pause/resume"),
		Delete: b([]string{"x"}, "x", "delete"),
		Adopt:  b([]string{"a"}, "a", "adopt"),
		Undo:   b([]string{"u"}, "u", "undo"),
		Filter: b([]string{"/"}, "/", "filter"),
		New:    b([]string{"n"}, "n", "new job"),
		Edit:   b([]string{"e"}, "e", "edit"),
		Run:    b([]string{"r"}, "r", "run now"),
		Test:   b([]string{"t"}, "t", "test run"),

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
		{m.New, m.Edit, m.Toggle, m.Delete, m.Adopt, m.Undo, m.Filter, m.Save},
		{m.Tab1, m.Tab2, m.Tab3, m.Tab4, m.NextTab, m.Help, m.Quit, m.ForceQuit},
	}
}

// GroupTitles name the FullHelp columns.
var GroupTitles = []string{"Navigate", "Jobs", "General"}
