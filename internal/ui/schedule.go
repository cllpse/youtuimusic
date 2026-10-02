package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// tabLoadDelay lets a run across the tabs settle before anything is asked
// of the server. Holding a key would otherwise be one request per tab.
const tabLoadDelay = 150 * time.Millisecond

// tabLoadMsg is a listing load coming due. It names the listing it was armed
// for, and which of the two lists wanted it: by the time it is due the reader
// may have opened something else, and fetching whatever is in front then was
// how a popover opened in the meantime took the tab's load for itself and left
// the tab waiting on a fetch that never went out.
type tabLoadMsg struct {
	generation int
	tab        Playlist
	inDetour   bool
}

// scheduleTabLoad arms a delayed fetch of the tab in front, cancelling any
// earlier one for the tabs by moving their generation past it.
func (m *Model) scheduleTabLoad(tab Playlist) tea.Cmd {
	if m.services.Library == nil {
		return nil
	}
	m.tabGen++
	return scheduleLoad(tabLoadMsg{generation: m.tabGen, tab: tab})
}

// scheduleDetourLoad is the same for the popover, with a generation of its
// own: arming one must not cancel the other.
func (m *Model) scheduleDetourLoad(tab Playlist) tea.Cmd {
	if m.services.Library == nil {
		return nil
	}
	m.detourGen++
	return scheduleLoad(tabLoadMsg{generation: m.detourGen, tab: tab, inDetour: true})
}

func scheduleLoad(msg tabLoadMsg) tea.Cmd {
	return tea.Tick(tabLoadDelay, func(time.Time) tea.Msg { return msg })
}

// prefetchDelay is how long the cursor has to sit still before the row under
// it is resolved. Without it, holding j down the length of a playlist starts
// a yt-dlp process per row.
const prefetchDelay = 250 * time.Millisecond

type prefetchTickMsg struct{ generation int }

// schedulePrefetch arms a delayed resolve of the highlighted row, cancelling
// any earlier one by moving the generation past it.
func (m Model) schedulePrefetch() (tea.Model, tea.Cmd) {
	if m.services.Streams == nil {
		return m, nil
	}
	m.prefetchGen++
	generation := m.prefetchGen
	return m, tea.Tick(prefetchDelay, func(time.Time) tea.Msg {
		return prefetchTickMsg{generation: generation}
	})
}
