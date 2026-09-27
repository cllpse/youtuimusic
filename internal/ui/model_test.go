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
	// Nothing is chosen in the popover while the box has the keys.
	if m.detour.query != "jk" || m.trackCursor != 0 || m.detour.cursor != noRow {
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
	// Two fills: the state's hue on the block, the band on the rest.
	codes := sgrCodes(bar)
	if !codes[liveBG] {
		t.Errorf("the state block is not blue: %v", codes)
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
	// And the player ends on a blank line, above the status bar.
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
	if anyCode(lines[m.barRow()], []string{liveFG, fillBG}) {
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

// The state block's three fills, as the SGR parameters they come out as:
// ANSI red, green and blue as a background. They are the only hues in the
// frame — see TestNothingIsColouredButTheStateBlock.
const (
	alertBG = "41"
	goodBG  = "42"
	busyBG  = "43"
	liveBG  = "44"
	// likedFG and dislikedFG are what you think of a row, which used to be a
	// pair of thumbs in front of the title. Red is shared with trouble on
	// purpose — see the palette.
	likedFG    = "35"
	dislikedFG = "31"
	// And the same two as a background, which is how the lower half of a
	// scrollbar cell is drawn: the upper half is a foreground over it. Note
	// that dislikedBG is the same parameter as alertBG — one code, two
	// meanings, which is why the monochrome test can no longer say where a red
	// fill is allowed.
	likedBG    = "45"
	dislikedBG = "41"
	// liveFG is the same blue as text: what is playing takes it wherever it
	// is pointed at — the row in the list, its mark in the scrollbar, the
	// played part of the bar.
	liveFG = "34"
)

// highlightSGR is the background a selected row renders as when the
// terminal has not said what its own background is — which it has not, in a
// test. That fallback is the scheme's dim entry used as a background.
const highlightSGR = "100"

// faintSGR is what dim text renders as: the terminal's own faint, so that it
// follows the theme rather than naming a grey the theme may have spent on
// being a shade of its background.
const faintSGR = "2"

// The player is a rule under the list, the buttons, a blank, the bar over
// two rows, a blank — and no frame. Every row runs edge to edge the way the
// list and the tabs above it do.
func TestThePlayerIsUnboxedRows(t *testing.T) {
	m := sample()
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != 20 {
		t.Fatalf("view is %d lines, want 20", len(lines))
	}
	top := m.playerTop()
	if got, want := plain(lines[top]), strings.Repeat("─", m.width); got != want {
		t.Errorf("the player does not start on a rule: %q", got)
	}
	// The buttons sit directly under the rule, with no blank between them.
	if m.controlsRow() != top+1 {
		t.Errorf("the buttons are on row %d, want %d", m.controlsRow(), top+1)
	}
	// A blank before the bar and one after it, and nothing else blank.
	for _, row := range []int{m.barRow() - 1, m.barRow() + 1} {
		if got := strings.TrimSpace(plain(lines[row])); got != "" {
			t.Errorf("row %d should be blank: %q", row, got)
		}
	}
	if !strings.Contains(plain(lines[m.controlsRow()]), labelPrevious) {
		t.Errorf("the buttons are not on row %d: %q", m.controlsRow(), plain(lines[m.controlsRow()]))
	}
	if !strings.ContainsAny(plain(lines[m.barRow()]), "▌"+string(emptyCell)) {
		t.Errorf("the bar is not on row %d: %q", m.barRow(), plain(lines[m.barRow()]))
	}
	// The buttons come before the bar, which is the way round it reads.
	if m.controlsRow() >= m.barRow() {
		t.Errorf("the buttons are on row %d and the bar on %d",
			m.controlsRow(), m.barRow())
	}
	// Nothing is framed, and the status bar follows immediately.
	for row := top; row < top+playerRows; row++ {
		if line := plain(lines[row]); strings.ContainsAny(line, "│╭╮╰╯") {
			t.Errorf("row %d is boxed: %q", row, line)
		}
		if got := lipgloss.Width(lines[row]); got != m.width {
			t.Errorf("row %d is %d cells, want %d", row, got, m.width)
		}
	}
	if top+playerRows != m.statusRow() {
		t.Errorf("the player ends at %d and the status bar is at %d",
			top+playerRows, m.statusRow())
	}
}

// A rated row says so by its colour: magenta liked, red disliked. It used to
// carry a thumb in front of the title, which cost two cells of the title and a
// glyph out of the reader's font.
func TestARatedRowIsColoured(t *testing.T) {
	m := sample()
	m.Tracks[0].Rating = RatingUp
	m.Tracks[1].Rating = RatingDown
	// Off the rated rows, so the cursor's fill is not what is being read.
	m.trackCursor = 2

	lines := strings.Split(m.View().Content, "\n")
	like, dislike := lines[tabsHeight+headerRows], lines[tabsHeight+headerRows+1]

	if !sgrCodes(like)[likedFG] {
		t.Errorf("the liked row is not magenta: %v", sgrCodes(like))
	}
	if !sgrCodes(dislike)[dislikedFG] {
		t.Errorf("the disliked row is not red: %v", sgrCodes(dislike))
	}
	if sgrCodes(like)[dislikedFG] || sgrCodes(dislike)[likedFG] {
		t.Error("the two ratings are drawn in each other's colour")
	}
	// The whole row takes it, columns included: one colour run, because a
	// nested style would end in a reset and drop it for the rest of the line.
	for _, want := range []string{"Alpha", "A", "1:00"} {
		if !strings.Contains(plain(like), want) {
			t.Errorf("the liked row lost %q: %q", want, plain(like))
		}
	}
	if sgrCodes(like)[faintSGR] {
		t.Errorf("part of a coloured row is faint as well: %q", like)
	}
	// And nothing is drawn in front of the title, by mark or by space.
	if got := column(plain(like), "Alpha"); got != 0 {
		t.Errorf("the title starts at column %d, want the edge: %q", got, plain(like))
	}
}

// Everything in the liked playlist is liked, so a page of magenta would say
// nothing a page of plain rows does not.
func TestTheLikedPlaylistDropsTheRatingColour(t *testing.T) {
	m := sample()
	m.Tracks[0].Rating = RatingUp
	m.trackCursor = 2

	elsewhere := strings.Split(m.View().Content, "\n")[tabsHeight+headerRows]

	m.showingID = likedPlaylistID
	inLiked := strings.Split(m.View().Content, "\n")[tabsHeight+headerRows]

	if sgrCodes(inLiked)[likedFG] {
		t.Errorf("the row is still magenta in the liked playlist: %q", inLiked)
	}
	if !sgrCodes(elsewhere)[likedFG] {
		t.Fatalf("the comparison is wrong; not magenta elsewhere either: %q", elsewhere)
	}
	// The rows are identical apart from the colour: dropping a rating cannot
	// move a title any more, because a rating never took any room.
	if plain(inLiked) != plain(elsewhere) {
		t.Errorf("the rows differ by more than their colour:\n liked %q\n other %q",
			plain(inLiked), plain(elsewhere))
	}
	if got := column(plain(inLiked), "Alpha"); got != 0 {
		t.Errorf("the title starts at column %d, want the edge: %q", got, plain(inLiked))
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
		for i, cell := range m.table(m.width, m.bodyHeight()).scrollbar() {
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
	if sgrCodes(alpha)[liveFG] {
		t.Errorf("the selected row is coloured as if playing: %v", sgrCodes(alpha))
	}

	// The playing track is coloured and not highlighted.
	if !sgrCodes(beta)[liveFG] {
		t.Errorf("the playing row is not coloured: %v", sgrCodes(beta))
	}
	if sgrCodes(beta)[highlightSGR] {
		t.Errorf("the playing row is highlighted as if selected: %v", sgrCodes(beta))
	}

	// A row that is neither is left alone.
	if sgrCodes(gamma)[highlightSGR] || sgrCodes(gamma)[liveFG] {
		t.Errorf("an ordinary row is styled: %v", sgrCodes(gamma))
	}

	// And a row that is both says both.
	m.trackCursor = 1
	both := strings.Split(m.View().Content, "\n")[tabsHeight+headerRows+1]
	if !sgrCodes(both)[liveFG] || !sgrCodes(both)[highlightSGR] {
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
// background, ordinary and bright. Red is left out and checked on its own: it
// is for trouble and nothing else. Blue is in the list and allowed by name
// where it belongs, so that it staying the player's colour is a decision each
// test site makes rather than a gap in this one.
var chromaticCodes = []string{
	"32", "33", "34", "35", "36", "42", "43", "44", "45", "46",
	"92", "93", "94", "95", "96", "102", "103", "104", "105", "106",
}

// The interface is monochrome but for the player and what you think of a
// track. Everything else that has to stand out does it by weight or by being
// turned inside out, so a hue anywhere else is a regression — and an easy one
// to make, since reaching for a colour is the obvious way to mark something.
//
// Three things earn one. The player's blue goes wherever the track playing is
// pointed at: the row in the list, its mark in the scrollbar, the played part
// of the bar, the state block. Magenta and red are a rating, on the row and on
// the button that sets it. And the state block alone also carries green, yellow
// or red, since it is the one thing on screen that says how the app is going
// rather than what it holds — which of those it says is
// TestTheStateBlockIsColouredByState's.
//
// All four of those appear as a background as well as a foreground: two marks
// in one cell of the scrollbar are drawn as one over the other. Which means red
// as a fill can no longer be placed — it is the state block and it is a
// disliked track's half of a cell, in the same SGR parameter. Cyan, the bright
// sixteen and green or yellow anywhere but the block are still nothing at all.
func TestNothingIsColouredButThePlayerAndTheRatings(t *testing.T) {
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
		{"with the keys sheet open", func(m Model) Model {
			m.sheetOpen = true
			return m
		}},
	} {
		t.Run(state.name, func(t *testing.T) {
			at := state.setup(m)
			for row, line := range strings.Split(at.View().Content, "\n") {
				for code := range sgrCodes(line) {
					switch {
					case code == liveFG || code == liveBG:
						// The player's own colour, wherever it is pointing.
					case code == likedFG || code == dislikedFG,
						code == likedBG || code == dislikedBG:
						// What you think of a row: on the row, in the trough.
					case row == at.statusRow() && (code == goodBG || code == busyBG):
						// The state block, on the row the state block is on.
					case slices.Contains(chromaticCodes, code):
						t.Errorf("a hue got in on row %d: SGR %s", row, code)
					}
				}
			}
		})
	}

	// And trouble is still red, or the exception is worth nothing.
	m.Err = errors.New("something went wrong")
	codes := sgrCodes(m.View().Content)
	if !codes[alertBG] && !codes["31"] {
		t.Errorf("an error is not red: %v", codes)
	}
}

// privateUse finds the first glyph from a Private Use Area, which is where
// every Nerd Font icon lives: the BMP's own area at U+E000..U+F8FF and the two
// supplementary planes. Nothing in a text interface has business in one — a
// codepoint there means whatever the reader's font decides it means, and a font
// without it draws a box.
func privateUse(s string) (rune, bool) {
	for _, r := range s {
		switch {
		case r >= 0xE000 && r <= 0xF8FF,
			r >= 0xF0000 && r <= 0xFFFFD,
			r >= 0x100000 && r <= 0x10FFFD:
			return r, true
		}
	}
	return 0, false
}

// The app draws no icons at all. It lost them a pair at a time — the
// transport's, the menu's, the leading column's — and the thumbs on a rated row
// were the last of them, replaced by the colour of the row. Everything drawn
// now is a glyph any terminal font has.
func TestNothingDrawsAnIcon(t *testing.T) {
	m := sized(sample(), 120, 40)
	m.Tracks = rows(40)
	m.Tracks[0].Rating, m.Tracks[1].Rating = RatingUp, RatingDown
	m.playing, m.Length, m.Position = m.Tracks[0], time.Minute, 20*time.Second
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}, {ID: "PL1", Title: "Favorites"}}

	for _, state := range []struct {
		name  string
		setup func(Model) Model
	}{
		{"the frame", func(m Model) Model { return m }},
		{"paused", func(m Model) Model { m.Paused = true; return m }},
		{"loading", func(m Model) Model { m.loading = true; return m }},
		{"trouble", func(m Model) Model { m.Err = errors.New("no"); return m }},
		{"the track menu", func(m Model) Model { return m.openMenu(m.Tracks[0], 4, 6) }},
		{"a popover", func(m Model) Model {
			m.detour = detour{active: true, tracks: m.Tracks,
				tab: Playlist{ID: "MPRE", Title: "Cherry", kind: tabAlbum}}
			return m
		}},
		{"a search with nothing found", func(m Model) Model {
			m.detour = detour{active: true, searched: "x",
				tab: Playlist{Title: "Search", kind: tabSearch}}
			return m
		}},
		{"the keys sheet", func(m Model) Model { m.sheetOpen = true; return m }},
	} {
		t.Run(state.name, func(t *testing.T) {
			frame := plain(state.setup(m).View().Content)
			if glyph, found := privateUse(frame); found {
				t.Errorf("%U is drawn on the frame:\n%s", glyph, frame)
			}
		})
	}

	// Including the labels themselves, which are read in isolation elsewhere.
	for _, label := range []string{
		labelPrevious, labelPlay, labelPause, labelNext,
		labelRepeatOff, labelRepeatOn, labelRepeatOne,
		labelClose, labelHelp, labelKeys, labelLoadMore, labelTypeToSearch,
	} {
		if glyph, found := privateUse(label); found {
			t.Errorf("the label %q carries %U", label, glyph)
		}
	}
}

// The block is coloured by what it says: red for trouble, yellow for a wait,
// blue while the player is on a track, green when there is nothing to report.
// The word and the fill come from one place, so a LOADING that has gone blue is
// not a state this can reach. Where the hues may appear at all is
// TestNothingIsColouredButThePlayer's.
func TestTheStateBlockIsColouredByState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
		word  string
		hue   string
	}{
		{"idle", func(m *Model) {}, "READY", goodBG},
		{"playing", func(m *Model) {
			m.playing = Track{VideoID: "a", Title: "Poly"}
		}, "PLAYING", liveBG},
		{"paused", func(m *Model) {
			m.playing = Track{VideoID: "a", Title: "Poly"}
			m.Paused = true
		}, "PAUSED", liveBG},
		{"loading", func(m *Model) { m.loading = true }, "LOADING", busyBG},
		{"a wait while a track plays", func(m *Model) {
			m.playing = Track{VideoID: "a", Title: "Poly"}
			m.loadingMore = true
		}, "LOADING", busyBG},
		{"trouble", func(m *Model) {
			m.Err = errors.New("no")
		}, "ERROR", alertBG},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sample()
			tc.setup(&m)
			row := strings.Split(m.View().Content, "\n")[m.statusRow()]
			if got := m.statusKey(); got != tc.word {
				t.Fatalf("the block says %q, want %q", got, tc.word)
			}
			if !sgrCodes(row)[tc.hue] {
				t.Errorf("%s is not SGR %s: %v", tc.word, tc.hue, sgrCodes(row))
			}
			// One hue on the row, not two: the other two states' colours are
			// not on it anywhere.
			for _, other := range []string{alertBG, goodBG, busyBG, liveBG} {
				if other != tc.hue && sgrCodes(row)[other] {
					t.Errorf("%s also carries SGR %s", tc.word, other)
				}
			}
			// The fill is the block's own, not the whole row's: the band
			// behind what is playing is still the highlight.
			if !sgrCodes(row)[highlightSGR] {
				t.Errorf("the band lost its fill: %v", sgrCodes(row))
			}
		})
	}
}

