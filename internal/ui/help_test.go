package ui

import (
	"fmt"
	"image/color"
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// sized is a model at a given window size, which most of these need: the
// sheet is as wide as the keys ask for and a narrow window makes bubbles drop
// whole columns of them.
func sized(m Model, width, height int) Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(Model)
}

// roomy is a window with room for every column.
func roomy(t *testing.T) Model {
	t.Helper()
	return sized(sample(), 120, 40)
}

// statusLine is the row the state block and the ? sit on.
func statusLine(m Model) string {
	return strings.Split(m.View().Content, "\n")[m.statusRow()]
}

// The way in is a ? at the far end of the bar, on the same fill as what is
// playing — it is part of that band rather than something dropped on top of
// it — and it answers to a click on the character itself.
func TestTheKeysButtonSitsAtTheEndOfTheBar(t *testing.T) {
	m := roomy(t)
	m.playing = Track{VideoID: "a", Title: "Poly", Album: "Cherry"}

	row := statusLine(m)
	if got := lipgloss.Width(plain(row)); got != m.width {
		t.Fatalf("the bar is %d cells, want %d", got, m.width)
	}
	start, ok := m.helpButtonSpan()
	if !ok {
		t.Fatal("no room for the button on a 120 cell row")
	}
	if got := column(plain(row), labelHelp); got != start {
		t.Errorf("the ? is at column %d, want %d", got, start)
	}
	// One cell of air past it, the way the other end of the band has one.
	if start+helpButtonWidth != m.width-1 {
		t.Errorf("the ? ends at %d, want one cell short of %d",
			start+helpButtonWidth, m.width)
	}

	// The band's fill runs under it: the ? is on the same surface as the
	// track, not on a hole in it.
	if gap := unfilled(row); gap != "" {
		t.Errorf("part of the bar has no fill: %q", gap)
	}

	// And the click lands on the character, not beside it.
	if where, _ := m.hit(start, m.statusRow()); where != regionHelp {
		t.Errorf("the ? is not clickable at %d: region %v", start, where)
	}
	for _, x := range []int{start - 1, start + helpButtonWidth} {
		if where, _ := m.hit(x, m.statusRow()); where == regionHelp {
			t.Errorf("column %d answers as the button, which does not draw there", x)
		}
	}
}

// It is a button like the rest, so it says when it is on.
func TestTheKeysButtonOpensAndClosesTheSheet(t *testing.T) {
	m := roomy(t)
	start, _ := m.helpButtonSpan()

	next, _ := m.Update(click(start, m.statusRow()))
	m = next.(Model)
	if !m.sheetOpen {
		t.Fatal("clicking the ? did not open the sheet")
	}
	if !strings.Contains(plain(m.View().Content), labelKeys) {
		t.Error("the sheet is open but not drawn")
	}

	// Again closes it: it is a toggle, and the ? is still where it was.
	next, _ = m.Update(click(start, m.statusRow()))
	if next.(Model).sheetOpen {
		t.Error("clicking the ? again did not close the sheet")
	}

	// The key does the same thing from either state.
	if !press(roomy(t), "?").sheetOpen {
		t.Error("? did not open the sheet")
	}
	if press(press(roomy(t), "?"), "?").sheetOpen {
		t.Error("? did not close the sheet again")
	}
}

// bindings is every field of the keymap, by name. Reflection rather than a
// list, because a list is the thing this test exists to make unnecessary.
func bindings(t *testing.T) map[string]key.Binding {
	t.Helper()
	v := reflect.ValueOf(appKeys)
	out := make(map[string]key.Binding, v.NumField())
	for i := range v.NumField() {
		b, ok := v.Field(i).Interface().(key.Binding)
		if !ok {
			t.Fatalf("%s is not a key.Binding", v.Type().Field(i).Name)
		}
		out[v.Type().Field(i).Name] = b
	}
	if len(out) == 0 {
		t.Fatal("the keymap has no fields; this proves nothing")
	}
	return out
}

