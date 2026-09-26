package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cllpse/youtuimusic/internal/ytm"
)

func rightClick(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseRight})
}

// linked is a track with somewhere to go.
func linked() []ytm.Track {
	return []ytm.Track{
		{VideoID: "a", Title: "Poly", Artist: "DAPHNI", Album: "Cherry",
			AlbumID: "MPREbCherry", ArtistID: "UCdaphni"},
		{VideoID: "b", Title: "Waves", Artist: "Normani"}, // links nowhere
	}
}

func menuModel(t *testing.T) (Model, *fakeLibrary, *fakeStreams, *fakeAudio) {
	t.Helper()
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	lib.tracks["MPREbCherry"] = []ytm.Track{{VideoID: "c1", Title: "Cherry Track"}}
	lib.tracks["UCdaphni"] = []ytm.Track{{VideoID: "d1", Title: "Daphni Track"}}
	m := wired(t, lib, st, au)
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	m.Tracks = fromAPI(linked())
	return m, lib, st, au
}

// rowAt is the screen position of a menu row.
func rowAt(m Model, item menuItem) (x, y int) {
	for i, row := range m.menuRows() {
		if row.item == item {
			return m.menu.x + 2, m.menu.y + 1 + visualRow(i)
		}
	}
	return -1, -1
}

func TestRightClickOpensTheMenuOnThatTrack(t *testing.T) {
	m, _, _, _ := menuModel(t)

	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)

	if !m.menu.open {
		t.Fatal("no menu")
	}
	if m.menu.track.VideoID != "a" {
		t.Errorf("the menu is on %q", m.menu.track.VideoID)
	}
	if m.trackCursor != 0 {
		t.Errorf("cursor = %d", m.trackCursor)
	}

	got := plain(m.renderMenu())
	for _, want := range []string{"Like track", "Go to album…", "Go to artist…"} {
		if !strings.Contains(got, want) {
			t.Errorf("menu is missing %q:\n%s", want, got)
		}
	}
	// Words only: the thumbs are the one pair of icons left in the app, and
	// they mark a rated row rather than a menu entry.
	for _, icon := range []string{iconThumbUp, iconThumbDown} {
		if strings.Contains(got, icon) {
			t.Errorf("the menu draws an icon:\n%s", got)
		}
	}
}

// Right-clicking anywhere that is not a track is not a track menu.
func TestRightClickElsewhereOpensNothing(t *testing.T) {
	m, _, _, _ := menuModel(t)
	for _, p := range []struct{ x, y int }{
		{2, 1},                    // a tab
		{trackX, m.barRow()},      // the bar
		{trackX, m.controlsRow()}, // the controls
		{trackX, trackRow(50)},    // past the last row
	} {
		next, cmd := m.Update(rightClick(p.x, p.y))
		if drain(t, next.(Model), cmd).menu.open {
			t.Errorf("a menu opened at %d,%d", p.x, p.y)
		}
	}
}

// The menu sits over the frame; opening one must not reflow it.
func TestTheMenuOverlaysWithoutReflowing(t *testing.T) {
	m, _, _, _ := menuModel(t)
	before := strings.Split(m.View().Content, "\n")

	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	after := strings.Split(m.View().Content, "\n")

	if len(after) != len(before) {
		t.Fatalf("the frame is %d lines with the menu, %d without", len(after), len(before))
	}
	// Compositing trims trailing spaces, so a line may come back shorter;
	// what matters is that none of them grew past the terminal.
	for i, line := range after {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("line %d is %d cells, past the terminal's %d", i, w, m.width)
		}
	}
	// The player below is untouched. Trailing spaces are trimmed off the
	// comparison: the panel is inset, so the row ends in margin, and
	// compositing drops trailing spaces that an uncomposited frame keeps.
	row := func(lines []string) string {
		return strings.TrimRight(plain(lines[m.controlsRow()]), " ")
	}
	if row(after) != row(before) {
		t.Error("the player moved")
	}
	// And the menu really is drawn.
	if !strings.Contains(plain(strings.Join(after, "\n")), "Go to album") {
		t.Error("the menu is not on the frame")
	}
}

func TestLikeFromTheMenuRatesTheTrack(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	m.trackCursor = 1 // a different row is highlighted

	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	x, y := rowAt(m, menuLike)
	next, cmd = m.Update(click(x, y))
	m = drain(t, next.(Model), cmd)

	if m.menu.open {
		t.Error("the menu stayed open")
	}
	if len(lib.rated) != 1 || lib.rated[0].videoID != "a" || lib.rated[0].rating != ytm.RatingUp {
		t.Fatalf("rated %+v, want the right-clicked track", lib.rated)
	}
	if m.Tracks[0].Rating != RatingUp {
		t.Errorf("the row is %v", m.Tracks[0].Rating)
	}
}

// openAlbum right-clicks the first track and chooses a row of the menu.
func openVia(t *testing.T, m Model, item menuItem) Model {
	t.Helper()
	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	x, y := rowAt(m, item)
	next, cmd = m.Update(click(x, y))
	return drain(t, next.(Model), cmd)
}

func TestGoToAlbumAndArtistOpenAPopover(t *testing.T) {
	for _, tc := range []struct {
		name  string
		item  menuItem
		asked string
		title string
		track string
	}{
		{"album", menuAlbum, "album:MPREbCherry", "Cherry", "Cherry Track"},
		{"artist", menuArtist, "artist:UCdaphni", "DAPHNI", "Daphni Track"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, lib, _, _ := menuModel(t)
			tabs, beneath := m.tabCount(), m.Tracks

			m = openVia(t, m, tc.item)

			if !m.detour.active {
				t.Fatal("no popover")
			}
			if m.detour.tab.Title != tc.title {
				t.Errorf("showing %q, want %q", m.detour.tab.Title, tc.title)
			}
			if len(m.detour.tracks) != 1 || m.detour.tracks[0].Title != tc.track {
				t.Fatalf("popover tracks = %+v", m.detour.tracks)
			}
			var asked bool
			for _, a := range lib.askedFor {
				asked = asked || a == tc.asked
			}
			if !asked {
				t.Errorf("asked for %v, want %q", lib.askedFor, tc.asked)
			}

			// What it covers is untouched: the tabs, and the list itself.
			if m.tabCount() != tabs {
				t.Errorf("the tab row grew from %d to %d", tabs, m.tabCount())
			}
			if len(m.Tracks) != len(beneath) || m.Tracks[0].Title != beneath[0].Title {
				t.Errorf("the list underneath changed to %+v", m.Tracks)
			}
			row := plain(strings.Split(m.View().Content, "\n")[1])
			if !strings.Contains(row, "Liked Music") {
				t.Errorf("the tab row reads %q; the tabs should still be there", row)
			}
		})
	}
}

