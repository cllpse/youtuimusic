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
	// albumInset sits an album inside the artist it usually opened from, so
	// that the artist is still there around it rather than replaced by it.
	//
	// It insets vertically as well, by a row. Two columns either side only
	// uncovers the artist's own border, which reads as a double line rather
	// than as something behind; a row off the top and bottom uncovers the
	// header it is showing, which reads as what it is.
	albumInsetX = 2
	albumInsetY = 1
)

// iconClose is the way out of a popover: the times sign, which every font
// has and nobody has to learn.
const iconClose = "×"

// The way back and the way out are the same inside-out fill the transport's
// buttons use when they are live.
var modalBackStyle = lipgloss.NewStyle().
	Background(emphasis).
	Foreground(background).
	Bold(true)

// Each is its character with a cell either side, all of it clickable.
const (
	modalBackWidth  = 3
	modalCloseWidth = 3
)

var modalBox = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(foreground).
	Padding(0, 1)

// modalBounds is where the popover sits: centred on the list, and inside
// it. It deliberately does not reach the tabs or the player — covering the
// transport would mean the thing playing could not be paused.
func (m Model) modalBounds() (x, y, width, height int) {
	available := m.bodyHeight()
	insetX, insetY := 0, 0
	if m.detour.tab.kind == tabAlbum {
		insetX, insetY = albumInsetX, albumInsetY
	}
	width = min(max(m.width-2*modalMarginX-2*insetX, 24), m.width)
	height = min(max(available-2*modalMarginY-2*insetY, 4), available)
	return (m.width - width) / 2, tabsHeight + (available-height)/2, width, height
}

func (m Model) modalContentWidth() int {
	_, _, width, _ := m.modalBounds()
	return max(width-modalChrome, 1)
}

// modalListHeight is the table's whole block, its header included.
func (m Model) modalListHeight() int {
	_, _, _, height := m.modalBounds()
	return max(height-2-modalHeader, 2)
}

// modalRowsHeight is how many tracks it shows at once.
func (m Model) modalRowsHeight() int { return max(m.modalListHeight()-headerRows, 1) }

const (
	// iconSearch heads the search popover's input.
	iconSearch = "\U000f0349" // md-magnify
	// iconBack marks a popover that has another behind it.
	iconBack = "\U000f004d" // md-arrow_left
)

func (m Model) modalIcon() string {
	switch m.detour.tab.kind {
	case tabArtist:
		return iconArtist
	case tabSearch:
		return iconSearch
	}
	return iconAlbum
}

// modalKind names what the popover is showing. The icon alone leaves it to
// be recognised; the word says it.
func (m Model) modalKind() string {
	if m.detour.tab.kind == tabArtist {
		return "Artist"
	}
	return "Album"
}

// openSearch opens the popover on an empty query, with the input focused.
func (m Model) openSearch() (tea.Model, tea.Cmd) {
	// A search is a fresh start rather than another step, so there is
	// nothing behind it to go back to.
	m.menu, m.history = trackMenu{}, nil
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
		// The way back stays against the left edge, drawn the way the
		// transport's buttons are so that it reads as something to press.
		// What it is showing sits in the middle of the row, where a title
		// belongs, and gives way to the button rather than under it.
		back, taken := "", 0
		if m.showsBack() {
			back = modalBackStyle.Render(" " + iconBack + " ")
			taken = modalBackWidth
		}
		// And the way out stays against the right, which is where a window
		// keeps it.
		close := modalBackStyle.Render(" " + iconClose + " ")
		right := modalCloseWidth

		prefix := m.modalIcon() + menuGap + m.modalKind() + menuGap
		room := max(inner-taken-right-lipgloss.Width(prefix), 0)
		title := active.Render(prefix) + truncate(m.detour.tab.Title, room)

		width := lipgloss.Width(title)
		// Centred on the whole row, but never under either button.
		start := min(max((inner-width)/2, taken), max(inner-right-width, taken))
		return back + strings.Repeat(" ", start-taken) + title +
			strings.Repeat(" ", max(inner-right-start-width, 0)) + close
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
		lines = append(lines, pad(m.loader(), inner))
		for len(lines) < modalHeader+height {
			lines = append(lines, strings.Repeat(" ", inner))
		}
		return modalBox.Render(strings.Join(lines, "\n"))
	}

	lines = append(lines, trackTable{
		sort:        m.sort,
		highlight:   m.highlightColor(),
		now:         m.clock(),
		tracks:      m.detour.tracks,
		cursor:      m.detour.cursor,
		offset:      m.detour.offset,
		width:       inner,
		height:      height,
		showRating:  m.detour.tab.ID != likedPlaylistID,
		titleOnly:   m.detour.tab.kind == tabAlbum,
		playing:     m.playing.VideoID,
		more:        m.detour.more.More(),
		loadingMore: m.loadingMore,
		loader:      m.loader(),
	}.rows()...)
	return modalBox.Render(strings.Join(lines, "\n"))
}

