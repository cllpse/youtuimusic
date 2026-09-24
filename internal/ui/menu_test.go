package ui

import (
	"strings"
	"testing"

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
			return m.menu.x + 2, m.menu.y + 1 + i
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

func TestGoToAlbumAndArtistTakeOverTheView(t *testing.T) {
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
			tabs := m.tabCount()

			next, cmd := m.Update(rightClick(trackX, trackRow(0)))
			m = drain(t, next.(Model), cmd)
			x, y := rowAt(m, tc.item)
			next, cmd = m.Update(click(x, y))
			m = drain(t, next.(Model), cmd)

			if !m.detour.active {
				t.Fatal("the view did not change")
			}
			if m.detour.tab.Title != tc.title {
				t.Errorf("showing %q, want %q", m.detour.tab.Title, tc.title)
			}
			if m.tabCount() != tabs {
				t.Errorf("the tab row grew from %d to %d", tabs, m.tabCount())
			}
			var asked bool
			for _, a := range lib.askedFor {
				asked = asked || a == tc.asked
			}
			if !asked {
				t.Errorf("asked for %v, want %q", lib.askedFor, tc.asked)
			}
			if len(m.Tracks) != 1 || m.Tracks[0].Title != tc.track {
				t.Fatalf("tracks = %+v", m.Tracks)
			}
			// The tab row says where you are and how to get back.
			row := plain(strings.Split(m.View().Content, "\n")[1])
			if !strings.Contains(row, tc.title) || !strings.Contains(row, iconBack) {
				t.Errorf("the tab row reads %q", row)
			}
			if strings.Contains(row, "Liked Music") {
				t.Error("the playlist tabs are still on screen")
			}
		})
	}
}

// Leaving puts back the tab and the place in it the detour interrupted.
func TestLeavingADetourPutsBackWhatWasThere(t *testing.T) {
	m, lib, _, _ := menuModel(t)
	lib.tracks["LM"] = fromUI(rows(40))
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)

	m.trackCursor = 22
	m.scroll()
	// The list is scrolled, so the top row on screen is not the first track.
	m.Tracks[m.trackOffset] = fromAPI(linked())[0]

	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	// The right-click selected that row, and that is the place to come back
	// to — not wherever the cursor was before it.
	wasCursor, wasOffset := m.trackCursor, m.trackOffset
	x, y := rowAt(m, menuAlbum)
	next, cmd = m.Update(click(x, y))
	m = drain(t, next.(Model), cmd)
	if !m.detour.active {
		t.Fatal("no detour")
	}

	next, cmd = m.Update(keyPress("esc"))
	m = drain(t, next.(Model), cmd)

	if m.detour.active {
		t.Fatal("esc did not leave")
	}
	if got, _ := m.SelectedPlaylist(); got.Title != "Liked Music" {
		t.Errorf("came back to %q", got.Title)
	}
	if m.trackCursor != wasCursor || m.trackOffset != wasOffset {
		t.Errorf("came back to cursor %d offset %d, want %d and %d",
			m.trackCursor, m.trackOffset, wasCursor, wasOffset)
	}
}

// Album then artist is one detour, and leaving returns to where it started
// rather than stepping back through it.
func TestADetourWithinADetourStillReturnsHome(t *testing.T) {
	m, _, _, _ := menuModel(t)
	m.tabCursor = 0

	for _, item := range []menuItem{menuAlbum, menuArtist} {
		m.Tracks = fromAPI(linked())
		next, cmd := m.Update(rightClick(trackX, trackRow(0)))
		m = drain(t, next.(Model), cmd)
		x, y := rowAt(m, item)
		next, cmd = m.Update(click(x, y))
		m = drain(t, next.(Model), cmd)
	}
	if m.detour.tab.Title != "DAPHNI" {
		t.Fatalf("second hop landed on %q", m.detour.tab.Title)
	}

	next, cmd := m.Update(keyPress("esc"))
	m = drain(t, next.(Model), cmd)
	if m.detour.active {
		t.Error("one esc should leave altogether, not step back a hop")
	}
}

// Clicking the tab row is the other way out.
func TestClickingTheDetourTabLeaves(t *testing.T) {
	m, _, _, _ := menuModel(t)
	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	x, y := rowAt(m, menuAlbum)
	next, cmd = m.Update(click(x, y))
	m = drain(t, next.(Model), cmd)

	next, cmd = m.Update(click(2, 1))
	m = drain(t, next.(Model), cmd)
	if m.detour.active {
		t.Error("clicking the tab row did not leave")
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
	// And they are drawn greyed rather than looking available.
	lines := strings.Split(m.renderMenu(), "\n")
	if !sgrCodes(lines[2])["90"] { // past the top border: the album row
		t.Errorf("a dead row is not greyed: %v", sgrCodes(lines[2]))
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
		// Just inside the scrollbar, which a long list puts in the last
		// column and which is not a track.
		{m.width - 2, trackRow(0)},
		{trackX, tabsHeight + m.bodyHeight() - 1},
		{m.width - 2, tabsHeight + m.bodyHeight() - 1},
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