// It floats over the frame, inset, rather than replacing it.
func TestThePopoverIsInsetAndOverlays(t *testing.T) {
	m, _, _, _ := menuModel(t)
	before := strings.Split(m.View().Content, "\n")
	m = openVia(t, m, menuAlbum)
	after := strings.Split(m.View().Content, "\n")

	if len(after) != len(before) {
		t.Fatalf("the frame is %d lines with the popover, %d without", len(after), len(before))
	}
	x, y, width, height := m.modalBounds()
	if x <= 0 || y <= 0 || x+width >= m.width || y+height >= m.height {
		t.Errorf("the popover fills the screen: %d,%d %dx%d on %dx%d",
			x, y, width, height, m.width, m.height)
	}
	if !strings.Contains(plain(strings.Join(after, "\n")), "Cherry Track") {
		t.Error("the popover's tracks are not on the frame")
	}
	// The player below it is untouched. Not the status bar: opening a
	// popover sets something loading, which is its job to say. Trailing
	// spaces are trimmed because compositing drops them and the inset
	// panel leaves margin at the end of the row.
	row := func(lines []string) string {
		return strings.TrimRight(plain(lines[m.statusRow()-1]), " ")
	}
	if row(after) != row(before) {
		t.Error("the bottom of the player moved")
	}
}

func TestEscAndClickingOutsideClosethePopover(t *testing.T) {
	m, _, _, _ := menuModel(t)

	m = openVia(t, m, menuAlbum)
	next, cmd := m.Update(keyPress("esc"))
	if drain(t, next.(Model), cmd).detour.active {
		t.Error("esc did not close it")
	}

	m = openVia(t, m, menuAlbum)
	x, y, _, _ := m.modalBounds()
	next, cmd = m.Update(click(max(x-1, 0), max(y-1, 0)))
	m = drain(t, next.(Model), cmd)
	if m.detour.active {
		t.Error("clicking outside did not close it")
	}
	// And that click was swallowed rather than also hitting the list.
	if m.trackCursor != 0 {
		t.Errorf("the dismissing click moved the list cursor to %d", m.trackCursor)
	}
}

// Going from an album to its artist replaces the popover; one esc is still
// the way out.
func TestASecondGoToReplacesThePopover(t *testing.T) {
	m, _, _, _ := menuModel(t)
	m = openVia(t, m, menuAlbum)

	// Right-click a row inside the popover and follow its artist.
	_, my, _, _ := m.modalBounds()
	inside := my + 1 + modalHeader + headerRows
	next, cmd := m.Update(rightClick(m.width/2, inside))
	m = drain(t, next.(Model), cmd)
	if !m.menu.open {
		t.Fatal("no menu inside the popover")
	}

	next, cmd = m.Update(keyPress("esc"))
	m = drain(t, next.(Model), cmd)
	if !m.detour.active {
		t.Error("closing the menu should not close the popover")
	}
	next, cmd = m.Update(keyPress("esc"))
	if drain(t, next.(Model), cmd).detour.active {
		t.Error("the second esc should close the popover")
	}
}

// The popover takes the navigation keys; the transport keys go through it.
func TestThePopoverOwnsNavigationButNotTransport(t *testing.T) {
	m, lib, _, au := menuModel(t)
	lib.tracks["MPREbCherry"] = fromUI(rows(20))
	m = openVia(t, m, menuAlbum)

	next, _ := m.Update(keyPress("j"))
	m = next.(Model)
	if m.detour.cursor != 1 {
		t.Errorf("popover cursor = %d", m.detour.cursor)
	}
	if m.trackCursor != 0 {
		t.Errorf("the list underneath moved to %d", m.trackCursor)
	}

	m.playing = m.Tracks[0]
	next, cmd := m.Update(keyPress(" "))
	drain(t, next.(Model), cmd)
	if au.toggles != 1 {
		t.Errorf("space did not reach the player: toggles = %d", au.toggles)
	}
}

// Clicking a row in the popover selects it; clicking again plays it.
func TestClickingInsidethePopover(t *testing.T) {
	m, lib, st, au := menuModel(t)
	lib.tracks["MPREbCherry"] = fromUI(rows(20))
	// Tall enough for an album to show three tracks: it is inset a row top
	// and bottom so the artist behind it shows, which costs it two.
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = sized.(Model)
	now := time.Unix(1000, 0)
	m = frozen(m, &now)
	m = openVia(t, m, menuAlbum)

	_, my, _, _ := m.modalBounds()
	row := my + 1 + modalHeader + headerRows + 2 // the third track

	next, cmd := m.Update(click(m.width/2, row))
	m = drain(t, next.(Model), cmd)
	if m.detour.cursor != 2 {
		t.Fatalf("popover cursor = %d", m.detour.cursor)
	}
	if len(au.loaded) != 0 {
		t.Error("one click started playback")
	}

	now = now.Add(100 * time.Millisecond)
	next, cmd = m.Update(click(m.width/2, row))
	m = drain(t, next.(Model), cmd)
	if len(st.resolved) == 0 || st.resolved[len(st.resolved)-1] != m.detour.tracks[2].VideoID {
		t.Fatalf("resolved %v, want the clicked row", st.resolved)
	}
}

