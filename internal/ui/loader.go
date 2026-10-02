package ui

import (
	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
)

// The one way this app says it is waiting. It had grown three spellings —
// two "Loading…" and a "loading…" — which is two too many for something
// whose whole job is to look the same wherever it appears.
const loaderLabel = "Loading…"

// newLoader builds the spinner. Its block frames are a step away from the
// progress bar's, which is the only other thing on screen made of blocks.
func newLoader() spinner.Model {
	return spinner.New(spinner.WithSpinner(spinner.Pulse), spinner.WithStyle(active))
}

// loader is the spinner and its word, as one thing.
func (m Model) loader() string {
	return m.spin.View() + " " + dim.Render(loaderLabel)
}

// centredLoader fills a region with it, for a list that has nothing in it
// yet.
func (m Model) centredLoader(width, height int) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.loader())
}