// Colour 8 is not a foreground for anything that has to be read or has to
// hold a line. A light theme has to spend it on being a shade of its own page
// — #BDBDBD on #FFFFFF is about 1.8:1 — so dim text is the terminal's own
// faint instead, and borders take the foreground.
//
// Where the terminal will not say what colour it is, colour 8 is the nearest
// the scheme has to a dim anything and everything dim falls back to it. That
// is the one case this does not cover, and it is checked with the page known.
func TestTheDimColourIsNotDrawnAsText(t *testing.T) {
	m := sample()
	// With the page's colour known, every dim line is derived from it — half
	// a step off the page, which is a line. Colour 8 is only what stands in
	// until the terminal answers, and it answers here.
	answered, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}})
	m = answered.(Model)
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

// The state block is as wide as the word in it. It was padded to the longest
// of the five so that nothing moved as the state changed, and what that
// bought was a block with a hole in it most of the time.
func TestTheStateBlockIsAsWideAsItsWord(t *testing.T) {
	m := sample()
	m.Tracks = rows(4)

	widths := map[string]int{}
	for _, tc := range []struct {
		name  string
		setup func(Model) Model
		want  string
	}{
		{"ready", func(m Model) Model { m.playing = Track{}; return m }, "READY"},
		{"playing", func(m Model) Model { m.playing = m.Tracks[0]; return m }, "PLAYING"},
		{"paused", func(m Model) Model {
			m.playing, m.Paused = m.Tracks[0], true
			return m
		}, "PAUSED"},
	} {
		at := tc.setup(m)
		row := plain(strings.Split(at.View().Content, "\n")[at.statusRow()])
		if !strings.Contains(row, tc.want) {
			t.Fatalf("%s: the block does not say %q: %q", tc.name, tc.want, row)
		}
		// The block runs from the start of the row to the end of its word
		// plus its padding; what follows is the wide half.
		end := strings.Index(row, tc.want) + len(tc.want) + 1
		widths[tc.want] = end
		if got := strings.TrimSpace(row[:end]); got != tc.want {
			t.Errorf("%s: the block holds %q, want just the word", tc.name, got)
		}
	}

	if widths["READY"] >= widths["PLAYING"] {
		t.Errorf("READY takes %d and PLAYING %d; it is not sizing to the word",
			widths["READY"], widths["PLAYING"])
	}
}