// A track with no album page has nowhere to go, and the row says so.
func TestRowsThatLeadNowhereAreDisabled(t *testing.T) {
	m, lib, _, _ := menuModel(t)

	next, cmd := m.Update(rightClick(trackX, trackRow(1))) // the unlinked track
	m = drain(t, next.(Model), cmd)

	for _, row := range m.menuRows() {
		switch row.item {
		case menuLike:
			if !row.enabled {
				t.Error("liking should always be possible")
			}
		default:
			if row.enabled {
				t.Errorf("%v is enabled on a track that links nowhere", row.item)
			}
		}
	}
	// And they are drawn dimmed rather than looking available. Dimming is
	// the terminal's own faint, not a grey, so that it lands on any theme.
	// Line 1 is the like row, 2 the rule, 3 the album row.
	lines := strings.Split(m.renderMenu(), "\n")
	if !sgrCodes(lines[3])[faintSGR] {
		t.Errorf("a dead row is not dimmed: %v", sgrCodes(lines[3]))
	}
	if sgrCodes(lines[1])[faintSGR] {
		t.Errorf("the like row is dimmed too; nothing distinguishes them")
	}

	// Clicking one does nothing at all, menu included.
	x, y := rowAt(m, menuAlbum)
	next, cmd = m.Update(click(x, y))
	m = drain(t, next.(Model), cmd)
	if len(lib.askedFor) != 0 {
		t.Errorf("asked for %v", lib.askedFor)
	}
	if !m.menu.open {
		t.Error("a dead row closed the menu")
	}
}

func TestClickingAwayClosesTheMenu(t *testing.T) {
	m, _, _, _ := menuModel(t)
	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)

	// Somewhere well clear of the menu.
	next, cmd = m.Update(click(m.width-1, trackRow(0)))
	m = drain(t, next.(Model), cmd)

	if m.menu.open {
		t.Fatal("the menu is still open")
	}
	// And the click was swallowed rather than also selecting a row.
	if m.trackCursor != 0 {
		t.Errorf("the dismissing click also moved the cursor to %d", m.trackCursor)
	}
}

func TestTheMenuSwallowsKeysAndEscCloses(t *testing.T) {
	m, _, _, _ := menuModel(t)
	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)

	// j moves the menu, not the list.
	next, _ = m.Update(keyPress("j"))
	m = next.(Model)
	if m.menu.cursor != 1 {
		t.Errorf("menu cursor = %d", m.menu.cursor)
	}
	if m.trackCursor != 0 {
		t.Errorf("the list moved to %d underneath", m.trackCursor)
	}

	next, cmd = m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	if m.menu.open {
		t.Error("enter left the menu open")
	}

	next, cmd = m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	next, _ = m.Update(keyPress("esc"))
	if next.(Model).menu.open {
		t.Error("esc left the menu open")
	}
}

// A right-click near an edge must not put the menu off screen.
func TestTheMenuIsNudgedOnScreen(t *testing.T) {
	m, _, _, _ := menuModel(t)
	// Enough rows that the bottom of the list is a track, not blank space.
	m.Tracks = fromAPI(linked())
	for range 6 {
		m.Tracks = append(m.Tracks, m.Tracks...)
	}
	width, height := m.menuSize()

	for _, p := range []struct{ x, y int }{
		// Just inside the scrollbar, which a long list draws near the right
		// edge and which is not a track.
		{m.scrollbarColumn() - 1, trackRow(0)},
		{trackX, tabsHeight + m.bodyHeight() - 1},
		{m.scrollbarColumn() - 1, tabsHeight + m.bodyHeight() - 1},
	} {
		next, cmd := m.Update(rightClick(p.x, p.y))
		opened := drain(t, next.(Model), cmd)
		if !opened.menu.open {
			t.Fatalf("no menu at %d,%d", p.x, p.y)
		}
		if opened.menu.x < 0 || opened.menu.x+width > opened.width {
			t.Errorf("menu at x=%d runs off a %d-wide screen", opened.menu.x, opened.width)
		}
		if opened.menu.y < 0 || opened.menu.y+height > opened.height {
			t.Errorf("menu at y=%d runs off a %d-tall screen", opened.menu.y, opened.height)
		}
	}
}

// The popover must not cover the tabs or the player: the transport has to
// stay reachable while it is open.
func TestThePopoverStaysInsideTheList(t *testing.T) {
	for _, size := range []struct{ w, h int }{{100, 20}, {80, 16}, {60, 15}, {120, 40}} {
		m, _, _, _ := menuModel(t)
		sized, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = sized.(Model)
		m = openVia(t, m, menuAlbum)
		if !m.detour.active {
			t.Fatalf("%dx%d: no popover", size.w, size.h)
		}
		x, y, width, height := m.modalBounds()
		if y < tabsHeight {
			t.Errorf("%dx%d: the popover starts at row %d, over the tabs", size.w, size.h, y)
		}
		if bottom, list := y+height, tabsHeight+m.bodyHeight(); bottom > list {
			t.Errorf("%dx%d: the popover ends at row %d, past the list at %d",
				size.w, size.h, bottom, list)
		}
		if x < 0 || x+width > m.width {
			t.Errorf("%dx%d: the popover spans %d..%d", size.w, size.h, x, x+width)
		}
		// The player's bottom border is still drawn.
		lines := strings.Split(m.View().Content, "\n")
		if last := strings.TrimRight(plain(lines[m.statusRow()-1]), " "); !strings.HasSuffix(last, "╯") {
			t.Errorf("%dx%d: the player box is broken: %q", size.w, size.h, last)
		}
	}
}

// The row says what pressing it does. Liked, that is to remove the like, so
// it carries a cross rather than a filled thumb — which would read as a
// statement about the track instead of as an action.
func TestALikedTrackOffersToUnlikeWithACross(t *testing.T) {
	m, _, _, _ := menuModel(t)
	m.Tracks[0].Rating = RatingUp

	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)

	rows := m.menuRows()
	if rows[0].label != "Unlike track" {
		t.Errorf("label = %q", rows[0].label)
	}
	if rendered := plain(m.renderMenu()); !strings.Contains(rendered, "Unlike track") {
		t.Errorf("the row does not say what it does:\n%s", rendered)
	}
}

