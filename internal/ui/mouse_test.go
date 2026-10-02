package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cllpse/youtuimusic/internal/player"
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

// trackRow is the screen row of the nth track, which sits under the tabs.
func trackRow(n int) int { return tabsHeight + headerRows + n }

// trackX is a column inside the track table.
const trackX = 10

func TestClickOpensATab(t *testing.T) {
	lib := library()
	lib.tracks["PL1"] = []ytm.Track{{VideoID: "c", Title: "Gamma"}}
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}, {ID: "PL1", Title: "Favorites"}}

	// Click somewhere inside the second tab, wherever the layout put it.
	spans := m.tabSpans()
	if len(spans) < 2 {
		t.Fatalf("only %d tabs fit", len(spans))
	}
	next, cmd := m.Update(click(spans[1].start+1, 1))
	m = drain(t, next.(Model), cmd)

	if m.tabCursor != 1 {
		t.Errorf("tab cursor = %d, want the clicked tab", m.tabCursor)
	}
	if len(m.Tracks) != 1 || m.Tracks[0].Title != "Gamma" {
		t.Fatalf("tracks = %+v", m.Tracks)
	}
}

// The gap past the last tab is not a tab.
func TestClickPastTheTabsDoesNothing(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	next, cmd := m.Update(click(m.width-1, 1))
	m = drain(t, next.(Model), cmd)
	if m.tabCursor != 0 {
		t.Errorf("tab cursor = %d", m.tabCursor)
	}
}

