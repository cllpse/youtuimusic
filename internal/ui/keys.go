package ui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Every shortcut the app has, in one place.
//
// The handlers dispatch on these bindings and the sheet that lists them is
// drawn from the same ones, so there is no second table to go out of date:
// adding a key here is adding it to the help, and a key the help does not
// mention is a key nothing dispatches on. A test walks the struct to make
// sure none of them is left out of a column.
//
// The alternates are all still here, the way the switch statements named
// them: vim's hjkl beside the arrows, ctrl+u and ctrl+d beside the page keys,
// = beside + because it is the same key unshifted. What each binding's help
// shows is the one worth learning, not the whole list.
type keyMap struct {
	// The player, which works wherever you are — a popover in front of it
	// does not take the transport with it.
	PlayPause key.Binding
	Next      key.Binding
	Previous  key.Binding
	Repeat    key.Binding
	Like      key.Binding
	Dislike   key.Binding

	// Getting around a list, and between them.
	Up       key.Binding
	Down     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Top      key.Binding
	Bottom   key.Binding
	PrevTab  key.Binding
	NextTab  key.Binding

	// Everything else.
	Open        key.Binding
	Search      key.Binding
	Sort        key.Binding
	SortReverse key.Binding
	Close       key.Binding
	Help        key.Binding
	Quit        key.Binding
}

// appKeys is the binding set, which nothing rebinds at runtime. It is a
// package var rather than a field on the Model so that a Model built without
// New — as the tests do — still answers its keys rather than swallowing every
// one of them.
var appKeys = keyMap{
	PlayPause: key.NewBinding(
		key.WithKeys(" ", "space"),
		key.WithHelp("space", "play or pause")),
	Next: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "next track")),
	Previous: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "previous track")),
	Repeat: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "repeat off, on, one")),
	Like: key.NewBinding(
		key.WithKeys("+", "="),
		key.WithHelp("+", "like")),
	Dislike: key.NewBinding(
		key.WithKeys("-", "_"),
		key.WithHelp("-", "dislike")),

	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("up/k", "up one row")),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("down/j", "down one row")),
	PageUp: key.NewBinding(
		key.WithKeys("pgup", "ctrl+u"),
		key.WithHelp("pgup/ctrl+u", "up a page")),
	PageDown: key.NewBinding(
		key.WithKeys("pgdown", "ctrl+d"),
		key.WithHelp("pgdown/ctrl+d", "down a page")),
	Top: key.NewBinding(
		key.WithKeys("home", "g"),
		key.WithHelp("home/g", "first row")),
	Bottom: key.NewBinding(
		key.WithKeys("end", "G"),
		key.WithHelp("end/G", "last row")),
	PrevTab: key.NewBinding(
		key.WithKeys("left", "h", "shift+tab"),
		key.WithHelp("left/h", "previous playlist")),
	NextTab: key.NewBinding(
		key.WithKeys("right", "l", "tab"),
		key.WithHelp("right/l", "next playlist")),

	Open: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "play, or open a release")),
	Search: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "search")),
	Sort: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "sort by the next column")),
	SortReverse: key.NewBinding(
		key.WithKeys("S"),
		key.WithHelp("S", "reverse the sort")),
	Close: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "close what is in front")),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "these keys")),
	Quit: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("ctrl+c", "quit")),
}

// FullHelp is what the sheet draws: a column each for the player, moving
// about, and everything else. bubbles renders one column per group, so the
// grouping is the layout.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.PlayPause, k.Next, k.Previous, k.Repeat, k.Like, k.Dislike},
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom, k.PrevTab, k.NextTab},
		{k.Open, k.Search, k.Sort, k.SortReverse, k.Close, k.Help, k.Quit},
	}
}

// ShortHelp is the one-line form. Nothing draws it — the sheet is the full
// help and the buttons name their own keys — but help.KeyMap asks for it, and
// if a line of them is ever wanted these are the ones worth the room.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.PlayPause, k.Next, k.Search, k.Help, k.Quit}
}

// matches is key.Matches, pinned to a keystroke and spelled shorter: the
// handlers are a column of these and the package name on every line of them
// says nothing the reader did not already know.
func matches(msg tea.KeyPressMsg, bindings ...key.Binding) bool {
	return key.Matches(msg, bindings...)
}