// A release opens rather than playing: there is nothing to play.
func TestEnterOnAReleaseOpensTheAlbum(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	lib.tracks["MPREbCherry"] = []ytm.Track{{VideoID: "c1", Title: "Cherry Track"}}
	m := wired(t, lib, st, au)
	m.detour = detour{
		active: true,
		tab:    Playlist{ID: "UCd", Title: "DAPHNI", kind: tabArtist},
		tracks: []Track{{Title: "Cherry", Artist: "DAPHNI", AlbumID: "MPREbCherry"}},
	}

	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)

	if len(au.loaded) != 0 || len(st.resolved) != 0 {
		t.Errorf("tried to play a release: loaded %v resolved %v", au.loaded, st.resolved)
	}
	if m.detour.tab.kind != tabAlbum || m.detour.tab.Title != "Cherry" {
		t.Fatalf("the popover is showing %+v", m.detour.tab)
	}
	if len(m.detour.tracks) != 1 || m.detour.tracks[0].Title != "Cherry Track" {
		t.Fatalf("album tracks = %+v", m.detour.tracks)
	}
}

// The icon alone leaves what you are looking at to be recognised; the word
// says it.
func TestThePopoverNamesWhatItShows(t *testing.T) {
	for _, tc := range []struct {
		item  menuItem
		label string
		title string
	}{
		{menuAlbum, "Album", "Cherry"},
		{menuArtist, "Artist", "DAPHNI"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			m, _, _, _ := menuModel(t)
			m = openVia(t, m, tc.item)

			header := plain(strings.Split(m.renderModal(), "\n")[1])
			if !strings.Contains(header, tc.label) {
				t.Errorf("%q does not name what it is", header)
			}
			if column(header, tc.label) > column(header, tc.title) {
				t.Errorf("the name comes before the label: %q", header)
			}
		})
	}
}

// The popover pages the same way the list underneath does.
func TestThePopoverOffersItsNextPage(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}
	lib.morePage = []ytm.Track{{VideoID: "z", Title: "from page two"}}

	m = openVia(t, m, menuAlbum)
	if !m.detour.more.More() {
		t.Fatal("the popover does not know there is more")
	}
	before := len(m.detour.tracks)
	if m.detourRowCount() != before+1 {
		t.Fatalf("row count = %d, want one more", m.detourRowCount())
	}

	// Walk onto the offer.
	for range before {
		next, cmd := m.Update(keyPress("j"))
		m = drain(t, next.(Model), cmd)
	}
	if len(m.detour.tracks) != before+1 {
		t.Fatalf("popover tracks = %+v", m.detour.tracks)
	}
	if m.detour.tracks[before].Title != "from page two" {
		t.Errorf("the appended row is %q", m.detour.tracks[before].Title)
	}
	// And the list underneath was not the one that grew.
	if m.more.More() {
		t.Error("the popover's page landed on the list behind it")
	}
}

// From an artist to one of their albums and back again. Going somewhere
// should be undoable.
func TestGoingBackFromAnAlbumReturnsToTheArtist(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.tracks["UCdaphni"] = []ytm.Track{
		{VideoID: "d1", Title: "Daphni Track"},
		{Title: "Cherry", AlbumID: "MPREbCherry"}, // a release on the page
	}

	m = openVia(t, m, menuArtist)
	if m.detour.tab.Title != "DAPHNI" {
		t.Fatalf("first hop landed on %q", m.detour.tab.Title)
	}
	// Move down to the release and open it.
	next, cmd := m.Update(keyPress("j"))
	m = drain(t, next.(Model), cmd)
	next, cmd = m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)

	if m.detour.tab.kind != tabAlbum || m.detour.tab.Title != "Cherry" {
		t.Fatalf("the album did not open: %+v", m.detour.tab)
	}
	if len(m.history) != 1 {
		t.Fatalf("history is %d deep, want the artist behind it", len(m.history))
	}

	next, cmd = m.Update(keyPress("esc"))
	m = drain(t, next.(Model), cmd)
	if !m.detour.active {
		t.Fatal("esc closed the popover instead of stepping back")
	}
	if m.detour.tab.Title != "DAPHNI" {
		t.Errorf("came back to %q", m.detour.tab.Title)
	}
	// And where the reader was in it.
	if m.detour.cursor != 1 {
		t.Errorf("the artist's cursor is at %d, want where it was left", m.detour.cursor)
	}

	next, cmd = m.Update(keyPress("esc"))
	if drain(t, next.(Model), cmd).detour.active {
		t.Error("the second esc should close, there being nothing behind")
	}
}

// Clicking away dismisses the lot rather than stepping back through it.
func TestClickingAwayClosesEveryPopover(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.tracks["UCdaphni"] = []ytm.Track{{Title: "Cherry", AlbumID: "MPREbCherry"}}

	m = openVia(t, m, menuArtist)
	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	if len(m.history) != 1 {
		t.Fatalf("history is %d deep", len(m.history))
	}

	// Outside the artist as well as the album. Inside the artist is a step
	// back to it now, which is what the inset is for. Its bounds come from
	// its own depth — the bottom of the stack, which takes the whole space.
	x, y, _, _ := m.modalBoundsAt(0)
	next, cmd = m.Update(click(max(x-1, 0), max(y-1, 0)))
	m = drain(t, next.(Model), cmd)

	if m.detour.active || len(m.history) != 0 {
		t.Errorf("active=%v history=%d, want everything closed", m.detour.active, len(m.history))
	}
}

// A popover has one button and it is the way out. There was a way back
// beside it until the two came to do the same thing: closing a popover steps
// back to whatever was behind it, and an album has its artist on the screen
// around it to step back to.
func TestAPopoverHasOneButton(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.tracks["UCdaphni"] = []ytm.Track{{Title: "Cherry", AlbumID: "MPREbCherry"}}

	m = openVia(t, m, menuArtist)
	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	if len(m.history) == 0 {
		t.Fatal("the album did not stack on the artist")
	}
	header := plain(strings.Split(m.renderModal(), "\n")[1])

	// One, and it is against the right.
	if got := strings.Count(header, labelClose); got != 1 {
		t.Errorf("%d buttons on %q", got, header)
	}
	// Past the box's own border and padding.
	if !strings.HasSuffix(strings.TrimRight(header, " │"), labelClose) {
		t.Errorf("the way out is not against the right: %q", header)
	}
	// It still says what it is showing.
	if !strings.Contains(header, "Album") {
		t.Errorf("the header does not name what it shows: %q", header)
	}
	// And esc is still a step back rather than a dismissal.
	next, cmd = m.Update(keyPress("esc"))
	stepped := drain(t, next.(Model), cmd)
	if !stepped.detour.active || stepped.detour.tab.Title != "DAPHNI" {
		t.Errorf("esc went to %+v, want back to the artist", stepped.detour.tab)
	}
}