// The highlight is derived from the page, so a theme change leaves it
// describing a page that is no longer there. There is no notice of one to
// hang a refresh on, so the two moments that usually accompany it re-ask.
func TestTheBackgroundIsAskedForAgainOnFocusAndResize(t *testing.T) {
	m := sample()
	next, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0x00, 0x00, 0x00, 0xFF}})
	m = next.(Model)
	dark := m.highlightColor()

	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{"focus", tea.FocusMsg{}},
		{"resize", tea.WindowSizeMsg{Width: m.width, Height: m.height}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, cmd := m.Update(tc.msg)
			if cmd == nil {
				t.Fatal("it did not ask the terminal anything")
			}
			// The command is the question. Its message is bubbletea's own
			// unexported request type, so the name is all there is to go on;
			// the answer comes back as the exported BackgroundColorMsg.
			if got := fmt.Sprintf("%T", cmd()); !strings.Contains(got, "backgroundColor") {
				t.Fatalf("it asked something else: %s", got)
			}
			lit, _ := next.(Model).Update(
				tea.BackgroundColorMsg{Color: color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}})
			if got := lit.(Model).highlightColor(); got == dark {
				t.Error("the tint did not follow the new page")
			}
		})
	}
}

// And the frame asks the terminal to report focus, or the question above is
// never put.
func TestTheViewAsksForFocusReports(t *testing.T) {
	if !sample().View().ReportFocus {
		t.Error("focus reporting is off, so a theme change goes unnoticed")
	}
}

