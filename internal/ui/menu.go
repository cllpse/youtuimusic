package ui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// More Material Design icons, again by their glyphnames.json names.

// menuGap separates one word from the next where two sit on a line — not an
// icon from its label, which sit against each other. One reads as
// cramped: these glyphs are drawn tight inside their cell.
const menuGap = "  "

// menuItem is a row of the track menu.
type menuItem int

const (
	menuLike menuItem = iota
	menuAlbum
	menuArtist
)

// trackMenu is the menu a right-click on a track opens.
type trackMenu struct {
	open   bool
	track  Track
	x, y   int
	cursor int
}

// menuRow is one line of it.
type menuRow struct {
	item  menuItem
	label string
	// enabled is false when the row has nowhere to go: a single with no
	// album page, or a track whose artist is not linked.
	enabled bool
	// hue is a colour of the row's own, for the one row that rates the track.
	// It says what pressing it does rather than what the track is: magenta to
	// like, red to take a like away, which is the one row in the app that
	// removes something. The rows that go somewhere have none.
	//
	// The transport's like button is magenta either way round, deliberately:
	// down there the colour says which button it is among six, and the label
	// says which way it will go.
	hue color.Color
}

// dividerAfter is the item the rule follows. Liking is about this track;
// everything under the rule is about going somewhere else.
const dividerAfter = 0

func (m Model) menuRows() []menuRow {
	t := m.menu.track
	// The row says what pressing it does, so a liked track offers to undo
	// it rather than offering to do it again.
	like := menuRow{menuLike, "Like track", true, liked}
	if t.Rating == RatingUp {
		like = menuRow{menuLike, "Unlike track", true, disliked}
	}
	return []menuRow{
		like,
		{menuAlbum, "Go to album…", t.AlbumID != "", nil},
		{menuArtist, "Go to artist…", t.ArtistID != "", nil},
	}
}

// visualRow is the line an item is drawn on, once the rule is counted.
func visualRow(item int) int {
	if item > dividerAfter {
		return item + 1
	}
	return item
}

// itemAtVisual is the reverse, and reports false on the rule itself, which
// is not something you can choose.
func itemAtVisual(line, items int) (int, bool) {
	var item int
	switch {
	case line <= dividerAfter:
		item = line
	case line == dividerAfter+1:
		return 0, false
	default:
		item = line - 1
	}
	if item < 0 || item >= items {
		return 0, false
	}
	return item, true
}

var (
	menuBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(foreground).
		Padding(0, 1)
	menuSelected = lipgloss.NewStyle().Bold(true).Foreground(emphasis)
)

// menuSize is the whole box, borders and padding included.
func (m Model) menuSize() (width, height int) {
	longest := 0
	for _, row := range m.menuRows() {
		longest = max(longest, lipgloss.Width(row.label))
	}
	// label, then padding and border either side.
	return longest + 2 + 2, len(m.menuRows()) + 1 + 2
}

// openMenu puts the menu on screen at a point, nudged so that all of it
// fits.
func (m Model) openMenu(t Track, x, y int) Model {
	m.menu = trackMenu{open: true, track: t}
	width, height := m.menuSize()
	m.menu.x = min(max(x, 0), max(0, m.width-width))
	m.menu.y = min(max(y, 0), max(0, m.height-height))
	return m
}

func (m Model) renderMenu() string {
	rows := m.menuRows()
	width, _ := m.menuSize()
	inner := width - 4 // padding and border

	lines := make([]string, 0, len(rows)+1)
	for i, row := range rows {
		line := pad(row.label, inner)
		switch {
		case !row.enabled:
			line = dim.Render(line)
		case row.hue != nil:
			// A row with a colour of its own keeps it, and takes the weight
			// rather than the emphasis when it is the one under the cursor:
			// two ways to say a thing, one each, as on a row of the list.
			line = lipgloss.NewStyle().
				Foreground(row.hue).
				Bold(i == m.menu.cursor).
				Render(line)
		case i == m.menu.cursor:
			line = menuSelected.Render(line)
		}
		lines = append(lines, line)
		if i == dividerAfter {
			lines = append(lines, dim.Render(strings.Repeat("─", inner)))
		}
	}
	return menuBox.Render(strings.Join(lines, "\n"))
}

// menuContains reports whether a point is anywhere on the menu, border
// included.
func (m Model) menuContains(x, y int) bool {
	if !m.menu.open {
		return false
	}
	width, height := m.menuSize()
	return x >= m.menu.x && x < m.menu.x+width &&
		y >= m.menu.y && y < m.menu.y+height
}

// menuHit reports which row of the menu a point is over.
func (m Model) menuHit(x, y int) (int, bool) {
	if !m.menuContains(x, y) {
		return 0, false
	}
	line := y - m.menu.y - 1 // past the top border
	if line < 0 {
		return 0, false // on the border
	}
	return itemAtVisual(line, len(m.menuRows()))
}

// handleMenuKey runs the menu while it is open, swallowing everything else:
// a menu that lets keystrokes through to the list underneath is a menu you
// cannot trust.
func (m Model) handleMenuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	rows, k := m.menuRows(), appKeys
	switch {
	case matches(msg, k.Close):
		m.menu = trackMenu{}
	case matches(msg, k.Quit):
		return m, tea.Quit
	case matches(msg, k.Up):
		m.menu.cursor = clamp(m.menu.cursor-1, len(rows))
	case matches(msg, k.Down):
		m.menu.cursor = clamp(m.menu.cursor+1, len(rows))
	case matches(msg, k.Open, k.PlayPause):
		// Space runs the row here rather than pausing: with a menu open on a
		// track, the nearest thing it can mean is this one.
		return m.activate(m.menu.cursor)
	}
	return m, nil
}

// activate runs a menu row and closes the menu.
func (m Model) activate(row int) (tea.Model, tea.Cmd) {
	rows := m.menuRows()
	if row < 0 || row >= len(rows) || !rows[row].enabled {
		return m, nil
	}
	t := m.menu.track
	m.menu = trackMenu{}

	switch rows[row].item {
	case menuLike:
		return m.rateTrack(t, RatingUp)
	case menuAlbum:
		return m.goTo(Playlist{ID: t.AlbumID, Title: t.Album, kind: tabAlbum})
	case menuArtist:
		return m.goTo(Playlist{ID: t.ArtistID, Title: t.Artist, kind: tabArtist})
	}
	return m, nil
}

// rateTrack rates any track, not only the playing or highlighted one.
func (m Model) rateTrack(t Track, r Rating) (tea.Model, tea.Cmd) {
	if t.VideoID == "" {
		return m, nil
	}
	previous := t.Rating
	if previous == r {
		r = RatingNone
	}
	return m.rated(t.VideoID, r, previous)
}

// goTo shows an album or an artist. It takes over the view rather than
// joining the tab row: it is somewhere you went, not somewhere you keep.
func (m Model) goTo(tab Playlist) (tea.Model, tea.Cmd) {
	next, cmd := m.enterDetour(tab)
	return next, cmd
}