// The artist stays on the screen behind the album, and clicking it is the
// way back to it.
func TestTheArtistShowsBehindTheAlbumAndTakesTheClick(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.tracks["UCdaphni"] = []ytm.Track{{Title: "Cherry", AlbumID: "MPREbCherry"}}

	m = openVia(t, m, menuArtist)
	ax, ay, awidth, _ := m.modalBounds()

	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)

	// The artist's own border is drawn where the album is not covering it.
	// Sliced by column and not by byte: the row is full of box drawing and
	// track titles, and neither is one byte a cell.
	frame := strings.Split(m.View().Content, "\n")
	row := plain(frame[ay])
	edge := row[colToByte(row, ax):colToByte(row, ax+awidth)]
	if !strings.HasPrefix(edge, "╭") || !strings.HasSuffix(edge, "╮") {
		t.Errorf("the artist is not drawn behind the album: %q", edge)
	}
	// The artist's top border has the row to itself — that is the point of
	// the vertical inset. Two columns either side and nothing else uncovers
	// only the artist's sides, which reads as a double line.
	if strings.Count(edge, "╭") != 1 {
		t.Errorf("the album is drawn on the artist's own border row: %q", edge)
	}
	// The album's border is on the next row down, inset inside the artist's
	// sides, which are still drawn either side of it.
	below := plain(frame[ay+stackInsetY])
	inner := below[colToByte(below, ax):colToByte(below, ax+awidth)]
	if !strings.HasPrefix(inner, "│") || !strings.HasSuffix(inner, "│") {
		t.Errorf("the artist's sides are not drawn beside the album: %q", inner)
	}
	if !strings.Contains(inner, "╭") || !strings.Contains(inner, "╮") {
		t.Errorf("the album is not drawn inside them: %q", inner)
	}

	// A click on the strip of artist the inset leaves showing steps back to
	// it rather than dismissing everything.
	bx, _, _, _ := m.modalBounds()
	if bx <= ax {
		t.Fatalf("the album is not inset: %d vs %d", bx, ax)
	}
	next, cmd = m.Update(click(ax+1, ay+2))
	back := drain(t, next.(Model), cmd)
	if !back.detour.active {
		t.Fatal("clicking the artist behind dismissed the lot")
	}
	if back.detour.tab.Title != "DAPHNI" {
		t.Errorf("it went to %q", back.detour.tab.Title)
	}
}

// A search starts over rather than stacking on whatever was open.
func TestSearchClearsWhatWasBehindIt(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.tracks["UCdaphni"] = []ytm.Track{{Title: "Cherry", AlbumID: "MPREbCherry"}}

	m = openVia(t, m, menuArtist)
	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)

	next, _ = m.Update(keyPress("/"))
	m = next.(Model)
	if len(m.history) != 0 {
		t.Errorf("history is %d deep behind a search", len(m.history))
	}
	next, cmd = m.Update(keyPress("esc"))
	if drain(t, next.(Model), cmd).detour.active {
		t.Error("esc from a search should close, not step back")
	}
}

// Working the transport while a popover is open should work the transport,
// not dismiss what was opened.
func TestThePlayerDoesNotDismissAPopover(t *testing.T) {
	m, lib, st, au := menuModel(t)
	lib.tracks["MPREbCherry"] = []ytm.Track{{VideoID: "c1", Title: "Cherry Track"}}
	m = openVia(t, m, menuAlbum)
	m.playing = Track{VideoID: "a", Title: "Poly"}
	m.Length = time.Minute

	// A control.
	b, ok := buttonAt(m, controlPlayPause)
	if !ok {
		t.Fatal("no play button")
	}
	next, cmd := m.Update(click(b.start+1, m.controlsRow()))
	m = drain(t, next.(Model), cmd)
	if !m.detour.active {
		t.Fatal("pressing a control closed the popover")
	}
	if au.toggles != 1 {
		t.Errorf("the control did nothing: %d toggles", au.toggles)
	}

	// The progress bar.
	start, width := m.barGeometry()
	next, cmd = m.Update(click(start+width/2, m.barRow()))
	m = drain(t, next.(Model), cmd)
	if !m.detour.active {
		t.Fatal("touching the bar closed the popover")
	}
	if !m.scrubbing {
		t.Error("the bar did not take the press")
	}
	next, cmd = m.Update(release(start+width/2, m.barRow()))
	m = drain(t, next.(Model), cmd)
	if len(au.seeks) != 1 {
		t.Errorf("seeks = %v", au.seeks)
	}
	if !m.detour.active {
		t.Error("letting go closed the popover")
	}
	_ = st

	// Clicking the list behind it still dismisses. The popover is inset, so
	// "behind it" means beside it rather than any row of the list.
	x, _, _, _ := m.modalBounds()
	if x < 1 {
		t.Fatal("the popover reaches the edge; nothing is beside it")
	}
	next, cmd = m.Update(click(x-1, trackRow(1)))
	if drain(t, next.(Model), cmd).detour.active {
		t.Error("clicking beside it did not dismiss")
	}
}

// The way back stays at the left edge; what the popover is showing sits in
// the middle of the row.
func TestTheModalTitleIsCentred(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.tracks["UCdaphni"] = []ytm.Track{{Title: "Cherry", AlbumID: "MPREbCherry"}}

	// Without a way back, centred on the whole row.
	m = openVia(t, m, menuArtist)
	header := plain(strings.Split(m.renderModal(), "\n")[1])
	if off := centreOffset(header, "Artist", "DAPHNI"); off > 1 {
		t.Errorf("off centre by %d without a button: %q", off, header)
	}

	// On an album, which draws no way back, still centred and still clear
	// of the way out on the right.
	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	header = plain(strings.Split(m.renderModal(), "\n")[1])
	if off := centreOffset(header, "Album", "Cherry"); off > 1 {
		t.Errorf("off centre by %d: %q", off, header)
	}
	if column(header, "Album") > column(header, labelClose) {
		t.Errorf("the title runs past the way out: %q", header)
	}
}

