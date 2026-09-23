package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cllpse/youtuimusic/internal/ytm"
)

func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func release(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func motion(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func wheel(x, y int, button tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg(tea.Mouse{X: x, Y: y, Button: button})
}

// frozen pins the clock so click timing is not a race.
func frozen(m Model, at *time.Time) Model {
	m.now = func() time.Time { return *at }
	return m
}

// trackX is a column inside the track table.
const trackX = sidebarWidth + 3

func TestClickOpensAPlaylist(t *testing.T) {
	lib := library()
	lib.tracks["PL1"] = []ytm.Track{{VideoID: "c", Title: "Gamma"}}
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}, {ID: "PL1", Title: "Favorites"}}
	m.focus = PaneTracks

	next, cmd := m.Update(click(2, 1))
	m = drain(t, next.(Model), cmd)

	if m.sidebarCursor != 1 {
		t.Errorf("sidebar cursor = %d, want the clicked row", m.sidebarCursor)
	}
	if m.focus != PaneSidebar {
		t.Error("clicking the sidebar should focus it")
	}
	if len(m.Tracks) != 1 || m.Tracks[0].Title != "Gamma" {
		t.Fatalf("tracks = %+v", m.Tracks)
	}
}

// One click selects. Starting a track by pointing at it would be too easy
// to do by accident.
func TestOneClickSelectsATrackWithoutPlaying(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(click(trackX, 1))
	m = drain(t, next.(Model), cmd)

	if m.trackCursor != 1 || m.focus != PaneTracks {
		t.Errorf("cursor = %d, focus = %v", m.trackCursor, m.focus)
	}
	if len(au.loaded) != 0 {
		t.Fatalf("a single click started playback: %v", au.loaded)
	}
	// It is warmed instead, so the second click plays without waiting.
	if len(st.prefetched) != 1 || st.prefetched[0] != "b" {
		t.Errorf("prefetched = %v", st.prefetched)
	}
}

func TestSecondClickPlays(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	now := time.Unix(1000, 0)
	m := frozen(wired(t, lib, st, au), &now)
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(click(trackX, 0))
	m = drain(t, next.(Model), cmd)
	now = now.Add(150 * time.Millisecond)
	next, cmd = m.Update(click(trackX, 0))
	m = drain(t, next.(Model), cmd)

	if len(au.loaded) != 1 || au.loaded[0] != "https://stream/a" {
		t.Fatalf("loaded = %v", au.loaded)
	}
	if m.NowPlaying != "A — Alpha" {
		t.Errorf("now playing = %q", m.NowPlaying)
	}
}

func TestTwoSlowClicksDoNotPlay(t *testing.T) {
	lib, au := library(), newFakeAudio()
	now := time.Unix(1000, 0)
	m := frozen(wired(t, lib, &fakeStreams{}, au), &now)
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(click(trackX, 0))
	m = drain(t, next.(Model), cmd)
	now = now.Add(2 * time.Second)
	next, cmd = m.Update(click(trackX, 0))
	drain(t, next.(Model), cmd)

	if len(au.loaded) != 0 {
		t.Fatalf("slow clicks played %v", au.loaded)
	}
}

// Two clicks on different rows are two selections, not a double click.
func TestTwoClicksOnDifferentRowsDoNotPlay(t *testing.T) {
	lib, au := library(), newFakeAudio()
	now := time.Unix(1000, 0)
	m := frozen(wired(t, lib, &fakeStreams{}, au), &now)
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(click(trackX, 0))
	m = drain(t, next.(Model), cmd)
	now = now.Add(50 * time.Millisecond)
	next, cmd = m.Update(click(trackX, 1))
	drain(t, next.(Model), cmd)

	if len(au.loaded) != 0 {
		t.Fatalf("played %v", au.loaded)
	}
}

