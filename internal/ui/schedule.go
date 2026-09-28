package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)


const tabLoadDelay = 150 * time.Millisecond

type tabLoadMsg struct{ generation int }

func (m *Model) scheduleTabLoad() tea.Cmd {
	if m.services.Library == nil {
		return nil
	}
	m.tabGen++
	generation := m.tabGen
	return tea.Tick(tabLoadDelay, func(time.Time) tea.Msg {
		return tabLoadMsg{generation: generation}
	})
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