// Every shortcut the app has is on the sheet, and every one of them says what
// it does. A binding added to the keymap and left out of a column would be a
// key nothing tells you about, which is the whole thing this is for.
func TestTheSheetListsEveryBinding(t *testing.T) {
	m := roomy(t)
	m.sheetOpen = true
	frame := plain(m.View().Content)

	var listed []key.Binding
	for _, column := range appKeys.FullHelp() {
		listed = append(listed, column...)
	}

	for name, b := range bindings(t) {
		if !b.Enabled() {
			t.Errorf("%s binds no keys", name)
			continue
		}
		help := b.Help()
		if help.Key == "" || help.Desc == "" {
			t.Errorf("%s has no help: %+v", name, help)
		}
		found := false
		for _, in := range listed {
			if in.Help() == help {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is in no column of the sheet", name)
		}
		if !strings.Contains(frame, help.Key) {
			t.Errorf("%s's key %q is not on the sheet", name, help.Key)
		}
		if !strings.Contains(frame, help.Desc) {
			t.Errorf("%s reads %q on the sheet, which is not there", name, help.Desc)
		}
	}

	// Nothing is listed twice, in two columns or in one.
	seen := map[key.Help]string{}
	for _, in := range listed {
		if where, again := seen[in.Help()]; again {
			t.Errorf("%q is listed twice (%s)", in.Help().Key, where)
		}
		seen[in.Help()] = in.Help().Desc
	}
}

// It is a popover, so it closes the way they all do: the key, the button, or
// clicking away from it. Clicking it is not clicking away.
func TestTheSheetClosesEveryWayAPopoverDoes(t *testing.T) {
	open := func(t *testing.T) Model {
		t.Helper()
		m := roomy(t)
		m.sheetOpen = true
		return m
	}

	if press(open(t), "esc").sheetOpen {
		t.Error("esc did not close the sheet")
	}

	m := open(t)
	x, y, width, ok := m.sheetCloseButton()
	if !ok {
		t.Fatal("the sheet has no close button")
	}
	if !strings.Contains(plain(m.View().Content), labelClose) {
		t.Error("the sheet does not draw the way out it hit-tests")
	}
	for _, at := range []int{x, x + width - 1} {
		next, _ := open(t).Update(click(at, y))
		if next.(Model).sheetOpen {
			t.Errorf("clicking the way out at %d left the sheet open", at)
		}
	}

	// Inside it but not on the button: nothing happens. A sheet is not
	// dismissed by being read.
	sx, sy, _, _ := m.sheetBounds()
	next, _ := m.Update(click(sx+1, sy+3))
	if !next.(Model).sheetOpen {
		t.Error("clicking the sheet itself closed it")
	}

	// Away from it, over the list: closed.
	next, _ = m.Update(click(0, tabsHeight))
	if next.(Model).sheetOpen {
		t.Error("clicking away left the sheet open")
	}
}

// The transport works while the sheet is up, for the same reason it works
// under a popover: pausing should not depend on what you are reading. The list
// underneath does not, because it is behind the thing you are reading.
func TestTheTransportWorksBehindTheSheet(t *testing.T) {
	m, _, _, au := playingModel(t)
	m.sheetOpen = true
	m.trackCursor = 2

	next, cmd := m.Update(keyPress(" "))
	m = drain(t, next.(Model), cmd)
	if au.toggles != 1 {
		t.Errorf("space behind the sheet toggled %d times, want 1", au.toggles)
	}
	if !m.sheetOpen {
		t.Error("the transport key also closed the sheet")
	}

	for _, k := range []string{"j", "k", "g", "G", "/", "s"} {
		if at := press(m, k); at.trackCursor != m.trackCursor || at.detour.active {
			t.Errorf("%q reached the list behind the sheet: cursor %d, popover %v",
				k, at.trackCursor, at.detour.active)
		}
	}
}

// Quit still quits, and still saves what it was playing on the way out: the
// sheet lets that one through rather than answering it.
func TestQuitWorksBehindTheSheet(t *testing.T) {
	m := roomy(t)
	m.sheetOpen = true
	_, cmd := m.Update(keyPress("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c behind the sheet did nothing")
	}
	if msg := cmd(); msg != tea.QuitMsg(struct{}{}) {
		t.Errorf("ctrl+c produced %#v, want a quit", msg)
	}
}

// It is in front of a popover as well, and opens from inside one: what the app
// does does not change with what you have open, so neither does where the keys
// for it are read.
func TestTheSheetIsInFrontOfAPopover(t *testing.T) {
	m := roomy(t)
	m.detour = detour{active: true, tracks: rows(20),
		tab: Playlist{ID: "MPRE", Title: "Cherry", kind: tabAlbum}}

	opened := press(m, "?")
	if !opened.sheetOpen {
		t.Fatal("? inside a popover did not open the sheet")
	}
	if !opened.detour.active {
		t.Error("opening the sheet closed the popover under it")
	}

	frame := strings.Split(plain(opened.View().Content), "\n")
	_, y, _, _ := opened.sheetBounds()
	if !strings.Contains(frame[y+1], labelKeys) {
		t.Errorf("the popover is drawn over the sheet's title: %q", frame[y+1])
	}
	if !strings.Contains(frame[y+1], labelClose) {
		t.Errorf("the sheet's way out is covered: %q", frame[y+1])
	}
	if !strings.Contains(plain(opened.View().Content), appKeys.PlayPause.Help().Desc) {
		t.Error("the keys themselves are covered by the popover")
	}

	// And closing it leaves the popover where it was.
	shut := press(opened, "esc")
	if shut.sheetOpen {
		t.Error("esc did not close the sheet")
	}
	if !shut.detour.active {
		t.Error("esc closed the popover as well as the sheet")
	}
}

// The frame behind the sheet goes quiet the way it does behind a popover:
// something is in front of the whole of it, so nothing it usually says — this
// tab is in front, this row is selected — is being said to anyone.
func TestTheFrameBehindTheSheetGoesQuiet(t *testing.T) {
	m := roomy(t)
	answered, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}})
	m = answered.(Model)
	m.Tracks = rows(100)
	m.playing, m.Length, m.Position = m.Tracks[2], time.Minute, 20*time.Second
	m.trackCursor = 4

	behind := m
	behind.sheetOpen = true
	if !behind.covered() {
		t.Fatal("the sheet does not count as covering the frame")
	}

	r, g, b, _ := m.quietColor().RGBA()
	quiet := fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)

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

	liveList := m.renderTracks(m.width, m.bodyHeight())
	offList := behind.renderTracks(behind.width, behind.bodyHeight())
	if !sgrCodes(liveList)[liveFG] {
		t.Fatal("the live list does not mark what is playing; this proves nothing")
	}
	if sgrCodes(offList)[liveFG] {
		t.Error("the list behind still marks what is playing")
	}
	if !strings.Contains(offList, quiet) {
		t.Errorf("the list behind is not in the quiet colour %s", quiet)
	}
}