// The view drawn before the first resize is what the renderer compares the
// next one against, so a mode missing from it is a mode never asked for. The
// mouse was lost that way once already.
func TestTheFirstViewDeclaresEveryMode(t *testing.T) {
	first := New(Services{}).View()
	sized := sample().View()

	if first.AltScreen != sized.AltScreen {
		t.Errorf("alt screen: %v then %v", first.AltScreen, sized.AltScreen)
	}
	if first.MouseMode != sized.MouseMode {
		t.Errorf("mouse mode: %v then %v", first.MouseMode, sized.MouseMode)
	}
	if first.ReportFocus != sized.ReportFocus {
		t.Errorf("focus reporting: %v then %v", first.ReportFocus, sized.ReportFocus)
	}
}

// A tab that is not in front is dim all the way round: its label faint, its
// border a dimmed colour rather than the full foreground.
func TestAnInactiveTabIsDimAllTheWayRound(t *testing.T) {
	m := sample()
	answered, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}})
	m = answered.(Model)
	// Neither of them the liked playlist: that one is magenta whether it is in
	// front or not, which is what TestTheLikedTabIsMagenta is for and would
	// leave nothing here to compare against.
	m.Playlists = []Playlist{{ID: "PL1", Title: "Favorites"}, {ID: "PL2", Title: "Mixes"}}
	m.tabCursor = 0

	rendered := m.renderTabs()
	// The dimmed colour is on the row, as a foreground, and it is derived
	// rather than colour 8 — a border cannot be faint, so it has to be a
	// colour, and the scheme's own dim entry is not a line on a light page.
	r, g, b, _ := m.dimmedColor().RGBA()
	want := fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
	if !strings.Contains(rendered, want) {
		t.Errorf("the tab borders are not the dimmed colour %s:\n%q", want, rendered)
	}
	if sgrCodes(rendered)["90"] {
		t.Errorf("a border fell back to colour 8: %v", sgrCodes(rendered))
	}
	// The label is faint, and the tab in front is neither.
	if !sgrCodes(rendered)[faintSGR] {
		t.Error("no tab is faint")
	}
	if !sgrCodes(rendered)[emphasisFG] {
		t.Error("the tab in front is not emphasised")
	}
}

