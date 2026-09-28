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
	// stackInset sits each popover inside the one it opened from, so that
	// what is behind is still there around it rather than replaced by it.
	// It is a step per level and not a rule about albums: a search with an
	// artist on it and an album on that is three, and the search has to be
	// wider than the artist to be seen under it at all.
	//
	// It insets vertically as well, by a row. Two columns either side only
	// uncovers the one behind's own border, which reads as a double line
	// rather than as something behind; a row off the top and bottom gives
	// that border a row of its own, which reads as what it is.
	stackInsetX = 2
	stackInsetY = 1
)

// labelTypeToSearch waits on you, which is what its ellipsis says.
const labelTypeToSearch = "Type to search…"

// labelClose closes the popover it is drawn on, stepping back to whatever was
// behind it. It says what it does and then names the key that does the same
// thing, the way the transport's buttons do — more use than a times sign,
// which says neither.
const labelClose = "Close (esc)"

// The way out is a button like the transport's, through the same component.
var modalCloseWidth = buttonWidth(labelClose)

var modalBox = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(foreground).
	Padding(0, 1)

// modalBounds is where the popover sits: centred on the list, and inside
// it. It deliberately does not reach the tabs or the player — covering the
// transport would mean the thing playing could not be paused.
func (m Model) modalBounds() (x, y, width, height int) {
	return m.modalBoundsAt(len(m.history))
}

// modalBoundsAt is where the popover at a depth sits: the one at the bottom
// of the stack takes the whole space and each one on top of it steps in.
func (m Model) modalBoundsAt(depth int) (x, y, width, height int) {
	available := m.bodyHeight()
	insetX, insetY := depth*stackInsetX, depth*stackInsetY
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

// modalKind names what the popover is showing.
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
		cursor: noRow,
	}
	return m, nil
}

// titleRow lays a popover's first line out: what it is showing in the middle
// of the row, where a title belongs, and one button against the right, where a
// window keeps the way out. There was a way back beside it until the two came
// to do the same thing: closing a popover steps back to whatever was behind it.
//
// The title arrives styled and cut to fit, because what it is made of differs —
// a kind and a name on a popover, one word on the keys sheet — and where it
// goes does not.
func titleRow(inner int, title string) string {
	close := renderButton(labelClose, buttonDefault, nil)
	width := lipgloss.Width(title)
	start := min(max((inner-width)/2, 0), max(inner-modalCloseWidth-width, 0))
	return strings.Repeat(" ", start) + title +
		strings.Repeat(" ", max(inner-modalCloseWidth-start-width, 0)) + close
}

// modalHeader is the popover's first line: what it is showing, or the
// search box being typed into.
func (m Model) modalHeader(inner int) string {
	if m.detour.tab.kind != tabSearch {
		// What it is showing sits in the middle of the row, where a title
		// belongs, and gives way to the button rather than running under it.
		prefix := m.modalKind() + menuGap
		room := max(inner-modalCloseWidth-lipgloss.Width(prefix), 0)
		return titleRow(inner, active.Render(prefix)+truncate(m.detour.tab.Title, room))
	}
	query := m.detour.query
	if m.detour.typing {
		query += "█"
	} else if query == "" {
		query = dim.Render(labelTypeToSearch)
	}
	close := renderButton(labelClose, buttonDefault, nil)
	room := max(inner-modalCloseWidth, 0)
	return pad(truncate(query, room), room) + close
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
		if m.detour.query == "" {
			return m, nil, true
		}
		// The keys stay in the input: results arrive with nothing chosen,
		// and down is what goes into them.
		m.detour.searched = m.detour.query
		return m, batch(m.startLoading(), m.runSearch(m.detour.query)), true
	case "down":
		if len(m.detour.tracks) == 0 {
			return m, nil, true
		}
		m.detour.typing = false
		m.detour.cursor, m.detour.offset = 0, 0
		return m.afterDetourMove()
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
	// Under the search box a rule, not a blank: the box is something being
	// typed into and the rest of the popover is the answer, and a line is
	// what says where one stops. It is there whether or not there is an
	// answer yet.
	under := strings.Repeat(" ", inner)
	if m.detour.tab.kind == tabSearch {
		under = dim.Render(strings.Repeat("─", inner))
	}
	lines = append(lines, m.modalHeader(inner), under)

	if m.detour.tab.kind == tabSearch && !m.loading && len(m.detour.tracks) == 0 {
		note := dim.Render(labelTypeToSearch)
		if m.detour.searched != "" {
			// What was run, not what is being typed: mid-word there is
			// nothing to have found yet. No ellipsis either — a result
			// rather than an invitation.
			note = dim.Render("Nothing found")
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
		dimmed:      m.dimmedColor(),
		now:         m.clock(),
		tracks:      m.detour.tracks,
		cursor:      m.detour.cursor,
		offset:      m.detour.offset,
		width:       inner,
		height:      height,
		showRating:  m.detour.tab.ID != likedPlaylistID,
		accent:      accentOf(m.detour.tab.ID),
		titleOnly:   m.detour.tab.kind == tabAlbum,
		playing:     m.playing.VideoID,
		more:        m.detour.more.More(),
		loadingMore: m.loadingMore,
		loader:      m.loader(),
	}.rows()...)
	return modalBox.Render(strings.Join(lines, "\n"))
}