// Where it fits, it sits over the list and leaves the player alone — the same
// place a popover goes. The transport is still there to work.
func TestTheSheetSitsOverTheList(t *testing.T) {
	m := roomy(t)
	m.sheetOpen = true
	x, y, width, height := m.sheetBounds()

	if y < tabsHeight {
		t.Errorf("the sheet starts at row %d, on the tabs", y)
	}
	if y+height > m.playerTop() {
		t.Errorf("the sheet ends at row %d, on the player at %d", y+height, m.playerTop())
	}
	// Centred, within a cell either way.
	if got := (m.width - width) / 2; x != got {
		t.Errorf("the sheet is at column %d, want %d", x, got)
	}

	// The player's own rows come out the same with it open as without.
	// Compared by what is on them: a composited frame writes the same cells
	// with its own escapes, and drops trailing spaces.
	trim := func(s string) string { return strings.TrimRight(plain(s), " ") }
	live := strings.Split(sized(sample(), 120, 40).View().Content, "\n")
	off := strings.Split(m.View().Content, "\n")
	for _, row := range []int{m.controlsRow(), m.barRow()} {
		if trim(live[row]) != trim(off[row]) {
			t.Errorf("row %d changed with the sheet open:\n%q\n%q",
				row, trim(live[row]), trim(off[row]))
		}
	}
}