// With a popover in front, the frame behind it says nothing: no tab is the
// tab in front, no row is selected, no row is playing, and the whole of it
// sinks towards the page. The player is not among them — the transport still
// works with a popover open, so dimming it would be a lie.
func TestTheFrameBehindAPopoverGoesQuiet(t *testing.T) {
	m := sample()
	answered, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}})
	m = answered.(Model)
	m.Tracks = rows(100)
	m.playing, m.Length, m.Position = m.Tracks[2], time.Minute, 20*time.Second
	m.trackCursor = 4
	// Not the liked playlist in front: it carries magenta of its own, and the
	// emphasis is what this checks for.
	m.Playlists = []Playlist{{ID: "PL1", Title: "Favorites"}, {ID: "PL2", Title: "Mixes"}}

	behind := m
	behind.detour = detour{active: true, tracks: m.Tracks,
		tab: Playlist{ID: "MPRE", Title: "Cherry", kind: tabAlbum}}

	r, g, b, _ := m.quietColor().RGBA()
	quiet := fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)

	// The tab row: one tab in front while nothing is over it, the whole row
	// in the quiet colour once something is.
	live, off := m.renderTabs(), behind.renderTabs()
	if !sgrCodes(live)[emphasisFG] {
		t.Fatal("no tab was in front to begin with; this proves nothing")
	}
	if sgrCodes(off)[emphasisFG] {
		t.Error("a tab is still drawn as the one in front")
	}
	if !strings.Contains(off, quiet) {
		t.Errorf("the tabs are not in the quiet colour %s", quiet)
	}

	// The list: nothing selected, nothing playing, all of it quiet.
	liveList := m.renderTracks(m.width, m.bodyHeight())
	offList := behind.renderTracks(m.width, behind.bodyHeight())
	hr, hg, hb, _ := m.highlightColor().RGBA()
	fill := fmt.Sprintf("48;2;%d;%d;%d", hr>>8, hg>>8, hb>>8)
	if !sgrCodes(liveList)[liveFG] {
		t.Fatal("the live list does not mark what is playing; this proves nothing")
	}
	if !strings.Contains(liveList, fill) {
		t.Fatal("the live list does not fill the chosen row; this proves nothing")
	}
	if sgrCodes(offList)[liveFG] {
		t.Error("the list behind still marks what is playing")
	}
	if strings.Contains(offList, fill) {
		t.Error("the list behind still fills the chosen row")
	}
	if !strings.Contains(offList, quiet) {
		t.Errorf("the list behind is not in the quiet colour %s", quiet)
	}

	// The quiet colour really is nearer the page than the dimmed one — that
	// is the whole of what makes it read as switched off.
	qr, _, _, _ := m.quietColor().RGBA()
	dr, _, _, _ := m.dimmedColor().RGBA()
	if qr <= dr {
		t.Errorf("quiet %v is not nearer a white page than dimmed %v",
			m.quietColor(), m.dimmedColor())
	}

	// The player is untouched: it still works, so it still looks like it.
	// Compared by what is on them rather than byte for byte: a frame with a
	// popover on it goes through the compositor, which writes the same cells
	// out with its own escapes.
	liveFrame := strings.Split(m.View().Content, "\n")
	offFrame := strings.Split(behind.View().Content, "\n")
	for _, row := range []int{m.barRow(), m.controlsRow(), m.statusRow()} {
		// Trailing spaces go too: compositing drops them, and an
		// uncomposited frame keeps them.
		trim := func(s string) string { return strings.TrimRight(plain(s), " ") }
		if trim(liveFrame[row]) != trim(offFrame[row]) {
			t.Errorf("row %d changed behind the popover:\n live %q\n then %q",
				row, trim(liveFrame[row]), trim(offFrame[row]))
		}
		if strings.Contains(offFrame[row], quiet) {
			t.Errorf("row %d of the player went quiet: %q", row, offFrame[row])
		}
	}
}