// centreOffset is how far a title's middle is from the row's, in cells. The
// title runs from the word for what it is to the end of its name.
func centreOffset(header, kind, name string) int {
	start := column(header, kind)
	end := column(header, name) + lipgloss.Width(name)
	if start < 0 || end < start {
		return 1 << 30
	}
	middle := start + (end-start)/2
	off := middle - lipgloss.Width(header)/2
	if off < 0 {
		return -off
	}
	return off
}

// The header names the kind on its own, so a nameless page must not repeat it.
func TestANamelessPageDoesNotRepeatItsKind(t *testing.T) {
	m, _, _, _ := menuModel(t)
	next, cmd := m.goTo(Playlist{ID: "MPREb", kind: tabAlbum})
	m = drain(t, next.(Model), cmd)
	header := plain(strings.Split(m.renderModal(), "\n")[1])
	if strings.Count(header, "Album") != 1 {
		t.Errorf("the kind is repeated: %q", header)
	}
}

// An album usually opens from an artist, and two popovers of the same size
// look like one that changed its mind rather than one on top of another.
func TestAnAlbumSitsInsideAnArtist(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.tracks["UCdaphni"] = []ytm.Track{{Title: "Cherry", AlbumID: "MPREbCherry"}}

	artist := openVia(t, m, menuArtist)
	ax, _, artistWidth, _ := artist.modalBounds()

	next, cmd := artist.Update(keyPress("enter"))
	album := drain(t, next.(Model), cmd)
	if album.detour.tab.kind != tabAlbum {
		t.Fatalf("the album did not open: %+v", album.detour.tab)
	}
	bx, _, albumWidth, _ := album.modalBounds()

	if want := artistWidth - 2*stackInsetX; albumWidth != want {
		t.Errorf("the album is %d wide, want %d", albumWidth, want)
	}
	// Inset on both sides, not just narrower on one.
	if want := ax + stackInsetX; bx != want {
		t.Errorf("the album starts at %d, want %d", bx, want)
	}
}

// Every popover can be shut from the popover, against the right edge where a
// window keeps it.
func TestTheCloseButtonShutsThePopover(t *testing.T) {
	m, _, _, _ := menuModel(t)
	m = openVia(t, m, menuArtist)

	x, y, width, ok := m.modalCloseButton()
	if !ok {
		t.Fatal("the popover has no way out")
	}
	mx, _, mwidth, _ := m.modalBounds()
	if want := mx + mwidth - modalChrome/2 - width; x != want {
		t.Errorf("the close button is at column %d, want %d", x, want)
	}

	// Drawn on the header, to the right of what the popover is showing.
	header := plain(strings.Split(m.renderModal(), "\n")[1])
	if at := column(header, labelClose); at < 0 {
		t.Fatalf("the close button is not drawn: %q", header)
	} else if title := column(header, "DAPHNI"); at < title {
		t.Errorf("the close button is left of the title: %q", header)
	}

	next, cmd := m.Update(click(x+1, y))
	if drain(t, next.(Model), cmd).detour.active {
		t.Error("clicking it did not shut the popover")
	}
}

// From a stacked popover it closes that one and leaves what was behind it,
// which is the whole point of the artist still being drawn there.
func TestTheCloseButtonStepsBackFromAStack(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.tracks["UCdaphni"] = []ytm.Track{{Title: "Cherry", AlbumID: "MPREbCherry"}}
	m = openVia(t, m, menuArtist)
	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	if len(m.history) == 0 {
		t.Fatal("nothing was stacked")
	}

	x, y, _, ok := m.modalCloseButton()
	if !ok {
		t.Fatal("the stacked popover has no way out")
	}
	next, cmd = m.Update(click(x+1, y))
	shut := drain(t, next.(Model), cmd)
	if !shut.detour.active {
		t.Fatal("closing the album closed the artist behind it too")
	}
	if shut.detour.tab.Title != "DAPHNI" {
		t.Errorf("it left %q open, want the artist", shut.detour.tab.Title)
	}
	if len(shut.history) != 0 {
		t.Errorf("%d popovers are still stacked behind the artist", len(shut.history))
	}

	// Esc from the album does the same thing, so the two agree.
	next, cmd = m.Update(keyPress("esc"))
	stepped := drain(t, next.(Model), cmd)
	if !stepped.detour.active || stepped.detour.tab.Title != shut.detour.tab.Title {
		t.Errorf("esc left %+v where the close button left %+v",
			stepped.detour.tab, shut.detour.tab)
	}

	// And from the artist, with nothing behind it, either one closes.
	x, y, _, ok = shut.modalCloseButton()
	if !ok {
		t.Fatal("the artist has no way out")
	}
	next, cmd = shut.Update(click(x+1, y))
	if drain(t, next.(Model), cmd).detour.active {
		t.Error("the last popover did not close")
	}
}

// A rating has to land everywhere the track is, not only where it was made.
// The popover behind the front one is on the screen — that is what the inset
// is for — and a listing already fetched is what a later visit is served.
func TestARatingReachesEveryListTheTrackIsIn(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	const shared = "shared"
	lib.tracks["LM"] = []ytm.Track{
		{VideoID: shared, Title: "Shared", Artist: "DAPHNI"},
		{VideoID: "other", Title: "Other", Artist: "B"},
	}
	lib.tracks["PL1"] = []ytm.Track{{VideoID: shared, Title: "Shared", Artist: "DAPHNI"}}
	lib.tracks["UCdaphni"] = []ytm.Track{
		{VideoID: shared, Title: "Shared", Artist: "DAPHNI"},
		{Title: "Cherry", AlbumID: "MPREbCherry"},
	}
	lib.tracks["MPREbCherry"] = []ytm.Track{{VideoID: shared, Title: "Shared", Artist: "DAPHNI"}}

	m := wired(t, lib, st, au)
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}, {ID: "PL1", Title: "Favorites"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)

	// Fetch the other tab so it is cached, then come back.
	m.tabCursor = 1
	tabbed, cmd := m.showTab()
	m = drain(t, tabbed, cmd)
	m.tabCursor = 0
	tabbed, cmd = m.showTab()
	m = drain(t, tabbed, cmd)
	if _, ok := m.cache["PL1"]; !ok {
		t.Fatal("the other playlist was not cached")
	}

	// An artist, then an album stacked on it.
	next, cmd := m.goTo(Playlist{ID: "UCdaphni", Title: "DAPHNI", kind: tabArtist})
	m = drain(t, next.(Model), cmd)
	next, cmd = m.goTo(Playlist{ID: "MPREbCherry", Title: "Cherry", kind: tabAlbum})
	m = drain(t, next.(Model), cmd)
	if len(m.history) != 1 {
		t.Fatalf("history is %d deep", len(m.history))
	}

	// Rate it from the album at the front.
	next, cmd = m.rateTrack(m.detour.tracks[0], RatingUp)
	m = drain(t, next.(Model), cmd)

	rated := func(what string, rows []Track) {
		for _, tr := range rows {
			if tr.VideoID == shared && tr.Rating != RatingUp {
				t.Errorf("%s still has it unrated", what)
			}
		}
	}
	rated("the album in front", m.detour.tracks)
	rated("the artist behind it", m.history[0].tracks)
	rated("the artist's arrival order", m.history[0].arrival)
	rated("the list underneath", m.Tracks)
	rated("its arrival order", m.arrival)
	rated("the other cached playlist", m.cache["PL1"].tracks)
}

