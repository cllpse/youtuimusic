package ui

import (
	"errors"
	"fmt"
	"image/color"
	"regexp"
	"slices"
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
	if !codes[fillBG] {
		t.Errorf("the state block is not filled: %v", codes)
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
		if got := strings.Trim(plain(lines[row]), "│ "); strings.ContainsAny(got, "PolyCherry") {
			t.Errorf("row %d still holds the track: %q", row, got)
		}
	}
	// The bar and the buttons are where they were.
	if !strings.ContainsAny(plain(lines[m.barRow()]), "▌"+string(emptyCell)) {
		t.Errorf("no progress bar on row %d: %q", m.barRow(), plain(lines[m.barRow()]))
	}
	if !strings.Contains(lines[m.controlsRow()], labelPrevious) {
		t.Errorf("no controls on row %d", m.controlsRow())
	}
	// And the box still closes above the status bar, inside its margin.
	if bottom := strings.TrimRight(plain(lines[m.statusRow()-1]), " "); !strings.HasSuffix(bottom, "╯") {
		t.Errorf("the box does not close above the status bar: %q", bottom)
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
	if anyCode(lines[m.barRow()], []string{emphasisFG, fillBG}) {
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

// The frame is monochrome. "Emphasised" is the bright end of the foreground,
// and a fill is that turned inside out: the bright foreground as a
// background, the background colour as the text on it.
const (
	emphasisFG   = "97"
	foregroundFG = "37"
	fillBG       = "107"
	onFillFG     = "30"
)

// highlightSGR is the background a selected row renders as when the
// terminal has not said what its own background is — which it has not, in a
// test. That fallback is the scheme's dim entry used as a background.
const highlightSGR = "100"

// faintSGR is what dim text renders as: the terminal's own faint, so that it
// follows the theme rather than naming a grey the theme may have spent on
// being a shade of its background.
const faintSGR = "2"

// The bar and the controls sit in one box, and it costs no height: its
// border takes the rows the blank lines used to.
func TestThePlayerIsBoxed(t *testing.T) {
	m := sample()
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != 20 {
		t.Fatalf("view is %d lines, want 20", len(lines))
	}

	// The bar sits directly under the border, one blank line below it, then
	// the buttons, which are controlsRows tall.
	top, bottom := lines[m.barRow()-1], lines[m.controlsRow()+controlsRows]
	if !strings.HasPrefix(plain(top), "╭") || !strings.HasSuffix(plain(top), "╮") {
		t.Errorf("no top border: %q", plain(top))
	}
	if !strings.HasSuffix(plain(bottom), "╯") {
		t.Errorf("no bottom border: %q", plain(bottom))
	}
	if inner := strings.TrimSpace(plain(lines[m.barRow()])[1:]); inner == "" {
		t.Error("the row under the border is blank; the bar should be there")
	}
	if inner := strings.Trim(plain(lines[m.barRow()+1]), "│ "); inner != "" {
		t.Errorf("the row under the bar is not blank: %q", inner)
	}
	// Every row inside it is bounded by the sides, blank lines included.
	inside := []int{m.barRow(), m.barRow() + 1}
	for r := range controlsRows {
		inside = append(inside, m.controlsRow()+r)
	}
	for _, row := range inside {
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
		for i, cell := range scrollbarFor(len(m.Tracks), m.trackOffset, height, -1, m.highlightColor()) {
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
	if !sgrCodes(alpha)[highlightSGR] {
		t.Errorf("the selected row is not highlighted: %v", sgrCodes(alpha))
	}
	if sgrCodes(alpha)[emphasisFG] {
		t.Errorf("the selected row is coloured as if playing: %v", sgrCodes(alpha))
	}

	// The playing track is coloured and not highlighted.
	if !sgrCodes(beta)[emphasisFG] {
		t.Errorf("the playing row is not coloured: %v", sgrCodes(beta))
	}
	if sgrCodes(beta)[highlightSGR] {
		t.Errorf("the playing row is highlighted as if selected: %v", sgrCodes(beta))
	}

	// A row that is neither is left alone.
	if sgrCodes(gamma)[highlightSGR] || sgrCodes(gamma)[emphasisFG] {
		t.Errorf("an ordinary row is styled: %v", sgrCodes(gamma))
	}

	// And a row that is both says both.
	m.trackCursor = 1
	both := strings.Split(m.View().Content, "\n")[tabsHeight+headerRows+1]
	if !sgrCodes(both)[emphasisFG] || !sgrCodes(both)[highlightSGR] {
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
	if !sgrCodes(row)[highlightSGR] {
		t.Fatalf("the row is not highlighted at all: %v", sgrCodes(row))
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

// The highlight is derived from the page rather than named, because the
// scheme has one grey for it and a light theme spends that on being a shade
// of its own background.
func TestTheHighlightIsATintOfTheTerminalsBackground(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bg          color.Color
		wantLighter bool
	}{
		{"a light page darkens", color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}, false},
		{"a dark page lightens", color.RGBA{0x00, 0x00, 0x00, 0xFF}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sample()
			next, _ := m.Update(tea.BackgroundColorMsg{Color: tc.bg})
			m = next.(Model)

			if m.highlight == nil {
				t.Fatal("the terminal answered and nothing was derived")
			}
			got, page := luminance(m.highlight), luminance(tc.bg)
			if tc.wantLighter && got <= page {
				t.Errorf("highlight %v is not lighter than the page %v", got, page)
			}
			if !tc.wantLighter && got >= page {
				t.Errorf("highlight %v is not darker than the page %v", got, page)
			}
			// Close to the page, or a row of text stops reading as text.
			if diff := got - page; diff > 0.4 || diff < -0.4 {
				t.Errorf("the highlight is %v off the page; too far", diff)
			}
		})
	}
}

// Until the terminal answers — and some never do — the scheme's own grey
// stands in, which is what this always used.
func TestTheHighlightFallsBackToTheScheme(t *testing.T) {
	m := sample()
	if m.highlight != nil {
		t.Fatal("something was derived without the terminal saying anything")
	}
	if got := m.highlightColor(); got != surface {
		t.Errorf("the fallback is %v, want the scheme's own %v", got, surface)
	}
	// And a selected row still renders with it.
	m.Tracks = []Track{{VideoID: "a", Title: "Alpha"}, {VideoID: "b", Title: "Beta"}}
	m.trackCursor = 0
	row := strings.Split(m.View().Content, "\n")[tabsHeight+headerRows]
	if !sgrCodes(row)[highlightSGR] {
		t.Errorf("the selected row is not filled: %v", sgrCodes(row))
	}
}

// luminance is rough and only used to compare two shades of the same page.
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 65535
}

// A terminal that cannot say what colour it is must leave the fallback
// alone. A tint of nothing is nothing, and the row would be styled and
// invisible.
func TestAnUnusableBackgroundAnswerIsIgnored(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.BackgroundColorMsg
	}{
		{"no colour at all", tea.BackgroundColorMsg{}},
		{"fully transparent", tea.BackgroundColorMsg{Color: color.RGBA{0x20, 0x20, 0x20, 0x00}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sample()
			next, _ := m.Update(tc.msg)
			m = next.(Model)

			if m.highlight != nil {
				t.Errorf("derived %v from an answer that said nothing", m.highlight)
			}
			if got := m.highlightColor(); got != surface {
				t.Errorf("the fallback was lost: %v", got)
			}
		})
	}
}

// colorTriples is every truecolor parameter in a rendered line, foreground
// and background alike.
func colorTriples(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`[34]8;2;(\d+;\d+;\d+)`).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// The bar's groove, the wide half of the status bar, the selected row and
// the scrollbar are one surface. Whatever the highlight turns out to be, all
// four take it — they were separate colours once, and it looked like it.
func TestOneSurfaceForEveryQuietPartOfTheFrame(t *testing.T) {
	m := sample()
	next, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}})
	m = next.(Model)
	// Long enough to need a scrollbar, which is the fourth of the four.
	m.Tracks = rows(100)
	m.playing = m.Tracks[0]
	m.Length, m.Position = time.Minute, 30*time.Second
	m.trackCursor = 1 // not the playing row, so the fill is the only styling

	r, g, b, _ := m.highlightColor().RGBA()
	want := fmt.Sprintf("%d;%d;%d", r>>8, g>>8, b>>8)
	if want == "0;0;0" {
		t.Fatalf("the highlight was not derived: %v", m.highlightColor())
	}

	lines := strings.Split(m.View().Content, "\n")
	for _, tc := range []struct {
		what string
		row  int
	}{
		{"the bar's groove", m.barRow()},
		{"the status bar", m.statusRow()},
		{"the selected row", tabsHeight + headerRows + 1},
		// A row that is neither selected nor playing: the only thing on it
		// that can carry the colour is the scrollbar.
		{"the scrollbar", tabsHeight + headerRows + 4},
	} {
		if got := colorTriples(lines[tc.row]); !slices.Contains(got, want) {
			t.Errorf("%s does not use the highlight %s: %v", tc.what, want, got)
		}
	}
}