// The liked playlist's tab says whose it is with its label and nothing else. Its
// outline is every other tab's — the emphasis in front, the dimmed colour behind
// — because the outline is already saying which tab is in front, and a second
// thing on the same line reads as an argument.
//
// Which tab is in front is also said by the shape: the front one has no bottom
// edge and the others are closed.
func TestOnlyTheLikedTabsLabelIsMagenta(t *testing.T) {
	m := sample()
	m.Playlists = []Playlist{
		{ID: likedPlaylistID, Title: "Liked Music"},
		{ID: "PL1", Title: "Favorites"},
	}

	for _, tc := range []struct {
		name   string
		cursor int
	}{
		{"in front", 0},
		{"behind another", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := m
			at.tabCursor = tc.cursor
			lines := strings.Split(at.renderTabs(), "\n")
			// The label, and only the label: not the top edge above it, not the
			// walls beside it on the same row, not the feet below it.
			if !sgrCodes(lines[1])[likedFG] {
				t.Errorf("the label is not magenta: %q", lines[1])
			}
			if sgrCodes(lines[0])[likedFG] {
				t.Errorf("the top edge is magenta: %q", lines[0])
			}
			// The walls on the label's own row, and the feet on the rule
			// below it. Not the rule itself: that is the page's, and while
			// this tab is in front the page is this playlist's.
			for row, glyphs := range map[int]string{1: "│", 2: "╯╰┴"} {
				for _, run := range styledRuns(lines[row]) {
					if !strings.Contains(run.codes, likedFG) {
						continue
					}
					if strings.ContainsAny(run.text, glyphs) {
						t.Errorf("magenta reaches the outline %q on row %d: %q",
							run.text, row, lines[row])
					}
				}
			}
			// The shape still says which one is in front: an open bottom edge.
			front := strings.HasPrefix(plain(lines[2]), "╯")
			if front != (tc.cursor == 0) {
				t.Errorf("the bottom edge reads front=%v with the cursor on %d: %q",
					front, tc.cursor, plain(lines[2]))
			}
		})
	}

	// It follows the playlist's id and not its title: a playlist of your own
	// called Liked Music is somebody's playlist, not the liked playlist.
	renamed := m
	renamed.Playlists[0].ID = "PL0"
	if sgrCodes(strings.Split(renamed.renderTabs(), "\n")[1])[likedFG] {
		t.Error("a tab that is not the liked playlist is magenta")
	}

	// And it goes quiet with the rest of the row when something is in front of
	// it: covered, no tab is saying anything.
	for name, cover := range map[string]func(Model) Model{
		"a popover": func(m Model) Model {
			m.detour = detour{active: true, tab: Playlist{Title: "Cherry", kind: tabAlbum}}
			return m
		},
		"the keys sheet": func(m Model) Model { m.sheetOpen = true; return m },
	} {
		if sgrCodes(strings.Split(cover(m).renderTabs(), "\n")[1])[likedFG] {
			t.Errorf("the label is still magenta behind %s", name)
		}
	}
}

