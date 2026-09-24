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
	m.playing = Track{VideoID: "a", Title: "Poly", Artist: "DAPHNI", Album: "Cherry"}
	out := plain(m.View().Content)
	if strings.Contains(out, "Ready") {
		t.Error("playing status should replace Ready")
	}
	if !strings.Contains(out, "Poly") || !strings.Contains(out, "Cherry") {
		t.Errorf("the status line is missing the track or its album:\n%s", out)
	}
}

// The wait is shown where it is happening — in the list, under the tabs —
// rather than down in the status line.
func TestLoadingShowsTheSpinnerInTheList(t *testing.T) {
	m := sample()
	m.Tracks = nil
	m.loading = true

	lines := strings.Split(plain(m.View().Content), "\n")
	list := strings.Join(lines[tabsHeight:tabsHeight+m.bodyHeight()], "\n")
	if !strings.Contains(list, "Loading…") {
		t.Fatalf("nothing loading in the list:\n%s", list)
	}
	var frames int
	for _, f := range m.spin.Spinner.Frames {
		if strings.Contains(list, strings.TrimSpace(f)) {
			frames++
		}
	}
	if frames == 0 {
		t.Errorf("no spinner frame, want one of %q", m.spin.Spinner.Frames)
	}
	// And not in the status line, which says what is playing.
	if strings.Contains(lines[m.barRow()-1], "Loading…") {
		t.Errorf("the status line still says it: %q", lines[m.barRow()-1])
	}
}

// Reloading a list that is already on screen must not blank it.
func TestReloadingKeepsTheListVisible(t *testing.T) {
	m := sample()
	m.loading = true
	out := plain(m.View().Content)
	if !strings.Contains(out, "Alpha") {
		t.Error("the list was replaced by the spinner")
	}
	if strings.Contains(out, "Loading…") {
		t.Error("a reload should not cover what is already there")
	}
}

// The status line leads with the track, then its album, muted.
func TestTheStatusLineIsTitleThenAlbum(t *testing.T) {
	m := sample()
	m.playing = Track{VideoID: "a", Title: "Poly", Artist: "DAPHNI", Album: "Cherry"}

	// The status line on its own, so the box's own border is not in the way.
	status := m.statusLine()
	bare := plain(status)
	if column(bare, "Poly") > column(bare, "Cherry") {
		t.Errorf("the album comes first: %q", bare)
	}
	if strings.Contains(bare, "DAPHNI") {
		t.Errorf("the artist is on the line: %q", bare)
	}
	if !sgrCodes(status)["90"] {
		t.Errorf("nothing on the line is muted: %v", sgrCodes(status))
	}
	if before := status[:strings.Index(status, "Poly")]; strings.Contains(before, "\x1b[") {
		t.Errorf("the title is styled as well: %q", before)
	}
	// It is on the frame too, where it belongs.
	if !strings.Contains(plain(strings.Split(m.View().Content, "\n")[m.barRow()-1]), "Poly") {
		t.Error("the status line is not above the bar")
	}
}

