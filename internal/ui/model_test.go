package ui

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	height := m.listHeight()

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
	height := m.listHeight()

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

// Search is a popover with its own input, so keys go to the box and not to
// the list behind it.
func TestSearchTypesIntoThePopover(t *testing.T) {
	m := press(sample(), "/")
	if !m.detour.active || m.detour.tab.kind != tabSearch {
		t.Fatal("/ did not open the search popover")
	}
	if !m.detour.typing {
		t.Error("the box does not have focus")
	}

	// j and k are text here, not movement.
	m = press(m, "j", "k")
	if m.detour.query != "jk" || m.trackCursor != 0 || m.detour.cursor != 0 {
		t.Fatalf("query = %q, list cursor = %d, popover cursor = %d",
			m.detour.query, m.trackCursor, m.detour.cursor)
	}
	m = press(m, "backspace")
	if m.detour.query != "j" {
		t.Fatalf("query = %q", m.detour.query)
	}
	if m = press(m, "esc"); m.detour.active {
		t.Fatal("esc left the popover open")
	}
}

func TestViewRendersEveryRegion(t *testing.T) {
	out := sample().View().Content
	for _, want := range []string{"One", "Alpha", "READY"} {
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
	idle := plain(strings.Split(m.View().Content, "\n")[m.statusRow()])
	if !strings.Contains(idle, "READY") || !strings.Contains(idle, "Nothing playing") {
		t.Errorf("idle bar reads %q", idle)
	}
	m.playing = Track{VideoID: "a", Title: "Poly", Artist: "DAPHNI", Album: "Cherry"}
	playing := plain(strings.Split(m.View().Content, "\n")[m.statusRow()])
	if strings.Contains(playing, "Nothing playing") {
		t.Error("the bar still says nothing is playing")
	}
	if !strings.Contains(playing, "Poly") || !strings.Contains(playing, "Cherry") {
		t.Errorf("the bar is missing the track or its album: %q", playing)
	}
}

// The wait is shown where it is happening — in the list, under the tabs —
// rather than down in the status line.
func TestLoadingShowsTheSpinnerInTheList(t *testing.T) {
	m := sample()
	m.Tracks = nil
	m.loading = true

	lines := strings.Split(plain(m.View().Content), "\n")
	list := strings.Join(lines[tabsHeight+headerRows:tabsHeight+m.bodyHeight()], "\n")
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
	// The status bar says so too, in its own words.
	if got := plain(lines[m.statusRow()]); !strings.Contains(got, "LOADING") {
		t.Errorf("the status bar does not say it: %q", got)
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

// The status bar is two blocks: what the app is doing, then what is
// playing. Both used to live inside the player box.
func TestTheStatusBarSaysWhatIsPlaying(t *testing.T) {
	m := sample()
	m.playing = Track{VideoID: "a", Title: "Poly", Artist: "DAPHNI", Album: "Cherry"}

	bar := strings.Split(m.View().Content, "\n")[m.statusRow()]
	bare := plain(bar)
	if !strings.Contains(bare, "PLAYING") {
		t.Errorf("no state block: %q", bare)
	}
	if column(bare, "PLAYING") > column(bare, "Poly") {
		t.Errorf("the state block is not first: %q", bare)
	}
	if column(bare, "Poly") > column(bare, "Cherry") {
		t.Errorf("the album comes before the title: %q", bare)
	}
	if strings.Contains(bare, "DAPHNI") {
		t.Errorf("the artist is on the bar: %q", bare)
	}
	if lipgloss.Width(bare) != m.width {
		t.Errorf("the bar is %d cells, want the full %d", lipgloss.Width(bare), m.width)
	}
	// Two fills: the bright one for the state, the quiet one for the rest.
	codes := sgrCodes(bar)
	if !codes["44"] {
		t.Errorf("the state block is not filled with the accent: %v", codes)
	}
	if !codes["100"] {
		t.Errorf("the rest of the bar is not filled: %v", codes)
	}
}

// The player holds the bar and the buttons, and nothing else: what is
// playing is the status bar's to say.
func TestThePlayerHoldsOnlyTheBarAndButtons(t *testing.T) {
	m := sample()
	m.playing = Track{VideoID: "a", Title: "Poly", Album: "Cherry"}

	lines := strings.Split(m.View().Content, "\n")
	for _, row := range []int{m.barRow() - 1, m.barRow() + 1} {
		if got := plain(lines[row]); strings.ContainsAny(got, "PolyCherry") {
			t.Errorf("row %d still holds the track: %q", row, got)
		}
	}
	// The bar and the buttons are where they were.
	if !strings.ContainsAny(plain(lines[m.barRow()]), "▌"+string(emptyCell)) {
		t.Errorf("no progress bar on row %d: %q", m.barRow(), plain(lines[m.barRow()]))
	}
	if !strings.Contains(lines[m.controlsRow()], iconPrevious) {
		t.Errorf("no controls on row %d", m.controlsRow())
	}
	// And the buttons are directly under the bar, with the blank line the
	// player ends on between them and the status bar.
	if !strings.Contains(plain(lines[m.controlsRow()]), iconPrevious) {
		t.Errorf("the buttons are not under the bar: %q", plain(lines[m.controlsRow()]))
	}
	if got := strings.TrimSpace(plain(lines[m.statusRow()-1])); got != "" {
		t.Errorf("the player does not end on a blank line: %q", got)
	}
}

// Trouble turns the state block red and puts the message beside it.
func TestTheStatusBarShowsErrors(t *testing.T) {
	m := sample()
	m.Err = errors.New("something went wrong")

	bar := strings.Split(m.View().Content, "\n")[m.statusRow()]
	if bare := plain(bar); !strings.Contains(bare, "ERROR") || !strings.Contains(bare, "something went wrong") {
		t.Errorf("bar reads %q", bare)
	}
	if !sgrCodes(bar)["41"] {
		t.Errorf("the state block is not red: %v", sgrCodes(bar))
	}
}

// The state block says what the player is doing, in the order that
// matters: trouble before a wait, a wait before the player, and ready only
// when there is nothing else to say.
func TestTheStateBlockSaysWhatThePlayerIsDoing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
		want  string
	}{
		{"nothing at all", func(m *Model) {}, "READY"},
		{"playing", func(m *Model) {
			m.playing = Track{VideoID: "a", Title: "Poly"}
		}, "PLAYING"},
		{"paused", func(m *Model) {
			m.playing = Track{VideoID: "a", Title: "Poly"}
			m.Paused = true
		}, "PAUSED"},
		{"loading beats the player", func(m *Model) {
			m.playing = Track{VideoID: "a", Title: "Poly"}
			m.loading = true
		}, "LOADING"},
		{"trouble beats everything", func(m *Model) {
			m.playing = Track{VideoID: "a", Title: "Poly"}
			m.loading = true
			m.Err = errors.New("no")
		}, "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sample()
			tc.setup(&m)
			if got := m.statusKey(); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
			bar := plain(strings.Split(m.View().Content, "\n")[m.statusRow()])
			if !strings.Contains(bar, tc.want) {
				t.Errorf("the bar reads %q", bar)
			}
		})
	}
}

