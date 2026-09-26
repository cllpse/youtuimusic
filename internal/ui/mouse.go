package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// doubleClick is how close two clicks on the same row have to be to count as
// one gesture. Selecting is one click and playing is two, so that pointing at
// a track cannot start it by accident.
const doubleClick = 400 * time.Millisecond

// region names what is drawn at a point on screen.
type region int

const (
	regionNone region = iota
	regionTabs
	regionTracks
	regionScrollbar
	regionBar
	regionControls
	regionModal
)

// hit maps a screen position onto what is drawn there: a tab index, a track
// row, or a column of the progress bar. It shares its geometry with the
// renderer, so a click cannot land somewhere other than where it looks.
func (m Model) hit(x, y int) (region, int) {
	if m.width == 0 || x < 0 || y < 0 || x >= m.width {
		return regionNone, 0
	}
	switch {
	case y < tabsHeight:
		for _, s := range m.tabSpans() {
			if x >= s.start && x < s.end {
				return regionTabs, s.index
			}
		}
		return regionNone, 0

	case y < tabsHeight+m.bodyHeight():
		if m.hasScrollbar() && x == m.scrollbarColumn() {
			return regionScrollbar, y
		}
		// The table is scrolled, so the row on screen is not the row in the
		// list.
		row := y - tabsHeight - headerRows + m.trackOffset
		if row >= m.rowCount() {
			return regionNone, 0
		}
		return regionTracks, row

	case y >= m.barRow() && y < m.barRow()+barRows:
		return regionBar, x

	case y >= m.controlsRow() && y < m.controlsRow()+controlsRows:
		for _, btn := range m.controlButtons() {
			if x >= btn.start && x < btn.end {
				return regionControls, int(btn.control)
			}
		}
		return regionNone, 0
	}
	return regionNone, 0
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()

	switch msg.(type) {
	case tea.MouseReleaseMsg:
		if m.draggingScroll {
			m.draggingScroll = false
			return m, nil
		}
		if !m.scrubbing {
			return m, nil
		}
		// Where the button comes up is the answer, not wherever the last
		// motion event happened to land.
		m.scrubbing = false
		m.Position = m.positionAt(mouse.X)
		return m, m.seek(m.Position)

	case tea.MouseMotionMsg:
		// Motion is only reported while a button is held down.
		if m.draggingScroll {
			m.scrollTo(mouse.Y)
			return m, nil
		}
		if !m.scrubbing {
			return m, nil
		}
		// Only the column matters: dragging a scrubber usually wanders off
		// its row, and that should not stop it tracking the pointer.
		m.Position = m.positionAt(mouse.X)
		return m, nil

	case tea.MouseWheelMsg:
		if m.menu.open {
			return m, nil // the menu is anchored; scrolling under it would lie
		}
		if m.detour.active {
			if m.modalContains(mouse.X, mouse.Y) {
				m.scrollDetour(wheelDelta(mouse) * wheelStep)
				if m.viewingDetourMoreRow() {
					return m.fetchMore(true)
				}
			}
			return m, nil
		}
		return m.handleWheel(mouse)

	case tea.MouseClickMsg:
		switch mouse.Button {
		case tea.MouseRight:
			return m.openMenuAt(mouse)
		case tea.MouseLeft:
			if m.menu.open {
				return m.clickMenu(mouse)
			}
			if m.detour.active {
				return m.clickModal(mouse)
			}
			return m.handleClick(mouse)
		}
	}
	return m, nil
}

// openMenuAt opens the track menu on whatever row was right-clicked.
func (m Model) openMenuAt(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	if m.detour.active {
		row, ok := m.modalHit(mouse.X, mouse.Y)
		if !ok {
			return m, nil
		}
		m.detour.cursor = row
		return m.openMenu(m.detour.tracks[row], mouse.X, mouse.Y), nil
	}
	where, n := m.hit(mouse.X, mouse.Y)
	if where != regionTracks || n >= len(m.Tracks) {
		return m, nil
	}
	m.trackCursor = n
	m.scroll()
	return m.openMenu(m.Tracks[n], mouse.X, mouse.Y), nil
}

// clickMenu runs a row, or dismisses the menu when the click lands outside
// it — which is what clicking away from a menu means everywhere else.
func (m Model) clickMenu(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	if !m.menuContains(mouse.X, mouse.Y) {
		m.menu = trackMenu{}
		return m, nil
	}
	if row, ok := m.menuHit(mouse.X, mouse.Y); ok {
		return m.activate(row)
	}
	return m, nil // on its border, which is neither a row nor outside
}

func (m Model) handleClick(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	where, n := m.hit(mouse.X, mouse.Y)
	again := m.isSecondClick(where, n)
	m.lastClickAt, m.lastClickRegion, m.lastClickRow = m.clock(), where, n

	switch where {
	case regionTabs:
		if m.detour.active {
			return m.leaveDetour()
		}
		return m.selectTab(n)

	case regionTracks:
		if m.more.More() && n == len(m.Tracks) {
			// The row that offers the next page is not a track.
			m.trackCursor = n
			m.scroll()
			return m.fetchMore(false)
		}
		if n >= len(m.Tracks) {
			return m, nil
		}
		m.trackCursor = n
		m.scroll()
		t := m.Tracks[n]
		if again {
			return m.open(t)
		}
		return m, m.prefetch(t.VideoID)

	case regionControls:
		return m.press(control(n))

	case regionScrollbar:
		m.draggingScroll = true
		m.scrollTo(mouse.Y)
		return m, nil

	case regionBar:
		if m.Length <= 0 {
			return m, nil // nothing loaded, so nowhere to seek to
		}
		m.scrubbing = true
		m.Position = m.positionAt(mouse.X)
		return m, nil
	}
	return m, nil
}

// wheelStep is how many rows a notch moves, which is what a wheel does
// everywhere else.
const wheelStep = 3

// handleWheel scrolls whatever is under the pointer. Over the list it moves
// the view and not the selection: looking further down a playlist should not
// lose your place in it, and nothing is resolved because nothing was chosen.
// wheelDelta is which way a notch went, and zero for anything else.
func wheelDelta(mouse tea.Mouse) int {
	switch mouse.Button {
	case tea.MouseWheelUp:
		return -1
	case tea.MouseWheelDown:
		return 1
	}
	return 0
}

func (m Model) handleWheel(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	delta := wheelDelta(mouse)
	if delta == 0 {
		return m, nil
	}

	where, _ := m.hit(mouse.X, mouse.Y)
	switch where {
	case regionTabs:
		if m.detour.active {
			return m, nil
		}
		return m.selectTab(m.tabCursor + delta)
	case regionTracks:
		m.scrollBy(delta * wheelStep)
		// Scrolling to the end of a list is the same as walking to it.
		if m.viewingMoreRow() {
			return m.fetchMore(false)
		}
	}
	return m, nil
}

// positionAt converts a column on the progress row into a playback position.
func (m Model) positionAt(x int) time.Duration {
	start, width := m.barGeometry()
	if width <= 0 || m.Length <= 0 {
		return 0
	}
	offset := min(max(x-start, 0), width)
	return time.Duration(float64(m.Length) * float64(offset) / float64(width))
}

func (m Model) isSecondClick(where region, n int) bool {
	return where == m.lastClickRegion && n == m.lastClickRow &&
		!m.lastClickAt.IsZero() && m.clock().Sub(m.lastClickAt) < doubleClick
}

// clock is overridable so click timing is testable.
func (m Model) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}
