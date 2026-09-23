package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// press feeds a keystroke through Update, the way the runtime would.
func press(m Model, keys ...string) Model {
	for _, k := range keys {
		next, _ := m.Update(keyPress(k))
		m = next.(Model)
	}
	return m
}

func sample() Model {
	m := New(Services{})
	m.Playlists = []Playlist{{ID: "1", Title: "One"}, {ID: "2", Title: "Two"}, {ID: "3", Title: "Three"}}
	m.Tracks = []Track{
		{VideoID: "a", Title: "Alpha", Artist: "A", Duration: time.Minute},
		{VideoID: "b", Title: "Beta", Artist: "B", Duration: 2 * time.Minute},
	}
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return sized.(Model)
}

// rows builds a list long enough to need scrolling.
func rows(n int) []Track {
	out := make([]Track, n)
	for i := range out {
		out[i] = Track{VideoID: string(rune('a' + i%26)), Title: trackName(i)}
	}
	return out
}

func trackName(i int) string { return "track-" + strings.Repeat("x", 0) + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestTabKeysMoveBetweenTabs(t *testing.T) {
	m := sample()
	if got, _ := m.SelectedPlaylist(); got.Title != "One" {
		t.Fatalf("starts on %q", got.Title)
	}
	if m = press(m, "l"); m.tabCursor != 1 {
		t.Errorf("l went to %d", m.tabCursor)
	}
	if m = press(m, "tab"); m.tabCursor != 2 {
		t.Errorf("tab went to %d", m.tabCursor)
	}
	// And it stops at the end rather than wrapping.
	if m = press(m, "l", "l"); m.tabCursor != 2 {
		t.Errorf("ran past the last tab to %d", m.tabCursor)
	}
	if m = press(m, "h", "h", "h"); m.tabCursor != 0 {
		t.Errorf("h stopped at %d", m.tabCursor)
	}
}

func TestCursorMovesThroughTracks(t *testing.T) {
	m := sample()
	if m = press(m, "j"); m.trackCursor != 1 {
		t.Errorf("j went to %d", m.trackCursor)
	}
	if m = press(m, "j", "j"); m.trackCursor != 1 {
		t.Errorf("ran past the last track to %d", m.trackCursor)
	}
	if m = press(m, "k", "k"); m.trackCursor != 0 {
		t.Errorf("k stopped at %d", m.trackCursor)
	}
}

func TestEmptyListsDoNotPanic(t *testing.T) {
	m := New(Services{})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(sized.(Model), "j", "k", "h", "l", "enter", "+", "-", "G", "g")
	_ = m.View()
}

// The list is taller than the window, so it has to scroll to reach the end.
func TestLongListScrollsToItsEnd(t *testing.T) {
	m := New(Services{})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = sized.(Model)
	m.Tracks = rows(100)
	height := m.bodyHeight()

	// Walking down past the window edge moves it, one row at a time.
	for i := 0; i < height; i++ {
		m = press(m, "j")
	}
	if m.trackCursor != height {
		t.Fatalf("cursor = %d after %d presses", m.trackCursor, height)
	}
	if m.trackOffset != 1 {
		t.Errorf("offset = %d, want the window to have moved by one", m.trackOffset)
	}
	if !strings.Contains(m.View().Content, "track-"+itoa(height)) {
		t.Error("the row under the cursor is not on screen")
	}

	// The last row has to be reachable, which is the bug this covers.
	m = press(m, "G")
	if m.trackCursor != 99 {
		t.Fatalf("G left the cursor at %d", m.trackCursor)
	}
	if !strings.Contains(m.View().Content, "track-99") {
		t.Fatalf("the last row is not on screen:\n%s", m.View().Content)
	}
	if got := m.trackOffset; got != 100-height {
		t.Errorf("offset = %d, want %d", got, 100-height)
	}

	m = press(m, "g")
	if m.trackCursor != 0 || m.trackOffset != 0 {
		t.Errorf("g left cursor %d offset %d", m.trackCursor, m.trackOffset)
	}
}

func TestPageKeysMoveAWindowAtATime(t *testing.T) {
	m := New(Services{})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = sized.(Model)
	m.Tracks = rows(100)
	height := m.bodyHeight()

	m = press(m, "pgdown")
	if m.trackCursor != height {
		t.Errorf("pgdown went to %d, want %d", m.trackCursor, height)
	}
	m = press(m, "pgup")
	if m.trackCursor != 0 {
		t.Errorf("pgup went to %d", m.trackCursor)
	}
}

// Scrolling must never leave the window past the end of the list.
func TestOffsetStaysInsideTheList(t *testing.T) {
	m := New(Services{})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = sized.(Model)
	m.Tracks = rows(100)
	m = press(m, "G")

	m.Tracks = rows(3) // a shorter playlist arrives
	m.scroll()
	if m.trackOffset != 0 {
		t.Errorf("offset = %d, want 0 for a list that fits", m.trackOffset)
	}
}

func TestRatingTogglesOff(t *testing.T) {
	m := sample()
	if m = press(m, "+"); m.Tracks[0].Rating != RatingUp {
		t.Fatalf("rating = %v", m.Tracks[0].Rating)
	}
	if m = press(m, "+"); m.Tracks[0].Rating != RatingNone {
		t.Fatalf("second press left %v", m.Tracks[0].Rating)
	}
	if m = press(m, "-"); m.Tracks[0].Rating != RatingDown {
		t.Fatalf("rating = %v", m.Tracks[0].Rating)
	}
}

func TestSearchCapturesKeys(t *testing.T) {
	m := press(sample(), "/")
	if !m.Searching {
		t.Fatal("not in search mode")
	}
	// j and k are text here, not movement.
	m = press(m, "j", "k")
	if m.Query != "jk" || m.trackCursor != 0 {
		t.Fatalf("query = %q, cursor = %d", m.Query, m.trackCursor)
	}
	m = press(m, "backspace")
	if m.Query != "j" {
		t.Fatalf("query = %q", m.Query)
	}
	if m = press(m, "esc"); m.Searching || m.Query != "" {
		t.Fatalf("esc left searching=%v query=%q", m.Searching, m.Query)
	}
}

func TestViewRendersEveryRegion(t *testing.T) {
	out := sample().View().Content
	for _, want := range []string{"One", "Alpha", "Ready"} {
		if !strings.Contains(out, want) {
			t.Fatalf("view is missing %q:\n%s", want, out)
		}
	}
	if lines := strings.Count(out, "\n") + 1; lines != 20 {
		t.Fatalf("view is %d lines, want the full 20-row height", lines)
	}
}

// Idle says Ready rather than naming the playlist, which the tabs already do.
func TestIdleSaysReady(t *testing.T) {
	m := sample()
	if !strings.Contains(m.View().Content, "Ready") {
		t.Error("idle status is not Ready")
	}
	m.NowPlaying = "DAPHNI — Poly"
	out := m.View().Content
	if strings.Contains(out, "Ready") || !strings.Contains(out, "DAPHNI — Poly") {
		t.Error("playing status should replace Ready")
	}
}

func TestLoadingShowsTheSpinner(t *testing.T) {
	m := sample()
	m.loading = true
	out := plain(m.View().Content)
	if !strings.Contains(out, "Loading…") {
		t.Fatalf("no loading line:\n%s", out)
	}
	frames := 0
	for _, f := range m.spin.Spinner.Frames {
		if strings.Contains(out, strings.TrimSpace(f)) {
			frames++
		}
	}
	if frames == 0 {
		t.Errorf("no spinner frame on screen, want one of %q", m.spin.Spinner.Frames)
	}
}

func TestViewIsStableBeforeFirstResize(t *testing.T) {
	// Rendering before a WindowSizeMsg must not divide by a zero width.
	if got := New(Services{}).View().Content; got != "" {
		t.Fatalf("unsized view = %q, want empty", got)
	}
}

// tmux's capture-pane renders runs of spaces as tabs, which looks like a
// layout bug in a screenshot. Assert on the real output instead.
func TestNoTabCharactersAndWidthIsExact(t *testing.T) {
	m := New(Services{})
	m.Playlists = []Playlist{{Title: "Liked Music"}, {Title: "Favorites"}}
	m.Tracks = []Track{{Title: "Poly", Artist: "DAPHNI", Duration: 6*time.Minute + 14*time.Second, Rating: RatingUp}}
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	out := sized.(Model).View().Content

	if strings.Contains(out, "\t") {
		t.Errorf("render contains tab characters")
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Errorf("line %d is %d cells wide, past the terminal's 100: %q", i, w, line)
		}
	}
}

// The tab row is a fixed three lines, or everything below it shifts.
func TestTabRowIsThreeLines(t *testing.T) {
	m := sample()
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) < tabsHeight {
		t.Fatal("no tab row")
	}
	if !strings.Contains(lines[1], "One") {
		t.Errorf("the tab label is not on the middle line: %q", plain(lines[1]))
	}
	if strings.Contains(plain(lines[tabsHeight]), "One") {
		t.Errorf("the tab row spills onto row %d", tabsHeight)
	}
	// The first track sits directly under the tabs.
	if !strings.Contains(lines[tabsHeight], "Alpha") {
		t.Errorf("row %d is %q, want the first track", tabsHeight, plain(lines[tabsHeight]))
	}
}
