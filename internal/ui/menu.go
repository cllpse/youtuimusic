package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// More Material Design icons, again by their glyphnames.json names.
const (
	iconAlbum  = "\U000f0025" // md-album
	iconArtist = "\U000f0803" // md-account_music
)

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
	icon  string
	label string
	// enabled is false when the row has nowhere to go: a single with no
	// album page, or a track whose artist is not linked.
	enabled bool
}

func (m Model) menuRows() []menuRow {
	t := m.menu.track
	like := iconThumbUpOff
	if t.Rating == RatingUp {
		like = iconThumbUp
	}
	return []menuRow{
		{menuLike, like, "Like track", true},
		{menuAlbum, iconAlbum, "Go to album", t.AlbumID != ""},
		{menuArtist, iconArtist, "Go to artist", t.ArtistID != ""},
	}
}

var (
	menuBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1)
	menuSelected = lipgloss.NewStyle().Bold(true).Foreground(accent)
)

// menuSize is the whole box, borders and padding included.
func (m Model) menuSize() (width, height int) {
	longest := 0
	for _, row := range m.menuRows() {
		longest = max(longest, lipgloss.Width(row.label))
	}
	// icon, space, label, then padding and border either side.
	return longest + 2 + 2 + 2, len(m.menuRows()) + 2
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

	lines := make([]string, 0, len(rows))
	for i, row := range rows {
		line := pad(row.icon+" "+row.label, inner)
		switch {
		case !row.enabled:
			line = dim.Render(line)
		case i == m.menu.cursor:
			line = menuSelected.Render(line)
		}
		lines = append(lines, line)
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
	row := y - m.menu.y - 1 // past the top border
	if row < 0 || row >= len(m.menuRows()) {
		return 0, false // on the border
	}
	return row, true
}

// handleMenuKey runs the menu while it is open, swallowing everything else:
// a menu that lets keystrokes through to the list underneath is a menu you
// cannot trust.
func (m Model) handleMenuKey(key string) (tea.Model, tea.Cmd) {
	rows := m.menuRows()
	switch key {
	case "esc", "q", "ctrl+c":
		m.menu = trackMenu{}
	case "up", "k":
		m.menu.cursor = clamp(m.menu.cursor-1, len(rows))
	case "down", "j":
		m.menu.cursor = clamp(m.menu.cursor+1, len(rows))
	case "enter", " ", "space":
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
	m.setRating(t.VideoID, r)
	return m, m.rate(t.VideoID, r, previous)
}

// goTo shows an album or an artist. It takes over the view rather than
// joining the tab row: it is somewhere you went, not somewhere you keep.
func (m Model) goTo(tab Playlist) (tea.Model, tea.Cmd) {
	if tab.Title == "" {
		tab.Title = "Album"
		if tab.kind == tabArtist {
			tab.Title = "Artist"
		}
	}
	next, cmd := m.enterDetour(tab)
	return next, cmd
}
