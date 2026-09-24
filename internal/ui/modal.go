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

// iconSearch heads the search popover's input.
const iconSearch = "\U000f0349" // md-magnify

func (m Model) modalIcon() string {
	switch m.detour.tab.kind {
	case tabArtist:
		return iconArtist
	case tabSearch:
		return iconSearch
	}
	return iconAlbum
}

// openSearch opens the popover on an empty query, with the input focused.
func (m Model) openSearch() (tea.Model, tea.Cmd) {
	m.menu = trackMenu{}
	m.detour = detour{
		active: true,
		tab:    Playlist{Title: "Search", kind: tabSearch},
		typing: true,
	}
	return m, nil
}

// modalHeader is the popover's first line: what it is showing, or the
// search box being typed into.
func (m Model) modalHeader(inner int) string {
	if m.detour.tab.kind != tabSearch {
		return active.Render(pad(truncate(m.modalIcon()+" "+m.detour.tab.Title, inner), inner))
	}
	query := m.detour.query
	if m.detour.typing {
		query += "█"
	} else if query == "" {
		query = dim.Render("type to search")
	}
	return active.Render(iconSearch+menuGap) + pad(truncate(query, max(inner-3, 0)), inner-3)
}

// typeInto runs the search box. Everything reaches it while it has focus,
// because everything is text — including the space bar, which would
// otherwise pause what is playing mid-word.
func (m Model) typeInto(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "ctrl+c":
		return m, tea.Quit, true
	case "esc":
		next, cmd := m.leaveDetour()
		return next, cmd, true
	case "enter":
		m.detour.typing = false
		if m.detour.query == "" {
			return m, nil, true
		}
		return m, batch(m.startLoading(), m.runSearch(m.detour.query)), true
	case "backspace":
		if q := m.detour.query; q != "" {
			m.detour.query = q[:len(q)-1]
		}
		return m, nil, true
	case "space":
		m.detour.query += " "
		return m, nil, true
	default:
		if len(key) == 1 {
			m.detour.query += key
		}
		return m, nil, true
	}
}

func (m Model) renderModal() string {
	inner := m.modalContentWidth()
	height := m.modalListHeight()

	lines := make([]string, 0, modalHeader+height)
	lines = append(lines, m.modalHeader(inner), strings.Repeat(" ", inner))

	if m.detour.tab.kind == tabSearch && !m.loading && len(m.detour.tracks) == 0 {
		note := dim.Render("type to search")
		if !m.detour.typing && m.detour.query != "" {
			note = dim.Render("nothing found")
		}
		lines = append(lines, lipgloss.Place(inner, height,
			lipgloss.Center, lipgloss.Center, note))
		return modalBox.Render(strings.Join(lines, "\n"))
	}

	if m.loading && len(m.detour.tracks) == 0 {
		lines = append(lines, pad(m.spin.View()+" "+dim.Render("Loading…"), inner))
		for len(lines) < modalHeader+height {
			lines = append(lines, strings.Repeat(" ", inner))
		}
		return modalBox.Render(strings.Join(lines, "\n"))
	}

	lines = append(lines, trackTable{
		tracks:     m.detour.tracks,
		cursor:     m.detour.cursor,
		offset:     m.detour.offset,
		width:      inner,
		height:     height,
		showRating: m.detour.tab.ID != likedPlaylistID,
		playing:    m.playing.VideoID,
	}.rows()...)
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
	m.detour.offset = clampOffset(m.detour.offset+delta,
		m.modalListHeight(), len(m.detour.tracks))
}

// handleModalKey runs the popover. It reports whether it took the key: the
// transport keys are deliberately left to fall through, because pausing
// should not depend on what is on top.
func (m Model) handleModalKey(key string) (tea.Model, tea.Cmd, bool) {
	if m.detour.typing {
		return m.typeInto(key)
	}
	switch key {
	case "/":
		if m.detour.tab.kind == tabSearch {
			m.detour.typing = true
			return m, nil, true
		}
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
			next, cmd := m.open(t)
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

	case "left", "h", "right", "l", "tab", "shift+tab":
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
		return m.open(t)
	}
	return m, m.prefetch(t.VideoID)
}