// modalCloseButton is where the way out sits. Every popover has one,
// including a search: its header draws the button, so the button answers.
func (m Model) modalCloseButton() (x, y, width int, ok bool) {
	if !m.detour.active {
		return 0, 0, 0, false
	}
	mx, my, mwidth, _ := m.modalBounds()
	// Against the inside of the right border, on the header line.
	return mx + mwidth - modalChrome/2 - modalCloseWidth, my + 1, modalCloseWidth, true
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
// behind the front one. Only that one: stepping back one is what a click on
// the strip it leaves showing means, and popping until a point landed would
// close more than was clicked.
func (m Model) behindContains(x, y int) bool {
	n := len(m.history)
	if n == 0 {
		return false
	}
	mx, my, width, height := m.modalBoundsAt(n - 1)
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
func (m Model) handleModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.detour.typing {
		return m.typeInto(msg.String())
	}
	k := appKeys
	switch {
	case matches(msg, k.Search):
		if m.detour.tab.kind == tabSearch {
			m.detour.typing = true
			m.detour.cursor, m.detour.offset = noRow, 0
			return m, nil, true
		}
		// A search from inside a popover is still a fresh start, not
		// another step on from wherever this got to.
		next, cmd := m.openSearch()
		return next, cmd, true
	case matches(msg, k.Close):
		next, cmd := m.leaveDetour()
		return next, cmd, true

	case matches(msg, k.Up):
		if m.detour.tab.kind == tabSearch && m.detour.cursor <= 0 {
			// Off the top of the results is the input, not the top of the
			// results again.
			m.detour.typing = true
			m.detour.cursor, m.detour.offset = noRow, 0
			return m, nil, true
		}
		m.moveDetour(-1)
		return m.afterDetourMove()
	case matches(msg, k.Down):
		m.moveDetour(1)
		return m.afterDetourMove()
	case matches(msg, k.PageUp):
		m.moveDetour(-m.modalRowsHeight())
		return m.afterDetourMove()
	case matches(msg, k.PageDown):
		m.moveDetour(m.modalRowsHeight())
		return m.afterDetourMove()
	case matches(msg, k.Top):
		m.moveDetour(-m.detourRowCount())
		return m.afterDetourMove()
	case matches(msg, k.Bottom):
		m.moveDetour(m.detourRowCount())
		return m.afterDetourMove()

	case matches(msg, k.Open):
		if t, ok := m.selectedDetourTrack(); ok {
			next, cmd := m.open(t)
			return next, cmd, true
		}
	case matches(msg, k.Like):
		if t, ok := m.selectedDetourTrack(); ok {
			next, cmd := m.rateTrack(t, RatingUp)
			return next, cmd, true
		}
	case matches(msg, k.Dislike):
		if t, ok := m.selectedDetourTrack(); ok {
			next, cmd := m.rateTrack(t, RatingDown)
			return next, cmd, true
		}

	case matches(msg, k.PrevTab, k.NextTab):
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
		// It closes the popover it is on, not everything under it: an album
		// closed from the artist it opened from should leave the artist,
		// which is still on the screen behind it. Same as esc. Clicking away
		// from the lot is what dismisses the lot.
		return m.leaveDetour()
	}
	row, ok := m.modalHit(mouse.X, mouse.Y)
	if !ok {
		return m, nil
	}
	// Clicking into the results is choosing one, so the keys stop going to
	// the search input if that is where they were.
	m.detour.typing = false
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
