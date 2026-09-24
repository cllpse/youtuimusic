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
	for _, want := range []string{"Like track", "Go to album", "Go to artist"} {
		if !strings.Contains(got, want) {
			t.Errorf("menu is missing %q:\n%s", want, got)
		}
	}
	for _, icon := range []string{iconThumbUpOff, iconAlbum, iconArtist} {
		if !strings.Contains(m.renderMenu(), icon) {
			t.Errorf("menu is missing icon %q", icon)
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
	// The player box below is untouched.
	if plain(after[m.barRow()+2]) != plain(before[m.barRow()+2]) {
		t.Error("the bottom of the player moved")
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
	// The player box below it is untouched. Not the status bar: opening a
	// popover sets something loading, which is its job to say.
	if plain(after[m.statusRow()-1]) != plain(before[m.statusRow()-1]) {
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
	// And they are drawn greyed rather than looking available. Line 1 is
	// the like row, 2 the rule, 3 the album row.
	lines := strings.Split(m.renderMenu(), "\n")
	if !sgrCodes(lines[3])["90"] {
		t.Errorf("a dead row is not greyed: %v", sgrCodes(lines[3]))
	}
	if sgrCodes(lines[1])["90"] {
		t.Errorf("the like row is greyed too; nothing distinguishes them")
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
		if last := plain(lines[m.statusRow()-1]); !strings.HasSuffix(last, "╯") {
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
	if rows[0].icon != iconRemove {
		t.Errorf("icon = %q, want the cross", rows[0].icon)
	}
	rendered := m.renderMenu()
	if strings.Contains(rendered, iconThumbUp) {
		t.Error("the filled thumb is still drawn")
	}
	if !strings.Contains(rendered, iconRemove) {
		t.Error("the cross is not drawn")
	}
}

// One space reads as cramped against these glyphs, which sit tight in their
// cell.
func TestTheMenuSeparatesIconFromLabel(t *testing.T) {
	m, _, _, _ := menuModel(t)
	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)

	rendered := plain(m.renderMenu())
	for _, row := range m.menuRows() {
		if !strings.Contains(rendered, row.icon+menuGap+row.label) {
			t.Errorf("%q is not separated from its icon:\n%s", row.label, rendered)
		}
	}
	// And the box is wide enough for it, with the rule spanning the inside.
	width, _ := m.menuSize()
	for i, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) != width {
			t.Errorf("menu line %d is %d cells, want %d: %q", i, lipgloss.Width(line), width, line)
		}
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
		icon  string
		label string
		title string
	}{
		{menuAlbum, iconAlbum, "Album", "Cherry"},
		{menuArtist, iconArtist, "Artist", "DAPHNI"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			m, _, _, _ := menuModel(t)
			m = openVia(t, m, tc.item)

			header := plain(strings.Split(m.renderModal(), "\n")[1])
			if !strings.Contains(header, tc.icon) {
				t.Errorf("no icon on %q", header)
			}
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

	x, y, _, _ := m.modalBounds()
	next, cmd = m.Update(click(max(x-1, 0), max(y-1, 0)))
	m = drain(t, next.(Model), cmd)

	if m.detour.active || len(m.history) != 0 {
		t.Errorf("active=%v history=%d, want everything closed", m.detour.active, len(m.history))
	}
}

// A popover with something behind it says so.
func TestAPopoverWithHistoryShowsTheWayBack(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.tracks["UCdaphni"] = []ytm.Track{{Title: "Cherry", AlbumID: "MPREbCherry"}}

	m = openVia(t, m, menuArtist)
	if header := plain(strings.Split(m.renderModal(), "\n")[1]); strings.Contains(header, iconBack) {
		t.Errorf("the first popover offers a way back: %q", header)
	}

	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	header := plain(strings.Split(m.renderModal(), "\n")[1])
	if !strings.Contains(header, iconBack) {
		t.Errorf("no way back shown on %q", header)
	}
	if !strings.Contains(header, "Album") {
		t.Errorf("the header stopped saying what it shows: %q", header)
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