// chromaticCodes are the SGR parameters that name a hue, foreground and
// background, ordinary and bright. Red is left out: it is the one colour the
// interface keeps, and only for trouble.
var chromaticCodes = []string{
	"32", "33", "34", "35", "36", "42", "43", "44", "45", "46",
	"92", "93", "94", "95", "96", "102", "103", "104", "105", "106",
}

// The interface is monochrome. Everything that has to stand out does it by
// weight or by being turned inside out, so a hue appearing anywhere is a
// regression — and an easy one to make, since reaching for a colour is the
// obvious way to mark something.
func TestNothingIsColouredButTrouble(t *testing.T) {
	m := sample()
	next, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}})
	m = next.(Model)
	m.Tracks = rows(100)
	m.playing, m.Length, m.Position = m.Tracks[3], time.Minute, 20*time.Second
	m.playing.Rating = RatingUp
	m.Tracks[3].Rating = RatingUp
	m.trackCursor, m.repeat, m.sort = 3, RepeatAll, sortSpec{by: sortTitle}
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}, {ID: "PL1", Title: "Favorites"}}

	for _, state := range []struct {
		name  string
		setup func(Model) Model
	}{
		{"playing", func(m Model) Model { return m }},
		{"paused", func(m Model) Model { m.Paused = true; return m }},
		{"loading", func(m Model) Model { m.loading = true; return m }},
		{"with the menu open", func(m Model) Model {
			return m.openMenu(m.Tracks[3], 4, 4)
		}},
		{"with a popover open", func(m Model) Model {
			m.detour = detour{active: true, tracks: m.Tracks,
				tab: Playlist{ID: "MPRE", Title: "Cherry", kind: tabAlbum}}
			return m
		}},
	} {
		t.Run(state.name, func(t *testing.T) {
			frame := state.setup(m).View().Content
			for code := range sgrCodes(frame) {
				if slices.Contains(chromaticCodes, code) {
					t.Errorf("a hue got in: SGR %s", code)
				}
				if code == "31" || code == "41" {
					t.Errorf("red without trouble: SGR %s", code)
				}
			}
		})
	}

	// And trouble is still red, or the exception is worth nothing.
	m.Err = errors.New("something went wrong")
	codes := sgrCodes(m.View().Content)
	if !codes["41"] && !codes["31"] {
		t.Errorf("an error is not red: %v", codes)
	}
}

