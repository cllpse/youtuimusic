package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The popover is inset from the edges so that what it covers is still
// visible around it, and reads as being in front rather than instead.
const (
	modalMarginX = 8
	modalMarginY = 1
	modalChrome  = 4 // border and padding, both sides
	modalHeader  = 2 // the title, and the blank line under it
)

var modalBox = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(accent).
	Padding(0, 1)

// modalBounds is where the popover sits: centred on the list, and inside
// it. It deliberately does not reach the tabs or the player — covering the
// transport would mean the thing playing could not be paused.
func (m Model) modalBounds() (x, y, width, height int) {
	available := m.bodyHeight()
	width = min(max(m.width-2*modalMarginX, 24), m.width)
	height = min(max(available-2*modalMarginY, 4), available)
	return (m.width - width) / 2, tabsHeight + (available-height)/2, width, height
}

func (m Model) modalContentWidth() int {
	_, _, width, _ := m.modalBounds()
	return max(width-modalChrome, 1)
}

// modalListHeight is how many tracks it shows at once.
func (m Model) modalListHeight() int {
	_, _, _, height := m.modalBounds()
	return max(height-2-modalHeader, 1)
}

func (m Model) modalIcon() string {
	if m.detour.tab.kind == tabArtist {
		return iconArtist
	}
	return iconAlbum
}

func (m Model) renderModal() string {
	inner := m.modalContentWidth()
	height := m.modalListHeight()

	title := pad(truncate(m.modalIcon()+" "+m.detour.tab.Title, inner), inner)
	lines := make([]string, 0, modalHeader+height)
	lines = append(lines, active.Render(title), strings.Repeat(" ", inner))

	if m.loading && len(m.detour.tracks) == 0 {
		lines = append(lines, pad(m.spin.View()+" "+dim.Render("Loading…"), inner))
		for len(lines) < modalHeader+height {
			lines = append(lines, strings.Repeat(" ", inner))
		}
		return modalBox.Render(strings.Join(lines, "\n"))
	}

	bar := scrollbarFor(len(m.detour.tracks), m.detour.offset, height)
	listWidth := inner
	if bar != nil {
		listWidth -= scrollbarWidth
	}
	showRating := m.detour.tab.ID != likedPlaylistID

	for i := range height {
		row := i + m.detour.offset
		line := ""
		if row < len(m.detour.tracks) {
			highlighted := row == m.detour.cursor
			line = m.trackLine(m.detour.tracks[row], listWidth, showRating, highlighted)
			if highlighted {
				line = selected.Render(line)
			}
		}
		line = pad(line, listWidth)
		if bar != nil {
			line += bar[i]
		}
		lines = append(lines, line)
	}
	return modalBox.Render(strings.Join(lines, "\n"))
}

// modalContains reports whether a point is anywhere on the popover.
func (m Model) modalContains(x, y int) bool {
	if !m.detour.active {
		return false
	}
	mx, my, width, height := m.modalBounds()
	return x >= mx && x < mx+width && y >= my && y < my+height
}

// modalHit reports which track a point is over.
func (m Model) modalHit(x, y int) (int, bool) {
	if !m.modalContains(x, y) {
		return 0, false
	}
	mx, my, width, _ := m.modalBounds()
	if x < mx+2 || x >= mx+width-2 { // border and padding
		return 0, false
	}
	line := y - my - 1 - modalHeader
	if line < 0 || line >= m.modalListHeight() {
		return 0, false
	}
	row := line + m.detour.offset
	if row >= len(m.detour.tracks) {
		return 0, false
	}
	return row, true
}

// scrollDetour moves the popover's window without moving its selection, the
// way the wheel behaves on the list underneath.
func (m *Model) scrollDetour(delta int) {
	height := m.modalListHeight()
	m.detour.offset = min(max(m.detour.offset+delta, 0),
		max(0, len(m.detour.tracks)-height))
}

// handleModalKey runs the popover. It reports whether it took the key: the
// transport keys are deliberately left to fall through, because pausing
// should not depend on what is on top.
func (m Model) handleModalKey(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "esc":
		next, cmd := m.leaveDetour()
		return next, cmd, true

	case "up", "k":
		m.moveDetour(-1)
	case "down", "j":
		m.moveDetour(1)
	case "pgup", "ctrl+u":
		m.moveDetour(-m.modalListHeight())
	case "pgdown", "ctrl+d":
		m.moveDetour(m.modalListHeight())
	case "home", "g":
		m.moveDetour(-len(m.detour.tracks))
	case "end", "G":
		m.moveDetour(len(m.detour.tracks))

	case "enter":
		if t, ok := m.selectedDetourTrack(); ok {
			next, cmd := m.start(t)
			return next, cmd, true
		}
	case "+", "=":
		if t, ok := m.selectedDetourTrack(); ok {
			next, cmd := m.rateTrack(t, RatingUp)
			return next, cmd, true
		}
	case "-", "_":
		if t, ok := m.selectedDetourTrack(); ok {
			next, cmd := m.rateTrack(t, RatingDown)
			return next, cmd, true
		}

	case "/", "left", "h", "right", "l", "tab", "shift+tab":
		// Working the thing underneath while it is covered would be a
		// surprise. The way out is esc.

	default:
		return m, nil, false
	}
	return m, nil, true
}

// clickModal selects a row, plays it on a second click, or dismisses the
// popover when the click lands outside it.
func (m Model) clickModal(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	if !m.modalContains(mouse.X, mouse.Y) {
		return m.leaveDetour()
	}
	row, ok := m.modalHit(mouse.X, mouse.Y)
	if !ok {
		return m, nil
	}
	again := m.isSecondClick(regionModal, row)
	m.lastClickAt, m.lastClickRegion, m.lastClickRow = m.clock(), regionModal, row

	m.detour.cursor = row
	m.moveDetour(0)
	t := m.detour.tracks[row]
	if again {
		return m.start(t)
	}
	return m, m.prefetch(t.VideoID)
}