// The line above the player takes the liked playlist's magenta while that is
// the tab in front: it closes off a page of that playlist's rows, so it says
// which page the way the tab does.
func TestTheSeparatorFollowsTheLikedTab(t *testing.T) {
	m := sample()
	answered, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}})
	m = answered.(Model)
	m.Playlists = []Playlist{
		{ID: likedPlaylistID, Title: "Liked Music"},
		{ID: "PL1", Title: "Favorites"},
	}
	r, g, b, _ := m.dimmedColor().RGBA()
	dimmed := fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)

	row := func(m Model) string {
		return strings.Split(m.View().Content, "\n")[m.playerTop()]
	}

	m.tabCursor = 0
	front := row(m)
	if !sgrCodes(front)[likedFG] {
		t.Errorf("the separator is not magenta on the liked playlist: %q", front)
	}
	if strings.Contains(front, dimmed) {
		t.Errorf("it is still the dimmed colour as well: %q", front)
	}

	m.tabCursor = 1
	elsewhere := row(m)
	if sgrCodes(elsewhere)[likedFG] {
		t.Errorf("the separator is magenta on another playlist: %q", elsewhere)
	}
	if !strings.Contains(elsewhere, dimmed) {
		t.Errorf("the separator is not the dimmed colour %s: %q", dimmed, elsewhere)
	}

	// Either way it is a line the width of the window, and it is the line
	// directly above the buttons.
	for _, line := range []string{front, elsewhere} {
		if got := lipgloss.Width(plain(line)); got != m.width {
			t.Errorf("the separator is %d cells, want %d", got, m.width)
		}
		if got := strings.TrimRight(plain(line), "─"); got != "" {
			t.Errorf("the separator is not all rule: %q", plain(line))
		}
	}

	// And a popover over the list does not touch it: the player stays live
	// under one, so the line above the player does too.
	m.tabCursor = 0
	m.detour = detour{active: true, tab: Playlist{Title: "Cherry", kind: tabAlbum}}
	if !sgrCodes(row(m))[likedFG] {
		t.Errorf("the separator sank behind a popover: %q", row(m))
	}
}

// styledRuns splits a line into its runs of styling: the SGR parameters in
// force and the plain text drawn under them. Which cells carry a colour is the
// whole question when a line is made of several things.
func styledRuns(line string) []struct{ codes, text string } {
	var out []struct{ codes, text string }
	codes := ""
	for line != "" {
		if at := ansiSequence.FindStringIndex(line); at != nil && at[0] == 0 {
			codes = strings.TrimSuffix(strings.TrimPrefix(line[:at[1]], "\x1b["), "m")
			line = line[at[1]:]
			continue
		}
		next := len(line)
		if at := ansiSequence.FindStringIndex(line); at != nil {
			next = at[0]
		}
		out = append(out, struct{ codes, text string }{codes, line[:next]})
		line = line[next:]
	}
	return out
}

// The rule the tabs sit on goes magenta with the liked playlist while that is
// the tab in front: the line is the top edge of the page below it, and the page
// is one of its rows.
//
// The rule only. Every tab keeps its own two feet in its own colour, which is
// what stops the magenta looking like it has leaked into playlists it has
// nothing to say about — a foot is the glyph that carries a tick up into the
// tab's own walls.
func TestTheRuleTheTabsSitOnFollowsTheLikedTab(t *testing.T) {
	m := sample()
	answered, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}})
	m = answered.(Model)
	m.Playlists = []Playlist{
		{ID: likedPlaylistID, Title: "Liked Music"},
		{ID: "PL1", Title: "Favorites"},
		{ID: "PL2", Title: "Mixes"},
	}
	r, g, b, _ := m.dimmedColor().RGBA()
	dimmed := fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)

	rule := func(m Model) string { return strings.Split(m.renderTabs(), "\n")[2] }
	tops := func(m Model) string { return strings.Split(m.renderTabs(), "\n")[0] }

	// In front: the rule between the feet is magenta, and the feet are not.
	m.tabCursor = 0
	line := rule(m)
	if !sgrCodes(line)[likedFG] {
		t.Fatalf("the rule is not magenta at all: %q", line)
	}
	for _, run := range styledRuns(line) {
		switch {
		case strings.Contains(run.codes, likedFG):
			// The rule, and nothing that belongs to a tab: not even the feet of
			// the playlist whose colour this is.
			if strings.Trim(run.text, "─") != "" {
				t.Errorf("magenta reaches %q, which is not rule: %q", run.text, line)
			}
		case strings.Contains(run.codes, dimmed), strings.Contains(run.codes, emphasisFG):
			// Feet, and nothing else: another tab's, or the one in front.
			if strings.Trim(run.text, "╯╰┴") != "" {
				t.Errorf("a run of feet is %q: %q", run.text, line)
			}
		}
	}
	// The rule really does run between the feet rather than stopping at them.
	if !strings.Contains(line, "\x1b[35m─") {
		t.Errorf("no magenta rule between the feet: %q", line)
	}
	// And the other tabs keep the rest of their outlines.
	if !strings.Contains(tops(m), dimmed) {
		t.Errorf("the other tabs' tops were recoloured too: %q", tops(m))
	}

	// Another tab in front, and the rule is the dimmed colour again.
	m.tabCursor = 1
	if !strings.Contains(rule(m), dimmed) {
		t.Errorf("the rule is not dimmed on another playlist: %q", rule(m))
	}
	// And with it behind another tab there is no magenta on that line at all:
	// the rule is not its page's, and its feet were never its own.
	if sgrCodes(rule(m))[likedFG] {
		t.Errorf("the rule carries magenta with another playlist in front: %q", rule(m))
	}

	// Behind a popover the whole row sinks, the rule with it.
	m.tabCursor = 0
	m.detour = detour{active: true, tab: Playlist{Title: "Cherry", kind: tabAlbum}}
	if sgrCodes(rule(m))[likedFG] {
		t.Errorf("the rule is still magenta behind a popover: %q", rule(m))
	}

	// Colour costs no cells: every row of the tab bar is the window's width.
	m.detour = detour{}
	for _, cursor := range []int{0, 1, 2} {
		m.tabCursor = cursor
		rows := strings.Split(m.renderTabs(), "\n")
		if len(rows) != tabsHeight {
			t.Fatalf("the tab bar is %d rows, want %d", len(rows), tabsHeight)
		}
		for row, text := range rows {
			if got := lipgloss.Width(plain(text)); got != m.width {
				t.Errorf("cursor %d row %d is %d cells, want %d", cursor, row, got, m.width)
			}
		}
	}
}