// modalCloseButton is where the way out sits. Every popover has one.
func (m Model) modalCloseButton() (x, y, width int, ok bool) {
	if !m.detour.active || m.detour.tab.kind == tabSearch {
		return 0, 0, 0, false
	}
	mx, my, mwidth, _ := m.modalBounds()
	// Against the inside of the right border, on the header line.
	return mx + mwidth - modalChrome/2 - modalCloseWidth, my + 1, modalCloseWidth, true
}

// showsBack reports whether the popover needs a way back drawn on it.
//
// An album does not: it sits inset on the artist it opened from, which is
// still on the screen around it, so the way back is the artist — clicking it,
// or esc. A button pointing at something already visible is furniture.
func (m Model) showsBack() bool {
	return len(m.history) > 0 && m.detour.tab.kind != tabAlbum
}

// modalBackButton is where the way back sits, when there is one.
func (m Model) modalBackButton() (x, y, width int, ok bool) {
	if !m.detour.active || m.detour.tab.kind == tabSearch || !m.showsBack() {
		return 0, 0, 0, false
	}
	mx, my, _, _ := m.modalBounds()
	// Past the box's border and its padding, on the header line.
	return mx + modalChrome/2, my + 1, modalBackWidth, true
}

// modalContains reports whether a point is anywhere on the popover.
func (m Model) modalContains(x, y int) bool {
	if !m.detour.active {
		return false
	}
	mx, my, width, height := m.modalBounds()
	return x >= mx && x < mx+width && y >= my && y < my+height
}

// behindContains reports whether a point is on the popover immediately
// behind the front one. Only that one: with the album inset on its artist
// there is never a third, and popping blindly until a point lands would
// close more than was clicked.
func (m Model) behindContains(x, y int) bool {
	n := len(m.history)
	if n == 0 {
		return false
	}
	under := m
	under.detour = m.history[n-1]
	mx, my, width, height := under.modalBounds()
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
	line := y - my - 1 - modalHeader - headerRows
	if line < 0 || line >= m.modalRowsHeight() {
		return 0, false
	}
	row := line + m.detour.offset
	if row >= m.detourRowCount() {
		return 0, false
	}
	return row, true
}

// viewingDetourMoreRow reports whether the popover is showing its offer of
// another page.
func (m Model) viewingDetourMoreRow() bool {
	return m.detour.more.More() &&
		m.detour.offset+m.modalRowsHeight() > len(m.detour.tracks)
}

// scrollDetour moves the popover's window without moving its selection, the
// way the wheel behaves on the list underneath.
func (m *Model) scrollDetour(delta int) {
	m.detour.offset = clampOffset(m.detour.offset+delta,
		m.modalRowsHeight(), m.detourRowCount())
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
		// A search from inside a popover is still a fresh start, not
		// another step on from wherever this got to.
		next, cmd := m.openSearch()
		return next, cmd, true
	case "esc":
		next, cmd := m.leaveDetour()
		return next, cmd, true

	case "up", "k":
		m.moveDetour(-1)
		return m.afterDetourMove()
	case "down", "j":
		m.moveDetour(1)
		return m.afterDetourMove()
	case "pgup", "ctrl+u":
		m.moveDetour(-m.modalRowsHeight())
		return m.afterDetourMove()
	case "pgdown", "ctrl+d":
		m.moveDetour(m.modalRowsHeight())
		return m.afterDetourMove()
	case "home", "g":
		m.moveDetour(-m.detourRowCount())
		return m.afterDetourMove()
	case "end", "G":
		m.moveDetour(m.detourRowCount())
		return m.afterDetourMove()

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
		// The player is not "away". Working the transport while a popover
		// is open should work the transport, not dismiss what you opened.
		if mouse.Y >= tabsHeight+m.bodyHeight() {
			return m.handleClick(mouse)
		}
		// On the popover behind this one, which the inset leaves showing:
		// step back to it. Dismissing the lot when the thing clicked is
		// visibly there would be a surprise.
		if m.behindContains(mouse.X, mouse.Y) {
			return m.leaveDetour()
		}
		// Clicking anywhere else dismisses the lot; esc is what steps back.
		return m.closeDetour()
	}
	if x, y, width, ok := m.modalCloseButton(); ok &&
		mouse.Y == y && mouse.X >= x && mouse.X < x+width {
		return m.closeDetour()
	}
	if x, y, width, ok := m.modalBackButton(); ok &&
		mouse.Y == y && mouse.X >= x && mouse.X < x+width {
		return m.leaveDetour()
	}
	row, ok := m.modalHit(mouse.X, mouse.Y)
	if !ok {
		return m, nil
	}
	if row == len(m.detour.tracks) {
		// The row that offers the next page is not a track.
		m.detour.cursor = row
		return m.fetchMore(true)
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