// Colour 8 is not a foreground for anything that has to be read or has to
// hold a line. A light theme has to spend it on being a shade of its own page
// — #BDBDBD on #FFFFFF is about 1.8:1 — so dim text is the terminal's own
// faint instead, and borders take the foreground.
//
// The two exceptions are the scrollbar and the bar's groove. Those are the
// row highlight, which is a surface and is meant to be near the page; where
// the terminal does not say what colour it is, colour 8 is the nearest thing
// the scheme has to that. Neither is text.
func TestTheDimColourIsNotDrawnAsText(t *testing.T) {
	m := sample()
	m.Tracks = rows(100)
	m.playing, m.Length, m.Position = m.Tracks[3], time.Minute, 20*time.Second
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}, {ID: "PL1", Title: "Favorites"}}

	for _, state := range []struct {
		name  string
		setup func(Model) Model
	}{
		{"playing", func(m Model) Model { return m }},
		{"idle", func(m Model) Model { m.playing = Track{}; return m }},
		{"paused", func(m Model) Model { m.Paused = true; return m }},
	} {
		t.Run(state.name, func(t *testing.T) {
			at := state.setup(m)
			lines := strings.Split(at.View().Content, "\n")

			rows := map[string]int{
				"the tab row":             1,
				"the rule under the tabs": tabsHeight - 1,
				"the player's top border": at.barRow() - 1,
				"the buttons":             at.controlsRow(),
				"the status bar":          at.statusRow(),
			}
			for what, row := range rows {
				if sgrCodes(lines[row])["90"] {
					t.Errorf("%s draws the dim colour: %q", what, plain(lines[row]))
				}
			}
			// Where it is dimmed, it is dimmed with faint.
			if !sgrCodes(lines[at.controlsRow()])[faintSGR] &&
				!sgrCodes(lines[1])[faintSGR] {
				t.Error("nothing is dimmed at all, so this proves nothing")
			}
		})
	}

	// It is still a background, which is the one thing it is good for.
	if !sgrCodes(sample().View().Content)[highlightSGR] {
		t.Error("it stopped being a background too")
	}
}