// The tab whose listing holds the track playing carries a blue block: the same
// block the scrollbar marks that track with, in the same colour, because it is
// the same statement about where the track is.
func TestTheTabHoldingThePlayingTrackIsMarked(t *testing.T) {
	m := sized(sample(), 120, 20)
	m.Playlists = []Playlist{
		{ID: "PL1", Title: "Favorites"},
		{ID: "PL2", Title: "Mixes"},
	}
	m.Tracks = rows(10)
	m.showingID = "PL1"
	// The other tab has been visited, so there are rows there to look through —
	// other rows, so that only one tab can hold a given track.
	others := []Track{{VideoID: "x1", Title: "One"}, {VideoID: "x2", Title: "Two"}}
	m.cache["PL2"] = cached{tracks: others}

	labels := func(m Model) string { return strings.Split(m.renderTabs(), "\n")[1] }

	// Nothing playing, no marker anywhere.
	if strings.Contains(plain(labels(m)), tabMarker) {
		t.Errorf("a marker with nothing playing: %q", plain(labels(m)))
	}

	// Playing a row of the visible listing marks that tab, and only that tab.
	m.playing = m.Tracks[3]
	if !m.tabHoldsPlaying(0) {
		t.Fatal("the tab the track is in does not hold it")
	}
	if m.tabHoldsPlaying(1) {
		t.Error("a tab whose listing does not have it says it does")
	}
	row := labels(m)
	if got := strings.Count(plain(row), tabMarker); got != 1 {
		t.Errorf("%d markers on the row, want one: %q", got, plain(row))
	}
	// In front of the label it belongs to, and in the player's blue.
	if column(plain(row), tabMarker) > column(plain(row), "Favorites") {
		t.Errorf("the marker is not in front of its label: %q", plain(row))
	}
	for _, run := range styledRuns(row) {
		if strings.Contains(run.text, tabMarker) && !strings.Contains(run.codes, liveFG) {
			t.Errorf("the marker is drawn %s, want the player's blue: %q", run.codes, row)
		}
	}

	// It costs the tab two cells, which the spans know about — or a click would
	// land on the tab next door.
	marked := m.tabSpans()
	m.playing = Track{}
	bare := m.tabSpans()
	if marked[0].end-marked[0].start != bare[0].end-bare[0].start+tabMarkerWidth {
		t.Errorf("a marked tab spans %d, unmarked %d",
			marked[0].end-marked[0].start, bare[0].end-bare[0].start)
	}
	if marked[1].start != bare[1].start+tabMarkerWidth {
		t.Errorf("the tab after it starts at %d, want %d",
			marked[1].start, bare[1].start+tabMarkerWidth)
	}

	// A track playing from somewhere else — an album popover, say — is in no
	// tab's listing, so no tab claims it.
	m.playing = Track{VideoID: "elsewhere", Title: "Something"}
	if strings.Contains(plain(labels(m)), tabMarker) {
		t.Errorf("a tab claimed a track it does not list: %q", plain(labels(m)))
	}

	// A track in two loaded listings marks both: they both do hold it.
	m.playing = m.Tracks[0]
	m.cache["PL2"] = cached{tracks: append(others, m.Tracks[0])}
	if got := strings.Count(plain(labels(m)), tabMarker); got != 2 {
		t.Errorf("%d markers, want one on each tab that lists it: %q", got, plain(labels(m)))
	}

	// Behind a popover it sinks with the rest of the row.
	m.detour = detour{active: true, tab: Playlist{Title: "Cherry", kind: tabAlbum}}
	if sgrCodes(labels(m))[liveFG] {
		t.Errorf("the marker is still lit behind a popover: %q", labels(m))
	}
	if !strings.Contains(plain(labels(m)), tabMarker) {
		t.Error("the marker lost its place as well as its colour")
	}
}