// The search box is something being typed into and the rest of the popover
// is the answer; a line is what says where one stops. It is there whether or
// not there is an answer yet.
func TestTheSearchBoxIsRuledOffFromItsResults(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.results = []ytm.Track{{VideoID: "z", Title: "Found", Artist: "Z"}}

	next, cmd := m.openSearch()
	m = drain(t, next.(Model), cmd)
	inner := m.modalContentWidth()

	rule := func(m Model, when string) {
		line := plain(strings.Split(m.renderModal(), "\n")[2])
		// Past the box's own border and padding either side.
		body := strings.Trim(line, "│ ")
		if body != strings.Repeat("─", inner) {
			t.Errorf("%s: the rule is %q", when, body)
		}
	}
	rule(m, "empty")

	for _, key := range []string{"f", "o", "u", "n", "d"} {
		next, cmd := m.Update(keyPress(key))
		m = drain(t, next.(Model), cmd)
	}
	rule(m, "typing")

	next, cmd = m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	if len(m.detour.tracks) == 0 {
		t.Fatal("nothing was found, so this proves nothing")
	}
	rule(m, "with results")

	// And the other popovers keep the blank line there, which is not a rule.
	artist := openVia(t, m, menuArtist)
	line := plain(strings.Split(artist.renderModal(), "\n")[2])
	if strings.Contains(line, "─") {
		t.Errorf("an artist popover is ruled off too: %q", line)
	}
}

// A search can sit at the bottom of a stack: go to an album or an artist
// from a result and the search is still there to come back to, query and
// results intact.
func TestASearchKeepsItsPlaceUnderAnAlbum(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.results = []ytm.Track{{VideoID: "z", Title: "Found", Artist: "Z",
		AlbumID: "MPREbCherry", ArtistID: "UCdaphni"}}
	lib.tracks["MPREbCherry"] = []ytm.Track{{VideoID: "c1", Title: "Cherry Track"}}

	next, cmd := m.openSearch()
	m = drain(t, next.(Model), cmd)
	for _, k := range []string{"f", "o", "u", "n", "d"} {
		n, c := m.Update(keyPress(k))
		m = drain(t, n.(Model), c)
	}
	n, c := m.Update(keyPress("enter"))
	m = drain(t, n.(Model), c)
	if len(m.detour.tracks) == 0 {
		t.Fatal("the search found nothing, so this proves nothing")
	}

	// The menu is reachable from a result, and it offers somewhere to go.
	x, y, _, _ := m.modalBounds()
	n2, c2 := m.Update(rightClick(x+4, y+1+modalHeader))
	m = drain(t, n2.(Model), c2)
	if !m.menu.open {
		t.Fatal("no menu on a search result")
	}
	at := -1
	for i, row := range m.menuRows() {
		if row.item == menuAlbum {
			if !row.enabled {
				t.Fatal("go to album is offered but not enabled")
			}
			at = i
		}
	}
	n3, c3 := m.activate(at)
	m = drain(t, n3.(Model), c3)

	if m.detour.tab.kind != tabAlbum {
		t.Fatalf("it went to %v", m.detour.tab.kind)
	}
	if len(m.history) != 1 || m.history[0].tab.kind != tabSearch {
		t.Fatalf("the search is not behind it: %+v", m.history)
	}

	// The way out of the album lands back on the search, as it was.
	cx, cy, _, ok := m.modalCloseButton()
	if !ok {
		t.Fatal("the album has no way out")
	}
	n4, c4 := m.Update(click(cx+1, cy))
	back := drain(t, n4.(Model), c4)
	if back.detour.tab.kind != tabSearch {
		t.Fatalf("closing the album landed on %v", back.detour.tab.kind)
	}
	if back.detour.query != "found" {
		t.Errorf("the query came back as %q", back.detour.query)
	}
	if len(back.detour.tracks) == 0 {
		t.Error("the results did not come back")
	}

	// And the search's own way out works, which it did not when the button
	// was drawn on it but nothing answered for it.
	sx, sy, _, ok := back.modalCloseButton()
	if !ok {
		t.Fatal("the search has no way out")
	}
	n5, c5 := back.Update(click(sx+1, sy))
	shut := drain(t, n5.(Model), c5)
	if shut.detour.active {
		t.Error("the search would not close")
	}
}

