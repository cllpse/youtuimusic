package ui

import (
	"strings"
	"sync"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The sheet is the app's keys, drawn by bubbles' own help view from the
// bindings in keys.go — the same ones the handlers dispatch on.
//
// labelKeys names it. labelHelp is the way in: one character at the end of
// the status bar, which is where a window keeps the thing that explains it,
// and the character every program has meant this by for forty years.
const (
	labelKeys = "Keys"
	labelHelp = "?"
)

// sheetHeader is the title and the blank line under it, as the popovers have.
const sheetHeader = 2

// helpView is the template the sheet is drawn from — copied per frame, since
// the only thing set on it is the width it has to fit in.
//
// Its own styles are dropped. bubbles' defaults are a fixed pair of hex greys,
// #909090 on the keys and #B2B2B2 on the descriptions, chosen against a page
// that is assumed dark; on this app's light theme the descriptions land near
// 1.7:1 and cannot be read. So they come from the scheme instead, by role: the
// key is emphasis, the description is whatever the terminal draws text in, and
// only the separator between columns is allowed to be faint.
var helpView = func() help.Model {
	v := help.New()
	v.ShowAll = true
	v.Styles.FullKey = active
	v.Styles.FullDesc = lipgloss.NewStyle()
	v.Styles.FullSeparator = dim
	v.Styles.Ellipsis = dim
	return v
}()

// helpButtonWidth is the ? at the end of the bar, through the same component
// as every other button.
var helpButtonWidth = buttonWidth(labelHelp)

// sheetLines is the help view's own rendering of the bindings, as lines. A
// width of zero means uncapped, which is how the sheet asks what the columns
// would like to be before deciding what they get.
//
// The result is memoised by width: the bindings never change at runtime, and
// the sheet's layout asks for the same one or two widths several times when it
// draws. Rendering help is the most expensive thing a frame with the sheet up
// does, and it was being done three or four times.
func sheetLines(width int) []string {
	if lines, ok := sheetCache.Load(width); ok {
		return lines.([]string)
	}
	v := helpView
	v.SetWidth(width)
	lines := strings.Split(v.View(appKeys), "\n")
	sheetCache.Store(width, lines)
	return lines
}

// sheetCache holds one rendered help layout per width. The bindings are fixed
// for the process, so a width is a complete key.
var sheetCache sync.Map

// sheetInner is how wide the inside of the box is: what the columns ask for,
// never less than the title row needs, and never more than the screen holds.
// Given less, bubbles drops whole columns with an ellipsis of its own.
func (m Model) sheetInner() int {
	natural := 0
	for _, line := range sheetLines(0) {
		natural = max(natural, lipgloss.Width(line))
	}
	least := lipgloss.Width(labelKeys) + modalCloseWidth
	return min(max(natural, least), max(m.width-modalChrome, 1))
}

// sheetSize is the whole box, border and padding included.
func (m Model) sheetSize() (width, height int) {
	inner := m.sheetInner()
	return inner + modalChrome, len(sheetLines(inner)) + sheetHeader + 2
}

// sheetBounds is where it sits: centred over the list the way a popover is,
// but at the size its contents ask for rather than the size of the space.
//
// Where the list is too short for it, it is centred on the window instead and
// covers the player for as long as it is up. A popover may not do that —
// working the transport must not depend on what you have open — but a sheet is
// read and dismissed, and cutting a row of keys off the bottom to protect a
// button esc can reach past would be the worse trade.
func (m Model) sheetBounds() (x, y, width, height int) {
	width, height = m.sheetSize()
	x = max((m.width-width)/2, 0)
	if height > m.bodyHeight() {
		return x, max((m.height-height)/2, 0), width, height
	}
	return x, tabsHeight + (m.bodyHeight()-height)/2, width, height
}

func (m Model) renderSheet() string {
	inner := m.sheetInner()
	lines := append(
		[]string{titleRow(inner, active.Render(labelKeys)), strings.Repeat(" ", inner)},
		sheetLines(inner)...)
	return modalBox.Render(strings.Join(lines, "\n"))
}

// sheetContains reports whether a point is anywhere on the sheet, border
// included.
func (m Model) sheetContains(x, y int) bool {
	if !m.sheetOpen {
		return false
	}
	sx, sy, width, height := m.sheetBounds()
	return x >= sx && x < sx+width && y >= sy && y < sy+height
}

// sheetCloseButton is where the way out sits, on the header line against the
// inside of the right border — the same place every popover keeps it.
func (m Model) sheetCloseButton() (x, y, width int, ok bool) {
	if !m.sheetOpen {
		return 0, 0, 0, false
	}
	sx, sy, swidth, _ := m.sheetBounds()
	return sx + swidth - modalChrome/2 - modalCloseWidth, sy + 1, modalCloseWidth, true
}

// toggleSheet opens or closes it. A menu behind a sheet is a menu you cannot
// see, so it goes.
func (m Model) toggleSheet() (tea.Model, tea.Cmd) {
	m.sheetOpen, m.menu = !m.sheetOpen, trackMenu{}
	return m, nil
}

// handleSheetKey runs the sheet while it is up. It reports whether it took the
// key: the transport is left to fall through, for the same reason a popover
// leaves it — pausing should not depend on what you happen to be reading —
// and so is quit, which has state to save on the way out. Everything else is
// swallowed, because the list it would work is behind the sheet.
func (m Model) handleSheetKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	k := appKeys
	switch {
	case matches(msg, k.Close, k.Help):
		m.sheetOpen = false
		return m, nil, true
	case matches(msg, k.Quit, k.PlayPause, k.Next, k.Previous, k.Repeat, k.Monochrome):
		return m, nil, false
	}
	return m, nil, true
}

// clickSheet closes it from the button, ignores the rest of it, and leaves the
// player alone: the transport is still down there, and so is the ? that opened
// this.
func (m Model) clickSheet(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	if m.sheetContains(mouse.X, mouse.Y) {
		if x, y, width, ok := m.sheetCloseButton(); ok &&
			mouse.Y == y && mouse.X >= x && mouse.X < x+width {
			m.sheetOpen = false
		}
		return m, nil
	}
	if mouse.Y >= m.playerTop() {
		return m.handleClick(mouse)
	}
	// Anywhere else is away from it, and clicking away from a sheet closes it.
	m.sheetOpen = false
	return m, nil
}

// helpButtonSpan is where the ? sits on the status row: at the far end of the
// band, with the cell of air the other end of it has. It reports false when
// the row is too narrow to hold it, which is also when it is not drawn — the
// renderer and the hit test both come here, so a click lands on the character
// it looks like it should.
func (m Model) helpButtonSpan() (start int, ok bool) {
	room := max(m.width-lipgloss.Width(m.statusBlock()), 0)
	// The track keeps a cell of the band: a bar with nothing but a ? in it
	// would be a bar that stopped saying what is playing.
	if room < helpButtonWidth+2 {
		return 0, false
	}
	return m.width - helpButtonWidth - 1, true
}