// The sheet is content-sized, so it is the one thing on screen that could
// outgrow the window. It does not: it stays inside it at every size, and the
// frame is the same shape open as closed. Every mouse coordinate in the app is
// counted off that frame, so a row added by opening the sheet would move the
// list out from under the pointer.
func TestTheSheetNeverOutgrowsTheWindow(t *testing.T) {
	for _, size := range [][2]int{{200, 60}, {120, 40}, {100, 20}, {80, 24}, {60, 15}, {40, 10}, {24, 8}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			shut := sized(sample(), size[0], size[1])
			m := shut
			m.sheetOpen = true

			x, y, width, height := m.sheetBounds()
			if x < 0 || y < 0 {
				t.Errorf("the sheet starts off screen at %d,%d", x, y)
			}
			if x+width > m.width {
				t.Errorf("the sheet is %d cells wide at column %d, past %d",
					width, x, m.width)
			}
			if y+height > m.height {
				t.Errorf("the sheet is %d rows tall at row %d, past %d",
					height, y, m.height)
			}

			// Against the same window with it shut, not against the window:
			// a window too short for the tabs, a row of list and the player
			// is already taller than itself before the sheet is opened.
			lines := strings.Split(m.View().Content, "\n")
			if want := len(strings.Split(shut.View().Content, "\n")); len(lines) != want {
				t.Errorf("the frame is %d rows with the sheet open, %d without",
					len(lines), want)
			}
			for row, line := range lines {
				if got := lipgloss.Width(plain(line)); got > m.width {
					t.Errorf("row %d is %d cells, past %d", row, got, m.width)
				}
			}
		})
	}
}

// A narrow window loses the ? rather than the track: the band says what is
// playing first, and the keys are still a keystroke away.
func TestTheKeysButtonGivesWayOnANarrowRow(t *testing.T) {
	m := sized(sample(), 9, 20) // "READY" and its padding leave nothing over
	if _, ok := m.helpButtonSpan(); ok {
		t.Error("the ? kept its place on a row with no room for it")
	}
	row := statusLine(m)
	if strings.Contains(plain(row), labelHelp) {
		t.Errorf("the ? is drawn where it does not fit: %q", plain(row))
	}
	if got := lipgloss.Width(plain(row)); got != m.width {
		t.Errorf("the bar is %d cells, want %d", got, m.width)
	}
	if press(m, "?").sheetOpen != true {
		t.Error("the key stopped working with the button gone")
	}
}

// The sheet's own colours come from the scheme, not from bubbles' defaults,
// which are a fixed pair of hex greys against a page that is not fixed.
func TestTheSheetTakesItsColoursFromTheScheme(t *testing.T) {
	m := roomy(t)
	m.sheetOpen = true
	frame := m.View().Content

	for _, hex := range []string{"909090", "626262", "B2B2B2", "4A4A4A", "DADADA", "3C3C3C"} {
		if strings.Contains(frame, hex) {
			t.Errorf("bubbles' default grey #%s reached the frame", hex)
		}
	}
	// The keys are the emphasised end of the foreground, and bold with it.
	codes := sgrCodes(frame)
	if !codes[emphasisFG] || !codes["1"] {
		t.Errorf("the keys are not drawn as emphasis: %v", codes)
	}
}