// Paused is said by the bar going grey and the control becoming a play
// triangle; a word as well would be a third.
func TestNothingSaysPaused(t *testing.T) {
	m := sample()
	m.playing = Track{VideoID: "a", Title: "Poly"}
	m.Paused = true
	if out := plain(m.View().Content); strings.Contains(strings.ToLower(out), "paused") {
		t.Errorf("the frame says paused:\n%s", out)
	}
	if m.playPauseIcon() != iconPlay {
		t.Error("the control is not a play triangle")
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

// The title, the bar and the controls sit in one box, and it costs no
// height: its border takes the rows the blank lines used to.
func TestThePlayerIsBoxed(t *testing.T) {
	m := sample()
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != 20 {
		t.Fatalf("view is %d lines, want 20", len(lines))
	}

	top, bottom := lines[m.barRow()-2], lines[m.barRow()+2]
	if !strings.HasPrefix(plain(top), "╭") || !strings.HasSuffix(plain(top), "╮") {
		t.Errorf("no top border: %q", plain(top))
	}
	if !strings.HasPrefix(plain(bottom), "╰") || !strings.HasSuffix(plain(bottom), "╯") {
		t.Errorf("no bottom border: %q", plain(bottom))
	}
	// The three rows inside it are bounded by the sides.
	for _, row := range []int{m.barRow() - 1, m.barRow(), m.barRow() + 1} {
		line := plain(lines[row])
		if !strings.HasPrefix(line, "│") || !strings.HasSuffix(line, "│") {
			t.Errorf("row %d is not inside the box: %q", row, line)
		}
		if lipgloss.Width(lines[row]) != m.width {
			t.Errorf("row %d is %d cells, want %d", row, lipgloss.Width(lines[row]), m.width)
		}
	}
}

// A liked row is marked with the same icon the control below it uses.
func TestALikedRowIsMarkedWithTheThumb(t *testing.T) {
	m := sample()
	m.Tracks[0].Rating = RatingUp
	m.Tracks[1].Rating = RatingDown

	lines := strings.Split(m.View().Content, "\n")
	if !strings.Contains(lines[tabsHeight], iconThumbUp) {
		t.Errorf("no thumbs-up on the liked row: %q", plain(lines[tabsHeight]))
	}
	if !strings.Contains(lines[tabsHeight+1], iconThumbDown) {
		t.Errorf("no thumbs-down on the disliked row: %q", plain(lines[tabsHeight+1]))
	}
	if strings.ContainsAny(plain(lines[tabsHeight]), "+-") {
		t.Error("the old plus/minus is still there")
	}
}

// Everything in the liked playlist is liked, so the column would say the
// same thing all the way down.
func TestTheLikedPlaylistDropsTheRatingColumn(t *testing.T) {
	m := sample()
	m.Tracks[0].Rating = RatingUp

	elsewhere := plain(strings.Split(m.View().Content, "\n")[tabsHeight])

	m.showingID = likedPlaylistID
	liked := plain(strings.Split(m.View().Content, "\n")[tabsHeight])

	if strings.Contains(liked, iconThumbUp) {
		t.Errorf("the thumb is still drawn in the liked playlist: %q", liked)
	}
	if !strings.Contains(elsewhere, iconThumbUp) {
		t.Fatalf("the comparison is wrong; no thumb elsewhere either: %q", elsewhere)
	}
	// The titles still start in the same column, so the two tabs line up.
	// Columns, not byte offsets: the icon is four bytes and a space is one.
	if column(liked, "Alpha") != column(elsewhere, "Alpha") {
		t.Errorf("titles moved from column %d to %d:\n liked %q\n other %q",
			column(elsewhere, "Alpha"), column(liked, "Alpha"), liked, elsewhere)
	}
	if lipgloss.Width(liked) != lipgloss.Width(elsewhere) {
		t.Errorf("row widths differ: %d vs %d", lipgloss.Width(liked), lipgloss.Width(elsewhere))
	}
}

// column is where a substring starts on screen, which is not where it starts
// in the string.
func column(line, needle string) int {
	i := strings.Index(line, needle)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(line[:i])
}

// The scrollbar only exists when the list is longer than the window.
func TestScrollbarAppearsOnlyWhenItOverflows(t *testing.T) {
	m := New(Services{})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = sized.(Model)

	m.Tracks = rows(m.bodyHeight())
	if m.hasScrollbar() {
		t.Error("a list that fits has a scrollbar")
	}
	if line := plain(strings.Split(m.View().Content, "\n")[tabsHeight]); strings.ContainsAny(line, "█│") {
		t.Errorf("a bar is drawn anyway: %q", line)
	}

	m.Tracks = rows(m.bodyHeight() + 1)
	if !m.hasScrollbar() {
		t.Fatal("an overflowing list has no scrollbar")
	}
	for i := range m.bodyHeight() {
		line := plain(strings.Split(m.View().Content, "\n")[tabsHeight+i])
		if lipgloss.Width(line) != m.width {
			t.Fatalf("row %d is %d cells, want %d", i, lipgloss.Width(line), m.width)
		}
		cells := []rune(line)
		if bar := cells[m.scrollbarColumn()]; bar != '█' && bar != '│' {
			t.Errorf("row %d has %q where the bar should be", i, string(bar))
		}
		// And a blank column to its right, so it is not against the edge.
		if last := cells[m.width-1]; last != ' ' {
			t.Errorf("row %d ends with %q, want a space past the bar", i, string(last))
		}
	}
}

// The thumb sits where the window is, and covers the whole trough only when
// there is nothing to scroll.
func TestTheScrollbarThumbFollowsTheWindow(t *testing.T) {
	m := New(Services{})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = sized.(Model)
	m.Tracks = rows(100)
	height := m.bodyHeight()

	thumbTop := func(m Model) int {
		for i, cell := range m.scrollbar(height) {
			if strings.Contains(cell, "█") {
				return i
			}
		}
		return -1
	}

	if got := thumbTop(m); got != 0 {
		t.Errorf("at the top the thumb starts at %d", got)
	}
	m = press(m, "G")
	if got, want := thumbTop(m), height-max(1, height*height/100); got != want {
		t.Errorf("at the bottom the thumb starts at %d, want %d", got, want)
	}
	m = press(m, "g")
	if got := thumbTop(m); got != 0 {
		t.Errorf("back at the top the thumb starts at %d", got)
	}
}

// Clicking the bar puts the window where the click was, and dragging keeps
// moving it.
func TestClickingTheScrollbarScrolls(t *testing.T) {
	m := New(Services{})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = sized.(Model)
	m.Tracks = rows(200)
	height := m.bodyHeight()
	column := m.scrollbarColumn()

	furthest := 200 - height

	// The top of the trough is the top of the list.
	next, _ := m.Update(click(column, tabsHeight))
	if got := next.(Model).trackOffset; got != 0 {
		t.Errorf("clicking the top gave offset %d", got)
	}

	// Halfway down is roughly halfway through. Not exactly: the trough's
	// last cell has to mean the end, so a cell maps to row/(height-1).
	next, _ = m.Update(click(column, tabsHeight+height/2))
	m = next.(Model)
	if got, want := m.trackOffset, furthest/2; got < want-furthest/10 || got > want+furthest/10 {
		t.Errorf("offset = %d, want near %d", got, want)
	}
	if !m.draggingScroll {
		t.Error("pressing the bar should begin a drag")
	}
	if m.trackCursor != 0 {
		t.Errorf("the selection moved to %d", m.trackCursor)
	}

	// Dragging to the bottom takes the window to the end.
	next, _ = m.Update(motion(column, tabsHeight+height-1))
	m = next.(Model)
	if m.trackOffset != furthest {
		t.Errorf("offset = %d, want the end at %d", m.trackOffset, furthest)
	}

	next, _ = m.Update(release(column, tabsHeight+height-1))
	if next.(Model).draggingScroll {
		t.Error("still dragging after release")
	}
}

// A list that fits has no bar, so that column is an ordinary track row.
func TestTheLastColumnIsATrackWhenThereIsNoBar(t *testing.T) {
	m := New(Services{})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = sized.(Model)
	m.Tracks = rows(2)

	if where, n := m.hit(m.width-1, tabsHeight+1); where != regionTracks || n != 1 {
		t.Errorf("hit = %v, %d; want the track row", where, n)
	}
}
