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
	regionSidebar
	regionTracks
	regionBar
)

// hit maps a screen position onto what is drawn there, returning the row for
// a list and the column for the progress bar. It shares its geometry with the
// renderer, so a click cannot land somewhere other than where it looks.
func (m Model) hit(x, y int) (region, int) {
	if m.width == 0 || x < 0 || y < 0 || x >= m.width {
		return regionNone, 0
	}
	if y < m.bodyHeight() {
		if x < sidebarWidth {
			return regionSidebar, y
		}
		return regionTracks, y
	}
	if y == m.barRow() {
		return regionBar, x
	}
	return regionNone, 0
}

// barRow is the line the progress bar is drawn on: past the lists, the blank
// line and the title.
func (m Model) barRow() int { return m.bodyHeight() + 2 }

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()

	switch msg.(type) {
	case tea.MouseReleaseMsg:
		if !m.scrubbing {
			return m, nil
		}
		// Where the button comes up is the answer, not wherever the last
		// motion event happened to land. The seek happens once, here:
		// seeking on every motion event makes mpv stutter.
		m.scrubbing = false
		m.Position = m.positionAt(mouse.X)
		return m, m.seek(m.Position)

	case tea.MouseMotionMsg:
		// Motion is only reported while a button is held down.
		if !m.scrubbing {
			return m, nil
		}
		// Only the column matters: dragging a scrubber usually wanders off
		// its row, and that should not stop it tracking the pointer.
		m.Position = m.positionAt(mouse.X)
		return m, nil

	case tea.MouseWheelMsg:
		return m.handleWheel(mouse)

	case tea.MouseClickMsg:
		if mouse.Button != tea.MouseLeft {
			return m, nil
		}
		return m.handleClick(mouse)
	}
	return m, nil
}

func (m Model) handleClick(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	where, n := m.hit(mouse.X, mouse.Y)
	again := m.isSecondClick(where, n)
	m.lastClickAt, m.lastClickRegion, m.lastClickRow = m.clock(), where, n

	switch where {
	case regionSidebar:
		if n >= len(m.Playlists) {
			return m, nil
		}
		m.focus, m.sidebarCursor = PaneSidebar, n
		m.loading = true
		return m, m.fetchTracks(m.Playlists[n])

	case regionTracks:
		if n >= len(m.Tracks) {
			return m, nil
		}
		m.focus, m.trackCursor = PaneTracks, n
		t := m.Tracks[n]
		if again {
			m.NowPlaying, m.Position, m.Length = nowPlaying(t), 0, t.Duration
			return m, m.play(t)
		}
		return m, m.prefetch(t.VideoID)

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

// handleWheel scrolls the list under the pointer. It deliberately leaves
// focus alone: the keyboard should stay where it was put.
func (m Model) handleWheel(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	var delta int
	switch mouse.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return m, nil
	}

	where, _ := m.hit(mouse.X, mouse.Y)
	switch where {
	case regionSidebar:
		m.sidebarCursor = clamp(m.sidebarCursor+delta, len(m.Playlists))
	case regionTracks:
		m.trackCursor = clamp(m.trackCursor+delta, len(m.Tracks))
		return m.schedulePrefetch()
	}
	return m, nil
}

// positionAt converts a column on the progress row into a playback position.
func (m Model) positionAt(x int) time.Duration {
	start, width := m.barGeometry()
	if width <= 0 || m.Length <= 0 {
		return 0
	}
	offset := x - start
	if offset < 0 {
		offset = 0
	}
	if offset > width {
		offset = width
	}
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