// Three deep: a search with an artist on it and an album on that. Each one
// steps in from the one below, so all three are on the screen at once.
func TestAThreeDeepStackShowsEveryLevel(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.results = []ytm.Track{{VideoID: "z", Title: "Found", Artist: "Z",
		ArtistID: "UCdaphni"}}
	lib.tracks["UCdaphni"] = []ytm.Track{{Title: "Cherry", AlbumID: "MPREbCherry"}}
	lib.tracks["MPREbCherry"] = []ytm.Track{{VideoID: "c1", Title: "Cherry Track"}}

	next, cmd := m.openSearch()
	m = drain(t, next.(Model), cmd)
	for _, k := range []string{"f", "o", "u", "n", "d"} {
		n, c := m.Update(keyPress(k))
		m = drain(t, n.(Model), c)
	}
	n, c := m.Update(keyPress("enter"))
	m = drain(t, n.(Model), c)

	n, c = m.goTo(Playlist{ID: "UCdaphni", Title: "DAPHNI", kind: tabArtist})
	m = drain(t, n.(Model), c)
	n, c = m.goTo(Playlist{ID: "MPREbCherry", Title: "Cherry", kind: tabAlbum})
	m = drain(t, n.(Model), c)

	if len(m.history) != 2 {
		t.Fatalf("the stack is %d deep", len(m.history))
	}
	if m.history[0].tab.kind != tabSearch || m.history[1].tab.kind != tabArtist {
		t.Fatalf("the stack is %v then %v", m.history[0].tab.kind, m.history[1].tab.kind)
	}

	// Each level steps in from the one below it, by the same amount.
	var last struct{ x, width int }
	for depth := range 3 {
		x, _, width, _ := m.modalBoundsAt(depth)
		if depth > 0 {
			if x != last.x+stackInsetX {
				t.Errorf("level %d starts at %d, want %d", depth, x, last.x+stackInsetX)
			}
			if width != last.width-2*stackInsetX {
				t.Errorf("level %d is %d wide, want %d", depth, width, last.width-2*stackInsetX)
			}
		}
		last.x, last.width = x, width
	}

	// And all three are drawn: the search's own border shows outside the
	// artist's, which shows outside the album's.
	frame := strings.Split(m.View().Content, "\n")
	for depth := range 3 {
		x, y, width, _ := m.modalBoundsAt(depth)
		row := plain(frame[y])
		edge := row[colToByte(row, x):colToByte(row, x+width)]
		if !strings.HasPrefix(edge, "╭") || !strings.HasSuffix(edge, "╮") {
			t.Errorf("level %d is not drawn on its own top row: %q", depth, edge)
		}
	}

	// Stepping back goes search-ward one level at a time.
	for _, want := range []tabKind{tabArtist, tabSearch} {
		n, c := m.leaveDetour()
		m = drain(t, n, c)
		if m.detour.tab.kind != want {
			t.Fatalf("stepped back to %v, want %v", m.detour.tab.kind, want)
		}
	}
	if m.detour.query != "found" {
		t.Errorf("the search came back as %q", m.detour.query)
	}
}

// Down goes from the box into the results and up comes back out of them,
// which means nothing has to be chosen for the reader when they arrive.
func TestDownAndUpWalkBetweenTheSearchBoxAndItsResults(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.results = []ytm.Track{
		{VideoID: "a", Title: "First", Artist: "A"},
		{VideoID: "b", Title: "Second", Artist: "B"},
	}

	next, cmd := m.openSearch()
	m = drain(t, next.(Model), cmd)
	if !m.detour.typing || m.detour.cursor != noRow {
		t.Fatalf("a fresh search is typing=%v cursor=%d", m.detour.typing, m.detour.cursor)
	}
	for _, k := range []string{"f", "i", "r", "s", "t"} {
		n, c := m.Update(keyPress(k))
		m = drain(t, n.(Model), c)
	}
	n, c := m.Update(keyPress("enter"))
	m = drain(t, n.(Model), c)
	if len(m.detour.tracks) != 2 {
		t.Fatalf("the search found %d", len(m.detour.tracks))
	}
	// Nothing chosen, and nothing highlighted with it.
	if m.detour.cursor != noRow {
		t.Fatalf("a result was chosen: %d", m.detour.cursor)
	}
	if sgrCodes(m.renderModal())[highlightSGR] {
		t.Error("a row is filled with nothing chosen")
	}

	// Down goes in, at the first result.
	n, c = m.Update(keyPress("down"))
	m = drain(t, n.(Model), c)
	if m.detour.typing {
		t.Error("down left the keys in the box")
	}
	if m.detour.cursor != 0 {
		t.Fatalf("down landed on %d, want the first result", m.detour.cursor)
	}

	// And on down the list from there.
	n, c = m.Update(keyPress("down"))
	m = drain(t, n.(Model), c)
	if m.detour.cursor != 1 {
		t.Fatalf("the second down landed on %d", m.detour.cursor)
	}

	// Up comes back through the list.
	n, c = m.Update(keyPress("up"))
	m = drain(t, n.(Model), c)
	if m.detour.cursor != 0 || m.detour.typing {
		t.Fatalf("up left cursor=%d typing=%v", m.detour.cursor, m.detour.typing)
	}

	// And up off the top is the box again, with the query still in it.
	n, c = m.Update(keyPress("up"))
	m = drain(t, n.(Model), c)
	if !m.detour.typing {
		t.Error("up off the top did not go back to the box")
	}
	if m.detour.cursor != noRow {
		t.Errorf("it left %d chosen", m.detour.cursor)
	}
	if m.detour.query != "first" {
		t.Errorf("the query came back as %q", m.detour.query)
	}
	// Typing works again from there.
	n, c = m.Update(keyPress("x"))
	m = drain(t, n.(Model), c)
	if m.detour.query != "firstx" {
		t.Errorf("typing after coming back gave %q", m.detour.query)
	}
}

// Nothing typed yet and nothing found are different things to say.
func TestTheSearchNoteTellsUntypedFromUnfound(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.results = nil

	next, cmd := m.openSearch()
	m = drain(t, next.(Model), cmd)
	// Nothing is in flight, so the popover is not waiting on anything.
	m.loading = false
	if got := plain(m.renderModal()); !strings.Contains(got, labelTypeToSearch) {
		t.Errorf("a fresh search does not invite one: %q", got)
	}

	// Mid-word there is still nothing to have found.
	for _, k := range []string{"z", "z"} {
		n, c := m.Update(keyPress(k))
		m = drain(t, n.(Model), c)
	}
	m.loading = false
	if got := plain(m.renderModal()); strings.Contains(got, "Nothing found") {
		t.Errorf("it gave up before the search was run: %q", got)
	}

	n, c := m.Update(keyPress("enter"))
	m = drain(t, n.(Model), c)
	m.loading = false
	if got := plain(m.renderModal()); !strings.Contains(got, "Nothing found") {
		t.Errorf("a fruitless search does not say so: %q", got)
	}
}