// Paused is said once, in the block. The bar going grey and the control
// becoming a play triangle say it again without words.
func TestPausedIsSaidOnce(t *testing.T) {
	m := sample()
	m.playing = Track{VideoID: "a", Title: "Poly"}
	m.Length, m.Position = time.Minute, 30*time.Second
	m.Paused = true

	lines := strings.Split(m.View().Content, "\n")
	if got := plain(lines[m.statusRow()]); !strings.Contains(got, "PAUSED") {
		t.Errorf("the block does not say it: %q", got)
	}
	// Not a second time beside the track.
	if got := plain(lines[m.statusRow()]); strings.Count(strings.ToLower(got), "paused") != 1 {
		t.Errorf("said more than once: %q", got)
	}
	if m.playPauseIcon() != iconPlay {
		t.Error("the control is not a play triangle")
	}
	if anyCode(lines[m.barRow()], []string{"34", "44", "94", "104"}) {
		t.Error("the bar is still lit")
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
	if strings.Contains(plain(lines[tabsHeight+headerRows]), "One") {
		t.Errorf("the tab row spills onto row %d", tabsHeight)
	}
	// The first track sits directly under the tabs.
	if !strings.Contains(lines[tabsHeight+headerRows], "Alpha") {
		t.Errorf("row %d is %q, want the first track", tabsHeight, plain(lines[tabsHeight+headerRows]))
	}
}

// A rule divides the list from the player. The player is not a panel, so
// it is not boxed in like one.
func TestThePlayerSitsUnderARule(t *testing.T) {
	m := sample()
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != 20 {
		t.Fatalf("view is %d lines, want 20", len(lines))
	}

	if got, want := plain(lines[m.barRow()-2]), strings.Repeat("─", m.width); got != want {
		t.Errorf("the rule above the player is %q", got)
	}
	// Nothing in the player carries a frame any more.
	for _, row := range []int{m.barRow() - 2, m.barRow() - 1, m.barRow(),
		m.barRow() + 1, m.barRow() + 2} {
		if line := plain(lines[row]); strings.ContainsAny(line, "│╭╮╰╯") {
			t.Errorf("row %d is still boxed: %q", row, line)
		}
		if w := lipgloss.Width(lines[row]); w != m.width {
			t.Errorf("row %d is %d cells, want %d", row, w, m.width)
		}
	}

	// Air above the bar and below the buttons, none between them: they are
	// one control, not two.
	if got := strings.TrimSpace(plain(lines[m.barRow()-1])); got != "" {
		t.Errorf("the line under the rule is not blank: %q", got)
	}
	if got := strings.TrimSpace(plain(lines[m.barRow()+2])); got != "" {
		t.Errorf("the line under the buttons is not blank: %q", got)
	}
	if !strings.Contains(plain(lines[m.barRow()+1]), iconPrevious) {
		t.Errorf("the buttons are not directly under the bar: %q", plain(lines[m.barRow()+1]))
	}

	// The bar is at the list's gutter.
	bar := plain(lines[m.barRow()])
	if strings.TrimSpace(bar) == "" {
		t.Error("no bar under the rule")
	}
	if start, _ := m.barGeometry(); !strings.HasPrefix(bar, strings.Repeat(" ", start)) ||
		bar[start] == ' ' {
		t.Errorf("the bar does not start at column %d: %q", start, bar)
	}
}

// A liked row is marked with the same icon the control below it uses.
func TestALikedRowIsMarkedWithTheThumb(t *testing.T) {
	m := sample()
	m.Tracks[0].Rating = RatingUp
	m.Tracks[1].Rating = RatingDown

	lines := strings.Split(m.View().Content, "\n")
	if !strings.Contains(lines[tabsHeight+headerRows], iconThumbUp) {
		t.Errorf("no thumbs-up on the liked row: %q", plain(lines[tabsHeight+headerRows]))
	}
	if !strings.Contains(lines[tabsHeight+headerRows+1], iconThumbDown) {
		t.Errorf("no thumbs-down on the disliked row: %q", plain(lines[tabsHeight+headerRows+1]))
	}
	if strings.ContainsAny(plain(lines[tabsHeight+headerRows]), "+-") {
		t.Error("the old plus/minus is still there")
	}
}

// Everything in the liked playlist is liked, so the column would say the
// same thing all the way down.
func TestTheLikedPlaylistDropsTheRatingColumn(t *testing.T) {
	m := sample()
	m.Tracks[0].Rating = RatingUp

	elsewhere := plain(strings.Split(m.View().Content, "\n")[tabsHeight+headerRows])

	m.showingID = likedPlaylistID
	liked := plain(strings.Split(m.View().Content, "\n")[tabsHeight+headerRows])

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

	m.Tracks = rows(m.listHeight())
	if m.hasScrollbar() {
		t.Error("a list that fits has a scrollbar")
	}
	if line := plain(strings.Split(m.View().Content, "\n")[tabsHeight+headerRows]); strings.ContainsAny(line, "█│") {
		t.Errorf("a bar is drawn anyway: %q", line)
	}

	m.Tracks = rows(m.listHeight() + 1)
	if !m.hasScrollbar() {
		t.Fatal("an overflowing list has no scrollbar")
	}
	for i := range m.listHeight() {
		line := plain(strings.Split(m.View().Content, "\n")[tabsHeight+headerRows+i])
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
	height := m.listHeight()

	thumbTop := func(m Model) int {
		for i, cell := range scrollbarFor(len(m.Tracks), m.trackOffset, height) {
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
	height := m.listHeight()
	column := m.scrollbarColumn()

	furthest := 200 - height

	// The top of the trough is the top of the list.
	next, _ := m.Update(click(column, tabsHeight+headerRows))
	if got := next.(Model).trackOffset; got != 0 {
		t.Errorf("clicking the top gave offset %d", got)
	}

	// Halfway down is roughly halfway through. Not exactly: the trough's
	// last cell has to mean the end, so a cell maps to row/(height-1).
	next, _ = m.Update(click(column, tabsHeight+headerRows+height/2))
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
	next, _ = m.Update(motion(column, tabsHeight+headerRows+height-1))
	m = next.(Model)
	if m.trackOffset != furthest {
		t.Errorf("offset = %d, want the end at %d", m.trackOffset, furthest)
	}

	next, _ = m.Update(release(column, tabsHeight+headerRows+height-1))
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

	if where, n := m.hit(m.width-1, tabsHeight+headerRows+1); where != regionTracks || n != 1 {
		t.Errorf("hit = %v, %d; want the track row", where, n)
	}
}

// The track playing and the row under the cursor are different things, and
// a row can be either, both, or neither.
func TestPlayingAndSelectedAreSeparate(t *testing.T) {
	m := sample()
	m.Tracks = []Track{
		{VideoID: "a", Title: "Alpha"},
		{VideoID: "b", Title: "Beta"},
		{VideoID: "c", Title: "Gamma"},
	}
	m.playing = m.Tracks[1] // Beta plays
	m.trackCursor = 0       // the cursor is on Alpha

	lines := strings.Split(m.View().Content, "\n")
	alpha, beta, gamma := lines[tabsHeight+headerRows], lines[tabsHeight+headerRows+1], lines[tabsHeight+headerRows+2]

	// The cursor is a filled background and nothing else.
	if !sgrCodes(alpha)["100"] {
		t.Errorf("the selected row is not highlighted: %v", sgrCodes(alpha))
	}
	if sgrCodes(alpha)["34"] {
		t.Errorf("the selected row is coloured as if playing: %v", sgrCodes(alpha))
	}

	// The playing track is coloured and not highlighted.
	if !sgrCodes(beta)["34"] {
		t.Errorf("the playing row is not coloured: %v", sgrCodes(beta))
	}
	if sgrCodes(beta)["100"] {
		t.Errorf("the playing row is highlighted as if selected: %v", sgrCodes(beta))
	}

	// A row that is neither is left alone.
	if sgrCodes(gamma)["100"] || sgrCodes(gamma)["34"] {
		t.Errorf("an ordinary row is styled: %v", sgrCodes(gamma))
	}

	// And a row that is both says both.
	m.trackCursor = 1
	both := strings.Split(m.View().Content, "\n")[tabsHeight+headerRows+1]
	if !sgrCodes(both)["34"] || !sgrCodes(both)["100"] {
		t.Errorf("the playing row under the cursor says %v, want both", sgrCodes(both))
	}
}

// The highlight has to run the width of the row, or it reads as a smear
// behind the text rather than as a bar.
func TestTheSelectionHighlightFillsTheRow(t *testing.T) {
	m := sample()
	m.Tracks = []Track{{VideoID: "a", Title: "Alpha", Artist: "A"}}
	m.trackCursor = 0

	row := strings.Split(m.View().Content, "\n")[tabsHeight+headerRows]
	if lipgloss.Width(plain(row)) != m.width {
		t.Fatalf("the row is %d cells, want %d", lipgloss.Width(plain(row)), m.width)
	}
	// Nothing turns the background off part way along.
	if i := strings.Index(row, "\x1b[49m"); i >= 0 && i < strings.LastIndex(row, "Alpha") {
		t.Errorf("the highlight stops before the text ends: %q", row)
	}
}

// Grey on a filled background is nothing, so a styled row must not mute its
// own columns.
func TestAHighlightedRowDoesNotMuteItsColumns(t *testing.T) {
	m := sample()
	m.Tracks = []Track{{VideoID: "a", Title: "Alpha", Artist: "DAPHNI"}}
	m.trackCursor = 0

	row := strings.Split(m.View().Content, "\n")[tabsHeight+headerRows]
	artist := row[strings.Index(row, "DAPHNI"):]
	if strings.Contains(artist[:len("DAPHNI")], "\x1b[") {
		t.Errorf("the artist is styled separately on a highlighted row: %q", row)
	}
	if !sgrCodes(row)["100"] {
		t.Fatalf("the row is not highlighted at all: %v", sgrCodes(row))
	}
}

// Clicking a column header orders by it; clicking again reverses.
func TestClickingTheHeaderSorts(t *testing.T) {
	m := sample()
	m.setTracks([]Track{
		{VideoID: "a", Title: "Zulu", Artist: "Zappa", Duration: 3 * time.Minute},
		{VideoID: "b", Title: "Alpha", Artist: "abba", Duration: time.Minute},
	})

	spans := m.table(m.width, m.bodyHeight()).headerSpans()
	var artist int
	for _, span := range spans {
		if span.by == sortArtist {
			artist = span.start + 1
		}
	}
	if artist == 0 {
		t.Fatal("no artist column on the header")
	}

	next, _ := m.Update(click(artist, tabsHeight))
	m = next.(Model)
	if m.sort.by != sortArtist || m.sort.desc {
		t.Fatalf("sort = %+v", m.sort)
	}
	if m.Tracks[0].Title != "Alpha" {
		t.Errorf("the list was not reordered: %q first", m.Tracks[0].Title)
	}

	next, _ = m.Update(click(artist, tabsHeight))
	m = next.(Model)
	if !m.sort.desc || m.Tracks[0].Title != "Zulu" {
		t.Errorf("the second click did not reverse: %+v, %q", m.sort, m.Tracks[0].Title)
	}
}

// s cycles the column and S reverses, without either needing the mouse.
func TestSortKeys(t *testing.T) {
	m := sample()
	m.setTracks([]Track{
		{VideoID: "a", Title: "Zulu", Artist: "Zappa"},
		{VideoID: "b", Title: "Alpha", Artist: "abba"},
	})

	m = press(m, "s")
	if m.sort.by != sortTitle {
		t.Fatalf("s sorted by %v", m.sort.by)
	}
	if m.Tracks[0].Title != "Alpha" {
		t.Errorf("the list was not reordered: %q first", m.Tracks[0].Title)
	}
	m = press(m, "S")
	if !m.sort.desc || m.Tracks[0].Title != "Zulu" {
		t.Errorf("S did not reverse: %+v, %q", m.sort, m.Tracks[0].Title)
	}
	// Cycling all the way round puts it back to the order it arrived in.
	m = press(m, "s", "s", "s")
	if m.sort.by != sortNone {
		t.Errorf("cycling ended on %v", m.sort.by)
	}
}

// Sorting moves the rows, so the cursor has to mean the row it points at.
func TestSortingKeepsTheCursorMeaningful(t *testing.T) {
	m := sample()
	m.setTracks([]Track{
		{VideoID: "a", Title: "Zulu"},
		{VideoID: "b", Title: "Alpha"},
	})
	m.trackCursor = 1

	m = press(m, "s")
	got, ok := m.SelectedTrack()
	if !ok {
		t.Fatal("nothing selected after sorting")
	}
	if got.Title != m.Tracks[m.trackCursor].Title {
		t.Errorf("the cursor points at %q but the row is %q", got.Title, m.Tracks[m.trackCursor].Title)
	}
	// It goes back to the top, which is where a reordered list starts.
	if m.trackCursor != 0 {
		t.Errorf("cursor = %d", m.trackCursor)
	}
}

// unfilled returns the printable text drawn with no background set, which
// is what a stray reset in the middle of a filled row leaves behind.
func unfilled(s string) string {
	var out strings.Builder
	filled := false
	for len(s) > 0 {
		if seq := ansiSequence.FindString(s); seq != "" && strings.HasPrefix(s, seq) {
			body := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m")
			for _, code := range strings.Split(body, ";") {
				switch {
				case code == "" || code == "0" || code == "49":
					filled = false
				case len(code) == 2 && code[0] == '4', len(code) == 3 && strings.HasPrefix(code, "10"):
					filled = true
				}
			}
			s = s[len(seq):]
			continue
		}
		r, size := utf8.DecodeRuneInString(s)
		if !filled && r != utf8.RuneError {
			out.WriteRune(r)
		}
		s = s[size:]
	}
	return out.String()
}

// The bar is one unbroken block of colour across the row. It was not: the
// title is bold, a nested style ends with a reset, and a reset clears the
// background as well as the weight — so the fill stopped at the comma.
func TestTheStatusBarFillIsUnbroken(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
	}{
		{"idle", func(m *Model) {}},
		{"a title alone", func(m *Model) {
			m.playing = Track{VideoID: "a", Title: "Xtal"}
		}},
		{"the one that broke it", func(m *Model) {
			m.playing = Track{VideoID: "a", Title: "The Cambrian Explosion",
				Album: "Phanerozoic I: Palaeozoic"}
		}},
		{"longer than the row", func(m *Model) {
			m.playing = Track{VideoID: "a", Title: strings.Repeat("long ", 40),
				Album: strings.Repeat("album ", 40)}
		}},
		{"an error", func(m *Model) { m.Err = errors.New("something went wrong") }},
		{"loading", func(m *Model) { m.loading = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sample()
			tc.setup(&m)
			bar := strings.Split(m.View().Content, "\n")[m.statusRow()]

			if gap := unfilled(bar); gap != "" {
				t.Errorf("part of the bar has no fill: %q\nin %q", gap, bar)
			}
			if w := lipgloss.Width(plain(bar)); w != m.width {
				t.Errorf("the bar is %d cells, want %d", w, m.width)
			}
		})
	}
}

// The comma case, spelled out: everything after the title keeps the fill.
func TestTheAlbumKeepsTheFill(t *testing.T) {
	m := sample()
	m.playing = Track{VideoID: "a", Title: "The Cambrian Explosion",
		Album: "Phanerozoic I: Palaeozoic"}

	bar := strings.Split(m.View().Content, "\n")[m.statusRow()]
	if !strings.Contains(plain(bar), "Phanerozoic") {
		t.Fatalf("the album is not on the bar: %q", plain(bar))
	}
	// Everything after the comma used to be drawn with no fill at all.
	if gap := unfilled(bar); strings.Contains(gap, "Phanerozoic") || gap != "" {
		t.Errorf("the fill stops at the comma; unfilled: %q", gap)
	}
}

// One loader, spelled one way, wherever it appears.
func TestEveryLoaderIsTheSame(t *testing.T) {
	m := sample()
	m.loading = true

	m.setTracks(nil)
	list := plain(m.View().Content)
	if !strings.Contains(list, loaderLabel) {
		t.Errorf("the list does not use it:\n%s", list)
	}

	// The row offering another page uses it too.
	table := trackTable{
		tracks: tableTracks(), width: 40, height: len(tableTracks()) + 1 + headerRows,
		more: true, loadingMore: true, loader: m.loader(),
	}
	if got := plain(table.rows()[len(tableTracks())+headerRows]); !strings.Contains(got, loaderLabel) {
		t.Errorf("the load-more row does not use it: %q", got)
	}

	// And nothing spells it any other way.
	for _, wrong := range []string{"loading…", "Loading...", "loading..."} {
		if strings.Contains(plain(m.View().Content), wrong) {
			t.Errorf("found %q on the frame", wrong)
		}
	}
}

// The bar's characters have to be in the font the terminal is using, or
// they are drawn from a fallback at whatever width that font likes and the
// row stops lining up. Shade blocks are the ones that are always there.
func TestTheBarUsesOnlySafeGlyphs(t *testing.T) {
	safe := map[rune]bool{'░': true, '▒': true, '▓': true, '█': true, '▌': true}
	if !safe[emptyCell] {
		t.Errorf("the unplayed cell is %q, which is not one of the shade or "+
			"block characters every monospace font carries", string(emptyCell))
	}
	if lipgloss.Width(string(emptyCell)) != 1 {
		t.Errorf("%q is %d cells wide; the bar is counted in single cells",
			string(emptyCell), lipgloss.Width(string(emptyCell)))
	}
}