func TestClickBelowTheLastRowDoesNothing(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := wired(t, lib, st, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	before := m

	next, cmd := m.Update(click(trackX, 10))
	m = drain(t, next.(Model), cmd)
	if m.trackCursor != before.trackCursor {
		t.Errorf("cursor moved to %d on an empty row", m.trackCursor)
	}
	next, cmd = m.Update(click(2, 8))
	m = drain(t, next.(Model), cmd)
	if m.sidebarCursor != before.sidebarCursor {
		t.Errorf("sidebar cursor moved to %d on an empty row", m.sidebarCursor)
	}
	if len(st.prefetched) != 0 {
		t.Errorf("prefetched %v from empty rows", st.prefetched)
	}
}

func TestClickOnTheBarSeeks(t *testing.T) {
	au := newFakeAudio()
	m := wired(t, library(), &fakeStreams{}, au)
	m.Length = 200 * time.Second
	start, width := m.barGeometry()

	next, cmd := m.Update(click(start+width/2, m.barRow()))
	m = drain(t, next.(Model), cmd)
	if !m.scrubbing {
		t.Error("pressing on the bar should begin a scrub")
	}
	// The bar moves under the pointer straight away; the seek waits for the
	// button to come up.
	if m.Position < 95*time.Second || m.Position > 105*time.Second {
		t.Errorf("position = %v, want about half of 200s", m.Position)
	}
	if len(au.seeks) != 0 {
		t.Fatalf("seeked before the button came up: %v", au.seeks)
	}

	next, cmd = m.Update(release(start+width/2, m.barRow()))
	m = drain(t, next.(Model), cmd)
	if m.scrubbing {
		t.Error("still scrubbing after release")
	}
	if len(au.seeks) != 1 || au.seeks[0] < 95 || au.seeks[0] > 105 {
		t.Fatalf("seeks = %v", au.seeks)
	}
}

// Dragging follows the pointer but only commits once, because seeking on
// every motion event makes mpv stutter.
func TestDraggingSeeksOnceAtTheEnd(t *testing.T) {
	au := newFakeAudio()
	m := wired(t, library(), &fakeStreams{}, au)
	m.Length = 100 * time.Second
	start, width := m.barGeometry()

	next, cmd := m.Update(click(start, m.barRow()))
	m = drain(t, next.(Model), cmd)
	for _, frac := range []float64{0.25, 0.5, 0.75} {
		next, cmd = m.Update(motion(start+int(float64(width)*frac), m.barRow()))
		m = drain(t, next.(Model), cmd)
	}
	if len(au.seeks) != 0 {
		t.Fatalf("seeked mid-drag: %v", au.seeks)
	}
	if m.Position < 70*time.Second {
		t.Errorf("the bar did not follow the drag: %v", m.Position)
	}

	next, cmd = m.Update(release(start+width, m.barRow()))
	m = drain(t, next.(Model), cmd)
	if len(au.seeks) != 1 {
		t.Fatalf("seeks = %v, want exactly one", au.seeks)
	}
	if au.seeks[0] < 99 {
		t.Errorf("seeked to %v, want the end", au.seeks[0])
	}
}

// Motion with no drag in progress is just the pointer moving.
func TestMotionWithoutADragIsIgnored(t *testing.T) {
	au := newFakeAudio()
	m := wired(t, library(), &fakeStreams{}, au)
	m.Length = 100 * time.Second
	m.Position = 10 * time.Second
	start, _ := m.barGeometry()

	next, cmd := m.Update(motion(start+20, m.barRow()))
	m = drain(t, next.(Model), cmd)

	if m.Position != 10*time.Second {
		t.Errorf("position moved to %v without a button down", m.Position)
	}
	if len(au.seeks) != 0 {
		t.Errorf("seeks = %v", au.seeks)
	}
}

func TestClickingTheBarWithNothingLoadedDoesNothing(t *testing.T) {
	au := newFakeAudio()
	m := wired(t, library(), &fakeStreams{}, au)
	start, _ := m.barGeometry()

	next, cmd := m.Update(click(start+5, m.barRow()))
	m = drain(t, next.(Model), cmd)

	if m.scrubbing {
		t.Error("scrubbing a bar with no track")
	}
	if len(au.seeks) != 0 {
		t.Errorf("seeks = %v", au.seeks)
	}
}

func TestWheelScrollsTheListUnderThePointer(t *testing.T) {
	lib := library()
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM"}, {ID: "PL1"}}
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, _ := m.Update(wheel(trackX, 0, tea.MouseWheelDown))
	m = next.(Model)
	if m.trackCursor != 1 {
		t.Errorf("track cursor = %d", m.trackCursor)
	}
	if m.sidebarCursor != 0 {
		t.Errorf("the sidebar moved too: %d", m.sidebarCursor)
	}

	next, _ = m.Update(wheel(2, 0, tea.MouseWheelDown))
	m = next.(Model)
	if m.sidebarCursor != 1 || m.trackCursor != 1 {
		t.Errorf("sidebar = %d, tracks = %d", m.sidebarCursor, m.trackCursor)
	}

	next, _ = m.Update(wheel(2, 0, tea.MouseWheelUp))
	if next.(Model).sidebarCursor != 0 {
		t.Errorf("scrolling up did not come back")
	}
}

// Scrolling is not a click: the keyboard stays where it was put.
func TestWheelDoesNotStealFocus(t *testing.T) {
	lib := library()
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.focus = PaneSidebar

	next, _ := m.Update(wheel(trackX, 0, tea.MouseWheelDown))
	if next.(Model).focus != PaneSidebar {
		t.Error("the wheel moved focus")
	}
}

// The whole point of hit-testing is that it agrees with what is drawn, so
// check it against the rendered frame rather than against itself.
func TestHitTestingMatchesTheRenderedFrame(t *testing.T) {
	lib := library()
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Playlists = []Playlist{{Title: "Liked Music"}, {Title: "Favorites"}}
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.Length = time.Minute

	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != 20 {
		t.Fatalf("rendered %d lines, want the terminal's 20", len(lines))
	}

	for row, want := range map[int]string{0: "Liked Music", 1: "Favorites"} {
		if !strings.Contains(lines[row], want) {
			t.Fatalf("row %d is %q, not %q", row, lines[row], want)
		}
		if where, n := m.hit(2, row); where != regionSidebar || n != row {
			t.Errorf("hit on the sidebar row %d = %v, %d", row, where, n)
		}
	}
	for row, want := range map[int]string{0: "Alpha", 1: "Beta"} {
		if !strings.Contains(lines[row], want) {
			t.Fatalf("row %d is %q, not %q", row, lines[row], want)
		}
		if where, n := m.hit(trackX, row); where != regionTracks || n != row {
			t.Errorf("hit on the track row %d = %v, %d", row, where, n)
		}
	}

	// The bar row is the one with the bar drawn on it.
	if !strings.ContainsAny(lines[m.barRow()], "━─") {
		t.Fatalf("row %d is %q, which has no bar on it", m.barRow(), lines[m.barRow()])
	}
	for row, line := range lines {
		if row != m.barRow() && strings.ContainsAny(line, "━─") {
			t.Errorf("row %d also looks like a bar: %q", row, line)
		}
	}

	// And the ends of the bar map to the ends of the track.
	start, width := m.barGeometry()
	if got := m.positionAt(start); got != 0 {
		t.Errorf("the left end is %v, want the beginning", got)
	}
	if got := m.positionAt(start + width); got != m.Length {
		t.Errorf("the right end is %v, want %v", got, m.Length)
	}
	if got := m.positionAt(start + width/2); got < 29*time.Second || got > 31*time.Second {
		t.Errorf("the middle is %v, want about 30s", got)
	}
}
