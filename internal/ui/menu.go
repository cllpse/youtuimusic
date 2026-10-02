package ui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// menuGap separates one word from the next where two sit on a line, as the
// kind and the name do in a popover's title. One space reads as a single
// phrase; two read as a label and what it labels.
const menuGap = "  "

// menuItem is a row of the track menu.
type menuItem int

const (
	menuAlbum menuItem = iota
	menuArtist
	menuMix
	menuLike
	menuDislike
	menuCancel
)

// trackMenu is the menu a right-click on a track opens.
//
// confirming is the step between choosing the dislike row and doing it. A
// dislike is the one thing in the menu that changes what the server thinks of
// a track you may have pointed at by accident, and it also changes what plays
// next when the track is the one playing — so it asks once, in place, and the
// same menu answers. No other row asks: going somewhere and liking are both
// undone by going back, and a menu that asked after everything would be a
// dialog rather than a menu.
type trackMenu struct {
	open       bool
	confirming bool
	track      Track
	x, y       int
	cursor     int
}

// menuRow is one line of it.
type menuRow struct {
	item  menuItem
	label string
	// enabled is false when the row has nowhere to go: a single with no
	// album page, or a track whose artist is not linked.
	enabled bool
	// hue is a colour of the row's own: the two rating rows, in the magenta and
	// red their rows take in the list. It says which rating the row is about
	// and not which way it will go — going the other way is still that rating,
	// and a row that turned red for taking a like off would read as a warning
	// about a thing you are allowed to do. The label says the direction.
	//
	// The rows that go somewhere have no colour.
	hue color.Color
}

// dividerAfter is the item the rule follows. Above it are the places this row
// leads — its album, its artist, and the mix built around it, which is a page
// that did not exist until you asked for it. Below it, what you think of the
// row. Going somewhere is the commoner errand of the two and reads first.
//
// The confirm view has two rows and no rule, so the constant is only reached
// when there are rows enough to pass it.
const dividerAfter = 2

func (m Model) menuRows() []menuRow {
	if m.menu.confirming {
		return m.confirmRows()
	}
	return m.rateRows()
}

// confirmRows is what the menu becomes while it is asking. The question is
// the row itself rather than a line of prose above it: a menu is a column of
// things you can do, and "Confirm dislike" is one.
func (m Model) confirmRows() []menuRow {
	p := m.palette()
	return []menuRow{
		{menuDislike, "Confirm dislike", true, p.disliked},
		{menuCancel, "Cancel", true, nil},
	}
}

// rateRows is the menu as it opens: where the track leads, then what you
// think of it.
func (m Model) rateRows() []menuRow {
	t := m.menu.track
	p := m.palette()
	// Each row says what pressing it does, so a track that already carries a
	// rating offers to take it off rather than offering to do it again.
	//
	// English has a word for one of those and not the other — unlike is one,
	// undislike is not — so the dislike says it the long way round. Both rows
	// are the same key they answer to elsewhere: + and -.
	like := menuRow{menuLike, "Like track", t.VideoID != "", p.liked}
	if t.Rating == RatingUp {
		like = menuRow{menuLike, "Unlike track", true, p.liked}
	}
	dislike := menuRow{menuDislike, "Dislike track", t.VideoID != "", p.disliked}
	if t.Rating == RatingDown {
		dislike = menuRow{menuDislike, "Remove dislike", true, p.disliked}
	}
	return []menuRow{
		{menuAlbum, "Go to album", t.AlbumID != "", nil},
		{menuArtist, "Go to artist", t.ArtistID != "", nil},
		// In the colour of the page it opens, the way the button that does the
		// same thing is. A release has no track to build a mix around.
		{menuMix, "Start mix", t.VideoID != "", p.mix},
		like,
		dislike,
	}
}

// itemAtVisual is the item drawn on a line, once the rule is counted, and
// reports false on the rule itself, which is not something you can choose.
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
)

// menuSize is the whole box, borders and padding included.
func (m Model) menuSize() (width, height int) {
	rows := m.menuRows()
	longest := 0
	for _, row := range rows {
		longest = max(longest, lipgloss.Width(row.label))
	}
	// label, then padding and border either side.
	return longest + 2 + 2, len(rows) + 1 + 2
}

// openMenu puts the menu on screen at a point, nudged so that all of it
// fits, with the first row that can be chosen already chosen.
//
// Not simply the first row: the ones that go somewhere are dead on a track
// with no album page or no linked artist, and a menu that opens on a dead row
// swallows the first thing you press.
func (m Model) openMenu(t Track, x, y int) Model {
	m.menu = trackMenu{open: true, track: t, confirming: false}
	for i, row := range m.menuRows() {
		if row.enabled {
			m.menu.cursor = i
			break
		}
	}
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
		style := lipgloss.NewStyle()
		switch {
		case !row.enabled:
			style = dim
		case row.hue != nil:
			// A row with a colour of its own keeps it, and is filled when it is
			// the one under the cursor: the colour says what the row is about
			// and the fill says it is chosen, one each, as on a row of the list.
			style = style.Foreground(row.hue)
		}
		if row.enabled && i == m.menu.cursor {
			style = style.Background(m.highlightColor())
		}
		line = style.Render(line)
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
		if m.menu.confirming {
			// esc steps back to the menu rather than dismissing it, the way
			// esc steps back through a stack of popovers. Dismissing the lot
			// from here would make the confirm step a trap: choosing to look
			// at the menu again would not put it back.
			m = m.stepBack()
			return m, nil
		}
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
	case matches(msg, k.Monochrome):
		// The theme is about the screen, not the menu, so it works here too.
		return m.toggleMono()
	}
	return m, nil
}

// activate runs a menu row and closes the menu — except the dislike row,
// which asks first, and whose answer closes it.
func (m Model) activate(row int) (tea.Model, tea.Cmd) {
	rows := m.menuRows()
	if row < 0 || row >= len(rows) || !rows[row].enabled {
		return m, nil
	}
	t := m.menu.track

	switch rows[row].item {
	case menuDislike:
		if !m.menu.confirming {
			return m.askDislike()
		}
		m.menu = trackMenu{}
		return m.rateTrack(t, RatingDown)
	case menuCancel:
		return m.stepBack(), nil
	}

	m.menu = trackMenu{}
	switch rows[row].item {
	case menuMix:
		return m.mixFrom(t)
	case menuLike:
		return m.rateTrack(t, RatingUp)
	case menuAlbum:
		return m.goTo(Playlist{ID: t.AlbumID, Title: t.Album, kind: tabAlbum})
	case menuArtist:
		return m.goTo(Playlist{ID: t.ArtistID, Title: t.Artist, kind: tabArtist})
	}
	return m, nil
}

// askDislike turns the menu into the question. The cursor goes to the cancel
// row, so that entering twice by accident — the gesture this step exists for
// — does nothing at all. Confirm is one deliberate move from anywhere.
func (m Model) askDislike() (tea.Model, tea.Cmd) {
	m.menu.confirming = true
	m.menu.cursor = 1 // cancel
	return m, nil
}

// stepBack puts the menu back the way it opened. The cursor lands on the
// dislike row, since that is where the reader just was.
func (m Model) stepBack() Model {
	m.menu.confirming = false
	for i, row := range m.rateRows() {
		if row.item == menuDislike {
			m.menu.cursor = i
		}
	}
	return m
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