// One click selects. Starting a track by pointing at it would be too easy
// to do by accident.
func TestOneClickSelectsATrackWithoutPlaying(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(click(trackX, trackRow(1)))
	m = drain(t, next.(Model), cmd)

	if m.trackCursor != 1 {
		t.Errorf("cursor = %d, want the clicked row", m.trackCursor)
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

	next, cmd := m.Update(click(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	now = now.Add(150 * time.Millisecond)
	next, cmd = m.Update(click(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)

	if len(au.loaded) != 1 || au.loaded[0] != "https://stream/a" {
		t.Fatalf("loaded = %v", au.loaded)
	}
	if m.playing.Title != "Alpha" {
		t.Errorf("now playing = %q", m.playing.Title)
	}
}

func TestTwoSlowClicksDoNotPlay(t *testing.T) {
	lib, au := library(), newFakeAudio()
	now := time.Unix(1000, 0)
	m := frozen(wired(t, lib, &fakeStreams{}, au), &now)
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(click(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	now = now.Add(2 * time.Second)
	next, cmd = m.Update(click(trackX, trackRow(0)))
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

	next, cmd := m.Update(click(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	now = now.Add(50 * time.Millisecond)
	next, cmd = m.Update(click(trackX, trackRow(1)))
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

	next, cmd := m.Update(click(trackX, trackRow(10)))
	m = drain(t, next.(Model), cmd)
	if m.trackCursor != before.trackCursor {
		t.Errorf("cursor moved to %d on an empty row", m.trackCursor)
	}
	if len(st.prefetched) != 0 {
		t.Errorf("prefetched %v from an empty row", st.prefetched)
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

// The wheel moves the view and leaves the selection alone, so looking
// further down a playlist does not lose your place in it.
func TestWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := wired(t, lib, st, newFakeAudio())
	m.Tracks = rows(100)
	m.trackCursor = 4

	next, cmd := m.Update(wheel(trackX, trackRow(0), tea.MouseWheelDown))
	m = drain(t, next.(Model), cmd)

	if m.trackOffset != wheelStep {
		t.Errorf("offset = %d, want %d", m.trackOffset, wheelStep)
	}
	if m.trackCursor != 4 {
		t.Errorf("the selection moved to %d", m.trackCursor)
	}
	if len(st.prefetched) != 0 {
		t.Errorf("prefetched %v; nothing was chosen", st.prefetched)
	}
	// The view really moved: the first row on screen is further down.
	if !strings.Contains(m.View().Content, "track-"+itoa(wheelStep)) {
		t.Error("the window did not move")
	}

	next, cmd = m.Update(wheel(trackX, trackRow(0), tea.MouseWheelUp))
	m = drain(t, next.(Model), cmd)
	if m.trackOffset != 0 {
		t.Errorf("scrolling back left the offset at %d", m.trackOffset)
	}
}

// It stops at both ends rather than running off them.
func TestWheelStopsAtTheEnds(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	m.Tracks = rows(20)

	for range 20 {
		next, _ := m.Update(wheel(trackX, trackRow(0), tea.MouseWheelDown))
		m = next.(Model)
	}
	if want := 20 - m.listHeight(); m.trackOffset != want {
		t.Errorf("offset = %d, want %d — the last row should sit at the bottom", m.trackOffset, want)
	}
	for range 20 {
		next, _ := m.Update(wheel(trackX, trackRow(0), tea.MouseWheelUp))
		m = next.(Model)
	}
	if m.trackOffset != 0 {
		t.Errorf("offset = %d at the top", m.trackOffset)
	}
}

// A list that fits has nowhere to scroll to.
func TestWheelDoesNothingWhenEverythingFits(t *testing.T) {
	lib := library()
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, _ := m.Update(wheel(trackX, trackRow(0), tea.MouseWheelDown))
	if got := next.(Model).trackOffset; got != 0 {
		t.Errorf("offset = %d, want 0", got)
	}
}

// Moving the cursor brings the view back to it.
func TestMovingTheCursorSnapsTheViewBack(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	m.Tracks = rows(100)
	for range 10 {
		next, _ := m.Update(wheel(trackX, trackRow(0), tea.MouseWheelDown))
		m = next.(Model)
	}
	if m.trackOffset == 0 {
		t.Fatal("the view did not move")
	}

	next, _ := m.Update(keyPress("j"))
	m = next.(Model)
	if m.trackCursor != 1 {
		t.Fatalf("cursor = %d", m.trackCursor)
	}
	if m.trackOffset != 1 {
		t.Errorf("offset = %d, want the view back on the cursor", m.trackOffset)
	}
}

// Over the tabs the wheel changes tab.
func TestWheelOverTheTabsSwitchesThem(t *testing.T) {
	lib := library()
	lib.tracks["PL1"] = []ytm.Track{{VideoID: "c", Title: "Gamma"}}
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}, {ID: "PL1", Title: "Favorites"}}

	next, cmd := m.Update(wheel(m.tabSpans()[0].start+1, 1, tea.MouseWheelDown))
	m = drain(t, next.(Model), cmd)
	if m.tabCursor != 1 {
		t.Errorf("tab cursor = %d", m.tabCursor)
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

	// Every tab is hit where it is drawn.
	for _, span := range m.tabSpans() {
		title := m.tabAt(span.index).Title
		if !strings.Contains(plain(lines[1]), title) {
			t.Fatalf("tab %q is not on the label row: %q", title, plain(lines[1]))
		}
		for _, x := range []int{span.start, span.start + 1, span.end - 1} {
			if where, n := m.hit(x, 1); where != regionTabs || n != span.index {
				t.Errorf("hit at column %d = %v, %d; want tab %d", x, where, n, span.index)
			}
		}
	}
	for row, want := range map[int]string{0: "Alpha", 1: "Beta"} {
		line := lines[trackRow(row)]
		if !strings.Contains(line, want) {
			t.Fatalf("row %d is %q, not %q", trackRow(row), plain(line), want)
		}
		if where, n := m.hit(trackX, trackRow(row)); where != regionTracks || n != row {
			t.Errorf("hit on the track row %d = %v, %d", row, where, n)
		}
	}

	// The bar row is the one with the bar drawn on it, and it is the whole
	// row: nothing flanks it.
	const barChars = string(progress.DefaultFullCharHalfBlock) +
		string(emptyCell)
	if !strings.ContainsAny(lines[m.barRow()], barChars) {
		t.Fatalf("row %d is %q, which has no bar on it", m.barRow(), lines[m.barRow()])
	}
	if got := lipgloss.Width(lines[m.barRow()]); got != m.width {
		t.Errorf("the bar row is %d cells wide, want the full %d", got, m.width)
	}
	// The status bar's block carries the spinner, whose frames are block
	// glyphs too; that is a loader, not a bar.
	for row, line := range lines {
		if row != m.barRow() && row != m.statusRow() && strings.ContainsAny(line, barChars) {
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

// The bar is drawn where the position is, with nothing in between.
func TestTheBarFollowsThePositionExactly(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	m.Length = 100 * time.Second
	start, width := m.barGeometry()

	next, _ := m.Update(click(start+width*3/4, m.barRow()))
	m = next.(Model)

	if got := m.fraction(); got < 0.7 || got > 0.8 {
		t.Fatalf("position is at %v of the track, want three quarters", got)
	}
	row := plain(strings.Split(m.View().Content, "\n")[m.barRow()])
	full := strings.Count(row, string(progress.DefaultFullCharHalfBlock))
	if want := width * 3 / 4; full < want-1 || full > want+1 {
		t.Errorf("%d cells filled of %d, want about %d", full, width, want)
	}
}

// The bar has to occupy exactly the width the hit-testing assumes, or a
// click lands somewhere other than where it looks.
func TestBarRendersExactlyItsGeometry(t *testing.T) {
	for _, width := range []int{40, 80, 100, 120, 200} {
		m := New(Services{})
		sized, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		m = sized.(Model)
		m.Length, m.Position = time.Minute, 30*time.Second

		// Measured on what is drawn, not on the model's own bar: the width
		// belongs to the render now, because it depends on how wide the
		// times beside it read.
		_, barWidth := m.barGeometry()
		row := plain(m.renderBar())
		drawn := strings.Count(row, string(progress.DefaultFullCharHalfBlock)) +
			strings.Count(row, string(emptyCell))
		if drawn != barWidth {
			t.Errorf("width %d: bar renders %d cells, geometry says %d",
				width, drawn, barWidth)
		}
	}
}

// mpv keeps playing while a drag is in progress. Letting its position
// through would make the bar fight the pointer.
func TestPlaybackDoesNotFightADrag(t *testing.T) {
	au := newFakeAudio(player.Event{Name: "time-pos", Data: 12.0})
	m := wired(t, library(), &fakeStreams{}, au)
	m.Length = 200 * time.Second
	start, width := m.barGeometry()

	next, cmd := m.Update(click(start+width/2, m.barRow()))
	m = drain(t, next.(Model), cmd)
	dragged := m.Position

	m = drain(t, m, m.watchEvents())
	if m.Position != dragged {
		t.Fatalf("a playback tick moved the bar from %v to %v mid-drag", dragged, m.Position)
	}

	// Once the button is up, the position follows playback again.
	next, cmd = m.Update(release(start+width/2, m.barRow()))
	m = drain(t, next.(Model), cmd)
	m = drain(t, m, func() tea.Msg { return eventMsg(player.Event{Name: "time-pos", Data: 99.0}) })
	if m.Position != 99*time.Second {
		t.Errorf("position = %v after the drag ended, want playback back in charge", m.Position)
	}
}

// plain strips the styling so the text underneath can be asserted on.
var ansiSequence = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

func plain(s string) string { return ansiSequence.ReplaceAllString(s, "") }

// The bar row carries no numbers: no elapsed time, no total, no percentage.
func TestTheBarRowIsTheBarBetweenTwoTimes(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	m.Length, m.Position = 256*time.Second, 64*time.Second

	row := plain(strings.Split(m.View().Content, "\n")[m.barRow()])
	// Where it is and where it runs to, as wide as they read and no wider.
	if !strings.HasPrefix(row, "1:04 ") {
		t.Errorf("the row does not start at the position: %q", row)
	}
	if !strings.HasSuffix(row, " 4:16") {
		t.Errorf("the row does not end at the length: %q", row)
	}
	if strings.Contains(row, "%") {
		t.Errorf("the bar shows a percentage: %q", row)
	}
	if got := lipgloss.Width(row); got != m.width {
		t.Errorf("the row is %d cells, want %d", got, m.width)
	}

	start, width := m.barGeometry()
	full := strings.Count(row, string(progress.DefaultFullCharHalfBlock))
	empty := strings.Count(row, string(emptyCell))
	if full+empty != width {
		t.Errorf("the bar is %d cells of the %d it is given", full+empty, width)
	}
	// It sits where hit-testing says it does, which is past the position and
	// the space after it.
	if want := len("1:04") + 1; start != want {
		t.Errorf("the bar starts at %d, want %d", start, want)
	}
	// A quarter of the way in, a quarter of the bar should be filled.
	if want := width / 4; full < want-2 || full > want+2 {
		t.Errorf("%d cells filled, want about %d", full, want)
	}
}

// The times are as wide as they read, so the bar gives up a cell when a
// track passes ten minutes and takes it back on the next one. What has to
// hold is that the row is always the width of the terminal and that the bar
// is where hit-testing says it is.
func TestTheBarAndItsTimesAlwaysFillTheRow(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())

	at := func(pos, length time.Duration) (Model, string) {
		m.Position, m.Length = pos, length
		return m, plain(strings.Split(m.View().Content, "\n")[m.barRow()])
	}
	for _, tc := range []struct {
		name           string
		pos, length    time.Duration
		wantAt, wantTo string
	}{
		{"idle", 0, 0, labelIdlePosition, labelIdleLength},
		{"under ten minutes", 4 * time.Second, 9 * time.Minute, "0:04", "9:00"},
		{"over ten minutes", 10 * time.Minute, 59 * time.Minute, "10:00", "59:00"},
		{"over an hour", time.Hour, 2 * time.Hour, "60:00", "120:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, row := at(tc.pos, tc.length)
			if got := lipgloss.Width(row); got != at.width {
				t.Errorf("the row is %d cells, want %d: %q", got, at.width, row)
			}
			if !strings.HasPrefix(row, tc.wantAt+" ") {
				t.Errorf("the row starts %q, want %q", row, tc.wantAt)
			}
			if !strings.HasSuffix(row, " "+tc.wantTo) {
				t.Errorf("the row ends %q, want %q", row, tc.wantTo)
			}
			// The bar starts just past the position it is drawn after.
			start, width := at.barGeometry()
			if want := lipgloss.Width(tc.wantAt) + 1; start != want {
				t.Errorf("the bar starts at %d, want %d", start, want)
			}
			// And the three of them come to the whole row. Cells, not bytes:
			// the ellipsis is one cell and three of them.
			if got := lipgloss.Width(tc.wantAt) + 1 + width + 1 +
				lipgloss.Width(tc.wantTo); got != at.width {
				t.Errorf("position, bar and length come to %d of %d", got, at.width)
			}
		})
	}
}

// Under a certain width the times give way: a hint that has eaten the thing
// it was hinting at is not one.
func TestTheTimesGiveWayOnANarrowRow(t *testing.T) {
	for _, width := range []int{4, 12, 19, 20, 40, 100} {
		m := New(Services{})
		sized, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		m = sized.(Model)
		m.Length, m.Position = time.Minute, 30*time.Second

		row := plain(strings.Split(m.View().Content, "\n")[m.barRow()])
		if got := lipgloss.Width(row); got != width {
			t.Errorf("width %d: the bar row is %d cells", width, got)
		}
		start, barWidth := m.barGeometry()
		if m.barLayout().times {
			at, _ := m.barTimes()
			if want := lipgloss.Width(at) + 1; start != want {
				t.Errorf("width %d: the bar starts at %d, want %d", width, start, want)
			}
			if barWidth < barLeastWidth {
				t.Errorf("width %d: only %d cells of bar left", width, barWidth)
			}
		} else if start != 0 {
			t.Errorf("width %d: no times, but the bar starts at %d", width, start)
		}
	}
}

// The bar has to use the terminal's own palette. An SGR foreground of
// 38;2;r;g;b or 38;5;n is a colour this program chose; 3x and 9x are the
// scheme's, whatever the user has set them to.
func TestTheBarStaysInTheTerminalPalette(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	m.Length, m.Position = 100*time.Second, 50*time.Second

	row := strings.Split(m.View().Content, "\n")[m.barRow()]
	if strings.Contains(row, "38;2;") || strings.Contains(row, "48;2;") {
		t.Errorf("the bar emits true colour, which ignores the scheme:\n%q", row)
	}
	if strings.Contains(row, "38;5;") || strings.Contains(row, "48;5;") {
		t.Errorf("the bar emits 256-colour indices, which ignore the scheme:\n%q", row)
	}
	// And it is actually coloured, so the check above is not vacuous.
	if !ansiSequence.MatchString(row) {
		t.Error("the bar is not styled at all")
	}
}

// The whole interface, not just the bar.
func TestNothingRendersOffPaletteColours(t *testing.T) {
	lib := library()
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Playlists = []Playlist{{Title: "Liked Music"}, {Title: "Favorites"}}
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.Tracks[0].Rating = RatingUp
	m.Length, m.Position = 100*time.Second, 25*time.Second
	m.Err = errors.New("something went wrong")

	for _, bad := range []string{"38;2;", "48;2;", "38;5;", "48;5;"} {
		if strings.Contains(m.View().Content, bad) {
			t.Errorf("the interface emits %q, which ignores the terminal scheme", bad)
		}
	}
}

// Paused, the bar keeps its shape but stops being the lit thing on screen.
func TestThePausedBarIsGreyed(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	m.Length, m.Position = 100*time.Second, 50*time.Second

	playing := strings.Split(m.View().Content, "\n")[m.barRow()]
	// Lit is the player's blue, the same colour the row and the state block
	// take. Paused drops to the ordinary foreground, which is quieter but
	// still a playhead — the groove behind it is the row highlight, and
	// neither of those matches that.
	lit := []string{liveFG}
	if !anyCode(playing, lit) {
		t.Fatalf("the playing bar is not lit at all; the test proves nothing:\n%q", playing)
	}

	m.Paused = true
	paused := strings.Split(m.View().Content, "\n")[m.barRow()]
	if anyCode(paused, lit) {
		t.Errorf("the paused bar is still lit: %v", sgrCodes(paused))
	}
	if !sgrCodes(paused)[foregroundFG] {
		t.Errorf("the paused bar has lost its playhead: %v", sgrCodes(paused))
	}
	// It is still a bar, and the same length.
	full := func(s string) int {
		return strings.Count(s, string(progress.DefaultFullCharHalfBlock))
	}
	if full(paused) != full(playing) || full(paused) == 0 {
		t.Errorf("the paused bar is %d cells, the playing one %d", full(paused), full(playing))
	}
}

// sgrCodes pulls the parameters out of a line's escape sequences. Matching
// whole sequences does not work: lipgloss combines them, writing a
// foreground and a background as one "34;44".
func sgrCodes(s string) map[string]bool {
	out := map[string]bool{}
	for _, seq := range ansiSequence.FindAllString(s, -1) {
		body := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m")
		for _, p := range strings.Split(body, ";") {
			out[p] = true
		}
	}
	return out
}

func anyCode(s string, codes []string) bool {
	present := sgrCodes(s)
	for _, c := range codes {
		if present[c] {
			return true
		}
	}
	return false
}
