package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cllpse/youtuimusic/internal/player"
	"github.com/cllpse/youtuimusic/internal/ytm"
)

// playingModel is wired up with a track already playing, which is the state
// most of the controls only mean something in.
func playingModel(t *testing.T) (Model, *fakeLibrary, *fakeStreams, *fakeAudio) {
	t.Helper()
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.playing = m.Tracks[0]
	m.Length = 3 * time.Minute
	return m, lib, st, au
}

// controlsLine is the row the labels sit on, which is the middle of however
// many rows the buttons take.
func controlsLine(m Model) string {
	return strings.Split(m.View().Content, "\n")[m.controlsRow()+controlsRows/2]
}

// controlsBlock is all three rows of them.
func controlsBlock(m Model) []string {
	lines := strings.Split(m.View().Content, "\n")
	return lines[m.controlsRow() : m.controlsRow()+controlsRows]
}

// buttonAt finds a control in the laid-out row.
func buttonAt(m Model, c control) (button, bool) {
	for _, b := range m.controlButtons() {
		if b.control == c {
			return b, true
		}
	}
	return button{}, false
}

// Two groups: the transport with repeat on the end against the left, the
// ratings against the right.
func TestControlsSitLeftAndRight(t *testing.T) {
	m, _, _, _ := playingModel(t)
	row := plain(controlsLine(m))

	// The rendered row includes the box's own border columns.
	if got := lipgloss.Width(row); got != m.width {
		t.Fatalf("the controls row is %d cells, want %d", got, m.width)
	}

	// Against the left edge of the box, in order, repeat last. The buttons
	// are their labels wide, so where each starts depends on the last.
	at := contentLeft
	for _, c := range []control{
		controlPrevious, controlPlayPause, controlNext, controlRepeat,
	} {
		b, ok := buttonAt(m, c)
		if !ok {
			t.Fatalf("control %v is missing", c)
		}
		if b.start != at {
			t.Errorf("control %v starts at %d, want %d", c, b.start, at)
		}
		if got := b.end - b.start; got != buttonWidth(b.label) {
			t.Errorf("control %v spans %d, want %d for %q",
				c, got, buttonWidth(b.label), b.label)
		}
		at = b.end + buttonGap
	}

	// The ratings end against the inside of the right border, in order, and
	// clear of the transport.
	up, _ := buttonAt(m, controlThumbUp)
	down, _ := buttonAt(m, controlThumbDown)
	if want := contentLeft + m.contentWidth(); down.end != want {
		t.Errorf("dislike ends at %d, want the inside of the border %d", down.end, want)
	}
	if want := down.start - buttonGap; up.end != want {
		t.Errorf("like ends at %d, want %d", up.end, want)
	}
	rep, _ := buttonAt(m, controlRepeat)
	if up.start < rep.end+buttonGap {
		t.Errorf("the ratings run into the transport: %d < %d",
			up.start, rep.end+buttonGap)
	}

	// And they are where the row actually draws them. The label is compared
	// trimmed: the repeat button pads its label to the width of the longest,
	// so what is drawn carries the padding too.
	for _, b := range m.controlButtons() {
		drawn := row[colToByte(row, b.start):colToByte(row, b.end)]
		if !strings.Contains(drawn, strings.TrimSpace(b.label)) {
			t.Errorf("control %v is not drawn in its own columns %d-%d: %q",
				b.control, b.start, b.end, drawn)
		}
	}
}

// colToByte converts a column into a byte offset, since the icons are
// multi-byte.
func colToByte(s string, col int) int {
	seen := 0
	for i, r := range s {
		if seen == col {
			return i
		}
		seen += lipgloss.Width(string(r))
	}
	return len(s)
}

// The label says what pressing it will do, which is Pause while it plays.
func TestThePlayPauseLabelFollowsTheState(t *testing.T) {
	m, _, _, _ := playingModel(t)
	for _, tc := range []struct {
		name  string
		setup func(Model) Model
		want  string
	}{
		{"playing", func(m Model) Model { return m }, labelPause},
		{"paused", func(m Model) Model { m.Paused = true; return m }, labelPlay},
		{"idle", func(m Model) Model { m.playing = Track{}; return m }, labelPlay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, ok := buttonAt(tc.setup(m), controlPlayPause)
			if !ok {
				t.Fatal("the play button is missing")
			}
			if got := strings.TrimSpace(b.label); got != tc.want {
				t.Errorf("it says %q, want %q", got, tc.want)
			}
		})
	}

	// Both words are drawn at the same width, so the row does not shuffle
	// when what is playing pauses.
	playing, _ := buttonAt(m, controlPlayPause)
	stopped := m
	stopped.Paused = true
	paused, _ := buttonAt(stopped, controlPlayPause)
	if a, b := buttonWidth(playing.label), buttonWidth(paused.label); a != b {
		t.Errorf("Pause is %d wide and Play %d", a, b)
	}
}

// The rating shows in whether the button is filled, not in its label.
func TestTheRatingLitsItsButton(t *testing.T) {
	m, _, _, _ := playingModel(t)

	up, _ := buttonAt(m, controlThumbUp)
	down, _ := buttonAt(m, controlThumbDown)
	if strings.TrimSpace(up.label) != labelLike ||
		strings.TrimSpace(down.label) != labelDislike {
		t.Errorf("the labels are %q/%q", up.label, down.label)
	}
	if up.state == buttonActive || down.state == buttonActive {
		t.Error("an unrated track makes a rating active")
	}

	m.playing.Rating = RatingUp
	up, _ = buttonAt(m, controlThumbUp)
	down, _ = buttonAt(m, controlThumbDown)
	if up.state != buttonActive {
		t.Errorf("rated up left Like %v", up.state)
	}
	if down.state == buttonActive {
		t.Error("rated up made Dislike active too")
	}

	m.playing.Rating = RatingDown
	up, _ = buttonAt(m, controlThumbUp)
	down, _ = buttonAt(m, controlThumbDown)
	if down.state != buttonActive || up.state == buttonActive {
		t.Errorf("rated down: like %v, dislike %v", up.state, down.state)
	}
}

func TestRepeatCyclesThroughItsThreeStates(t *testing.T) {
	m, _, _, _ := playingModel(t)
	want := []struct {
		state Repeat
		label string
		lit   bool
	}{
		{RepeatAll, labelRepeatOn, true},
		{RepeatOne, labelRepeatOne, true},
		{RepeatOff, labelRepeatOff, false},
	}
	// Repeat off is a state it is in, not a thing it cannot do, so it is the
	// default rather than disabled.
	if b, _ := buttonAt(m, controlRepeat); !strings.Contains(b.label, labelRepeatOff) ||
		b.state != buttonDefault {
		t.Fatalf("starts at %q state=%v, want repeat-off as the default", b.label, b.state)
	}
	for _, step := range want {
		next, _ := m.Update(keyPress("r"))
		m = next.(Model)
		if m.repeat != step.state {
			t.Fatalf("repeat = %v, want %v", m.repeat, step.state)
		}
		b, _ := buttonAt(m, controlRepeat)
		want := buttonDefault
		if step.lit {
			want = buttonActive
		}
		if !strings.Contains(b.label, step.label) || b.state != want {
			t.Errorf("%v shows %q state=%v, want %q as %v",
				step.state, b.label, b.state, step.label, want)
		}
		// Every state is drawn at the same width, so the button does not
		// change size under the pointer as it cycles.
		if got := buttonWidth(b.label); got != buttonWidth(m.repeat.label()) {
			t.Errorf("%v is %d wide", step.state, got)
		}
		if got := lipgloss.Width(b.label); got != widestRepeatLabel {
			t.Errorf("%v pads to %d, want %d", step.state, got, widestRepeatLabel)
		}
	}
}

// Repeat decides what happens when mpv reports the end of a track.
func TestRepeatDecidesWhatPlaysNext(t *testing.T) {
	for _, tc := range []struct {
		name    string
		repeat  Repeat
		playing int // index in a two-track list
		want    string
	}{
		{"off, mid-list", RepeatOff, 0, "b"},
		{"off, at the end", RepeatOff, 1, ""},
		{"all, at the end wraps", RepeatAll, 1, "a"},
		{"one replays the same", RepeatOne, 0, "a"},
		{"one at the end still replays", RepeatOne, 1, "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib, st := library(), &fakeStreams{}
			au := newFakeAudio(player.Event{Name: player.EndFile, Data: "eof"})
			m := wired(t, lib, st, au)
			m.Tracks = fromAPI(lib.tracks["LM"])
			m.playing, m.repeat = m.Tracks[tc.playing], tc.repeat

			m = drain(t, m, m.watchEvents())

			if tc.want == "" {
				if len(st.resolved) != 0 {
					t.Fatalf("resolved %v, want nothing to follow", st.resolved)
				}
				return
			}
			if len(st.resolved) == 0 || st.resolved[0] != tc.want {
				t.Fatalf("resolved %v, want %q", st.resolved, tc.want)
			}
		})
	}
}

func TestPreviousGoesBackAndStopsAtTheStart(t *testing.T) {
	m, _, st, _ := playingModel(t)
	m.playing = m.Tracks[1]

	next, cmd := m.Update(keyPress("p"))
	m = drain(t, next.(Model), cmd)
	if len(st.resolved) != 1 || st.resolved[0] != "a" {
		t.Fatalf("resolved %v, want the earlier track", st.resolved)
	}

	// Already at the start, and not wrapping unless repeat says to.
	next, cmd = m.Update(keyPress("p"))
	m = drain(t, next.(Model), cmd)
	if len(st.resolved) != 1 {
		t.Errorf("resolved %v; there is nothing before the first track", st.resolved)
	}

	m.repeat = RepeatAll
	next, cmd = m.Update(keyPress("p"))
	drain(t, next.(Model), cmd)
	if len(st.resolved) != 2 || st.resolved[1] != "b" {
		t.Errorf("resolved %v, want a wrap to the last track", st.resolved)
	}
}

func TestClickingTheControls(t *testing.T) {
	for _, tc := range []struct {
		name  string
		which control
		check func(t *testing.T, m Model, st *fakeStreams, au *fakeAudio, lib *fakeLibrary)
	}{
		{"next", controlNext, func(t *testing.T, m Model, st *fakeStreams, _ *fakeAudio, _ *fakeLibrary) {
			if len(st.resolved) != 1 || st.resolved[0] != "b" {
				t.Errorf("resolved %v", st.resolved)
			}
		}},
		{"play/pause", controlPlayPause, func(t *testing.T, _ Model, _ *fakeStreams, au *fakeAudio, _ *fakeLibrary) {
			if au.toggles != 1 {
				t.Errorf("toggles = %d", au.toggles)
			}
		}},
		{"thumb up", controlThumbUp, func(t *testing.T, m Model, _ *fakeStreams, _ *fakeAudio, lib *fakeLibrary) {
			if len(lib.rated) != 1 || lib.rated[0].videoID != "a" {
				t.Errorf("rated %+v", lib.rated)
			}
			if m.playing.Rating != RatingUp {
				t.Errorf("playing rating = %v", m.playing.Rating)
			}
		}},
		{"repeat", controlRepeat, func(t *testing.T, m Model, _ *fakeStreams, _ *fakeAudio, _ *fakeLibrary) {
			if m.repeat != RepeatAll {
				t.Errorf("repeat = %v", m.repeat)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, lib, st, au := playingModel(t)
			b, ok := buttonAt(m, tc.which)
			if !ok {
				t.Fatalf("control %v is not on the row", tc.which)
			}
			next, cmd := m.Update(click(b.start+1, m.controlsRow()))
			m = drain(t, next.(Model), cmd)
			tc.check(t, m, st, au, lib)
		})
	}
}

// Every column of every button has to answer, or the edges are dead.
func TestControlsHitTestingCoversEveryColumn(t *testing.T) {
	m, _, _, _ := playingModel(t)
	for _, b := range m.controlButtons() {
		for x := b.start; x < b.end; x++ {
			where, n := m.hit(x, m.controlsRow())
			if where != regionControls || control(n) != b.control {
				t.Errorf("column %d of %v gave %v, %d", x, b.control, where, n)
			}
		}
	}
	// And the space between two groups is not a button. Find a real gap
	// rather than guessing a column.
	transport, _ := buttonAt(m, controlNext)
	thumbs, _ := buttonAt(m, controlThumbUp)
	if thumbs.start <= transport.end {
		t.Fatal("no gap between the groups to test")
	}
	for _, x := range []int{transport.end, thumbs.start - 1} {
		if where, n := m.hit(x, m.controlsRow()); where != regionNone {
			t.Errorf("the gap at column %d answered %v, %d", x, where, n)
		}
	}
	// Past the last button is not a button either. There is no border to
	// test any more: the row runs edge to edge, and the first button starts
	// at the first column.
	last, _ := buttonAt(m, controlThumbDown)
	if last.end < m.width {
		if where, _ := m.hit(m.width-1, m.controlsRow()); where != regionNone {
			t.Errorf("the last column answered %v", where)
		}
	}
}

// The thumbs are beside the transport, so they rate what plays — not
// whatever row the cursor happens to be on.
func TestThumbsRateThePlayingTrackNotTheSelection(t *testing.T) {
	m, lib, _, _ := playingModel(t)
	m.trackCursor = 1 // highlight the other track

	b, _ := buttonAt(m, controlThumbUp)
	next, cmd := m.Update(click(b.start+1, m.controlsRow()))
	m = drain(t, next.(Model), cmd)

	if len(lib.rated) != 1 || lib.rated[0].videoID != "a" {
		t.Fatalf("rated %+v, want the playing track", lib.rated)
	}
	if m.Tracks[1].Rating != RatingNone {
		t.Error("the highlighted row was rated instead")
	}
	// The key still rates the selection, which is the other half of the pair.
	next, cmd = m.Update(keyPress("+"))
	m = drain(t, next.(Model), cmd)
	if len(lib.rated) != 2 || lib.rated[1].videoID != "b" {
		t.Fatalf("rated %+v, want the highlighted row", lib.rated)
	}
}

// A rating clears on a second press, through the controls as through the keys.
func TestTheThumbControlTogglesOff(t *testing.T) {
	m, lib, _, _ := playingModel(t)
	b, _ := buttonAt(m, controlThumbUp)

	for range 2 {
		next, cmd := m.Update(click(b.start+1, m.controlsRow()))
		m = drain(t, next.(Model), cmd)
	}
	if len(lib.rated) != 2 || lib.rated[1].rating != ytm.RatingNone {
		t.Fatalf("rated %+v", lib.rated)
	}
	if m.playing.Rating != RatingNone {
		t.Errorf("rating = %v, want cleared", m.playing.Rating)
	}
}

// With nothing playing, skipping has nothing to skip from and says so. Play
// does not: it starts the row under the cursor, which is something to do.
func TestTheTransportIsDisabledWithNothingPlaying(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	for _, tc := range []struct {
		control control
		want    buttonState
	}{
		{controlPrevious, buttonDisabled},
		{controlNext, buttonDisabled},
		{controlPlayPause, buttonDefault},
		{controlThumbUp, buttonDisabled},
		{controlThumbDown, buttonDisabled},
	} {
		b, ok := buttonAt(m, tc.control)
		if !ok {
			t.Fatalf("control %v is missing", tc.control)
		}
		if b.state != tc.want {
			t.Errorf("control %v is %v with nothing playing, want %v",
				tc.control, b.state, tc.want)
		}
	}
}

// A row too narrow for everything drops groups rather than overlapping them.
func TestNarrowRowsDegradeCleanly(t *testing.T) {
	for _, width := range []int{4, 8, 9, 12, 20, 40} {
		m := New(Services{})
		sized, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		m = sized.(Model)

		last := -1
		for _, b := range m.controlButtons() {
			if b.start < last {
				t.Errorf("width %d: %v overlaps its neighbour", width, b.control)
			}
			if b.end > width {
				t.Errorf("width %d: %v runs to %d, past the edge", width, b.control, b.end)
			}
			last = b.end
		}
		if got := lipgloss.Width(plain(controlsLine(m))); got != width {
			t.Errorf("width %d: the row renders %d cells", width, got)
		}
		for _, b := range m.controlButtons() {
			if b.start < contentLeft {
				t.Errorf("width %d: %v starts on the border", width, b.control)
			}
		}
	}
}

// mpv ends the previous file whenever a replacement is loaded, reporting
// "stop". Treating that as the end of a track would advance again, and
// again, straight through the playlist.
func TestOnlyARealEndAdvances(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	au := newFakeAudio(player.Event{Name: player.EndFile, Data: "stop"})
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.playing = m.Tracks[0]

	m = drain(t, m, m.watchEvents())

	if len(st.resolved) != 0 {
		t.Fatalf("resolved %v after a stop; only eof should advance", st.resolved)
	}
}

// Liking a track puts it in Liked Music, so what is held for that tab stops
// describing it.
func TestRatingRefreshesLikedMusic(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	lib.tracks[likedPlaylistID] = []ytm.Track{{VideoID: "a", Title: "Alpha", Artist: "A"}}
	m := wired(t, lib, st, au)
	m.Playlists = []Playlist{{ID: "PL1", Title: "Favorites"}, {ID: likedPlaylistID, Title: "Liked Music"}}

	// Visit Liked Music so it is cached, then go back to the other tab.
	for _, key := range []string{"l", "h"} {
		next, cmd := m.Update(keyPress(key))
		m = drain(t, next.(Model), cmd)
	}
	if _, ok := m.cache[likedPlaylistID]; !ok {
		t.Fatal("Liked Music was not cached by visiting it")
	}

	m.Tracks = fromAPI(lib.tracks["LM"])
	m.playing = m.Tracks[0]
	b, _ := buttonAt(m, controlThumbUp)
	next, cmd := m.Update(click(b.start+1, m.controlsRow()))
	m = drain(t, next.(Model), cmd)

	if _, ok := m.cache[likedPlaylistID]; ok {
		t.Error("Liked Music is still cached from before the rating")
	}
}

// Sitting on Liked Music, a rating reloads it in place rather than waiting
// for the next visit — and does not throw away where the reader was.
func TestRatingWhileOnLikedMusicReloadsIt(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	lib.tracks[likedPlaylistID] = fromUI(rows(40))
	m := wired(t, lib, st, au)
	m.Playlists = []Playlist{{ID: likedPlaylistID, Title: "Liked Music"}}
	opened, openCmd := m.showTab()
	m = drain(t, opened, openCmd)

	m.trackCursor = 25
	m.scroll()
	before := m.trackOffset
	m.playing = m.Tracks[25]

	requests := len(lib.askedFor)
	// Rated through the control rather than by clicking it: the liked
	// playlist does not carry the thumbs, since every row there is liked.
	next, cmd := m.press(controlThumbUp)
	m = drain(t, next.(Model), cmd)

	if len(lib.askedFor) != requests+1 {
		t.Errorf("asked for %v; want one more fetch of Liked Music", lib.askedFor)
	}
	if m.trackCursor != 25 || m.trackOffset != before {
		t.Errorf("the reload jumped to cursor %d offset %d", m.trackCursor, m.trackOffset)
	}
	// The server can still describe the track the old way; ours wins.
	if m.Tracks[25].Rating != RatingUp {
		t.Errorf("the rating was lost in the reload: %v", m.Tracks[25].Rating)
	}
}

// fromUI is the inverse of fromAPI, for building fixtures.
func fromUI(ts []Track) []ytm.Track {
	out := make([]ytm.Track, 0, len(ts))
	for _, t := range ts {
		out = append(out, ytm.Track{VideoID: t.VideoID, Title: t.Title, Artist: t.Artist})
	}
	return out
}

// The fill is part of the button: pressing it has to do the same thing as
// pressing the icon, or the target is smaller than it looks.
func TestTheWholeButtonIsClickable(t *testing.T) {
	for _, offset := range []int{0, 1, 2} {
		m, _, st, _ := playingModel(t)
		b, ok := buttonAt(m, controlNext)
		if !ok {
			t.Fatal("no next button")
		}
		next, cmd := m.Update(click(b.start+offset, m.controlsRow()))
		drain(t, next.(Model), cmd)
		if len(st.resolved) != 1 {
			t.Errorf("column %d of the button did nothing", b.start+offset)
		}
	}
}

// What is drawn has to be as wide and as tall as what is clicked, or a click
// lands on the wrong button or on nothing.
func TestAButtonIsAsWideAsItsHitbox(t *testing.T) {
	for _, label := range []string{labelPrevious, labelPause, labelRepeatOne} {
		for _, state := range []buttonState{buttonDefault, buttonActive, buttonDisabled} {
			lines := strings.Split(renderButton(label, state, nil), "\n")
			if len(lines) != controlsRows {
				t.Errorf("%q %v draws %d rows, want %d",
					label, state, len(lines), controlsRows)
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got != buttonWidth(label) {
					t.Errorf("%q %v row %d is %d cells, buttonWidth is %d",
						label, state, i, got, buttonWidth(label))
				}
			}
			// And it is the label with its padding, and nothing else.
			if got, want := plain(lines[0]), padded(label); got != want {
				t.Errorf("%q %v draws %q, want %q", label, state, got, want)
			}
		}
	}
}

// q used to quit, which is a keystroke away from every other letter. Only
// ctrl+c does now.
func TestOnlyCtrlCQuits(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	if _, cmd := m.Update(keyPress("q")); cmd != nil {
		t.Error("q still does something")
	}
	if _, cmd := m.Update(keyPress("ctrl+c")); cmd == nil {
		t.Error("ctrl+c does not quit")
	}
	// Not from inside the menu either.
	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	m.Tracks = fromAPI(library().tracks["LM"])
	next, cmd = m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	if !m.menu.open {
		t.Fatal("no menu to test")
	}
	next, cmd = m.Update(keyPress("q"))
	if cmd != nil {
		t.Error("q quits from the menu")
	}
	if !next.(Model).menu.open {
		t.Error("q closed the menu; only esc should")
	}
	if _, cmd := next.(Model).Update(keyPress("ctrl+c")); cmd == nil {
		t.Error("ctrl+c does not quit from the menu")
	}
}

// Unliking a track takes it out of Liked Music at once. Refetching instead
// would put it back: the server takes a moment to agree.
func TestUnlikingRemovesTheRowFromLikedMusic(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.Playlists = []Playlist{{ID: likedPlaylistID, Title: "Liked Music"}}
	m.Tracks = fromAPI(lib.tracks["LM"])
	for i := range m.Tracks {
		m.Tracks[i].Rating = RatingUp
	}
	m.showingID = likedPlaylistID
	m.cache[likedPlaylistID] = cached{tracks: m.Tracks}
	before := len(m.Tracks)
	m.playing = m.Tracks[0]

	// Rated through the control rather than by clicking it: the liked
	// playlist does not carry the thumbs, since every row there is liked.
	next, cmd := m.press(controlThumbUp)
	m = drain(t, next.(Model), cmd)

	if len(m.Tracks) != before-1 {
		t.Fatalf("the list still has %d rows, want %d", len(m.Tracks), before-1)
	}
	for _, track := range m.Tracks {
		if track.VideoID == "a" {
			t.Error("the unliked track is still listed")
		}
	}
	if _, cached := m.cache[likedPlaylistID]; cached {
		t.Error("the stale list is still cached")
	}
	// And it did not go asking the server for the list again.
	for _, asked := range lib.askedFor {
		if asked == likedPlaylistID {
			t.Error("refetched Liked Music, which can still list the track")
		}
	}
}

// Liking one does need the server: there is no telling where it goes.
func TestLikingWhileOnLikedMusicRefetches(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	lib.tracks[likedPlaylistID] = []ytm.Track{{VideoID: "a", Title: "Alpha"}}
	m := wired(t, lib, st, au)
	m.Playlists = []Playlist{{ID: likedPlaylistID, Title: "Liked Music"}}
	opened, openCmd := m.showTab()
	m = drain(t, opened, openCmd)
	m.playing = m.Tracks[0]

	asked := len(lib.askedFor)
	// Rated through the control rather than by clicking it: the liked
	// playlist does not carry the thumbs, since every row there is liked.
	next, cmd := m.press(controlThumbUp)
	m = drain(t, next.(Model), cmd)

	if len(lib.askedFor) != asked+1 {
		t.Errorf("asked for %v; want one more fetch", lib.askedFor)
	}
}

// The thumbs follow the track, not the playlist: pressing one on a track
// that already carries that rating takes it off, and the label says so.
func TestTheThumbsFollowTheTrack(t *testing.T) {
	m, _, _, _ := playingModel(t)

	label := func(m Model, c control) string {
		b, ok := buttonAt(m, c)
		if !ok {
			t.Fatalf("%v is missing", c)
		}
		return strings.TrimSpace(b.label)
	}

	if got := label(m, controlThumbUp); got != labelLike {
		t.Errorf("unrated says %q, want %q", got, labelLike)
	}
	m.playing.Rating = RatingUp
	if got := label(m, controlThumbUp); got != labelUnlike {
		t.Errorf("liked says %q, want %q", got, labelUnlike)
	}
	if got := label(m, controlThumbDown); got != labelDislike {
		t.Errorf("liked changed the other one to %q", got)
	}
	// Dislike keeps its name in both states: English has no word for taking
	// one off. The fill is what says it is in force.
	m.playing.Rating = RatingDown
	if got := label(m, controlThumbDown); got != labelDislike {
		t.Errorf("disliked says %q, want %q", got, labelDislike)
	}
	if b, _ := buttonAt(m, controlThumbDown); b.state != buttonActive {
		t.Errorf("a disliked track leaves the button %v", b.state)
	}
	if got := label(m, controlThumbUp); got != labelLike {
		t.Errorf("disliked changed the other one to %q", got)
	}

	// Both states are drawn at the same width, so the button does not change
	// size under the pointer when a rating lands.
	m.playing.Rating = RatingNone
	unrated, _ := buttonAt(m, controlThumbUp)
	m.playing.Rating = RatingUp
	rated, _ := buttonAt(m, controlThumbUp)
	if a, b := buttonWidth(unrated.label), buttonWidth(rated.label); a != b {
		t.Errorf("Like is %d wide and Unlike %d", a, b)
	}
}

// The liked playlist offers neither, the way its table carries no mark: every
// row there is liked, so one button would only ever read Unlike and the other
// would only ever take the row off the page.
func TestTheLikedPlaylistDropsTheThumbs(t *testing.T) {
	m, lib, _, _ := playingModel(t)
	m.showingID = likedPlaylistID
	m.playing.Rating = RatingUp

	for _, c := range []control{controlThumbUp, controlThumbDown} {
		if b, ok := buttonAt(m, c); ok {
			t.Errorf("the liked playlist still draws %q", strings.TrimSpace(b.label))
		}
	}
	// The transport stays, and the row is still the whole width.
	if _, ok := buttonAt(m, controlPlayPause); !ok {
		t.Error("the transport went with them")
	}
	if got := lipgloss.Width(plain(controlsLine(m))); got != m.width {
		t.Errorf("the row is %d cells, want %d", got, m.width)
	}

	// Only the buttons are gone: rating is still a key away, and the row menu
	// still offers it.
	next, cmd := m.Update(keyPress("+"))
	drain(t, next.(Model), cmd)
	if len(lib.rated) != 1 {
		t.Errorf("rated %+v, want the + key to still work there", lib.rated)
	}
}

// The row doubles as its own help, so every button says which key works it.
func TestEveryButtonNamesItsKey(t *testing.T) {
	m, _, _, _ := playingModel(t)
	want := map[control]string{
		controlPrevious:  "(p)",
		controlPlayPause: "(space)",
		controlNext:      "(n)",
		controlThumbUp:   "(+)",
		controlThumbDown: "(-)",
		controlRepeat:    "(r)",
	}
	for _, b := range m.controlButtons() {
		key, ok := want[b.control]
		if !ok {
			t.Errorf("%v is not in the table", b.control)
			continue
		}
		if !strings.Contains(b.label, key) {
			t.Errorf("%v says %q, want it to name %s", b.control, b.label, key)
		}
		delete(want, b.control)
	}
	for c := range want {
		t.Errorf("%v was not on the row", c)
	}
}

// A dislike of what is playing moves on. Nothing honours "do not play this"
// like not playing it.
func TestDislikingWhatIsPlayingMovesOn(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.setTracks(fromAPI(lib.tracks["LM"]))
	if len(m.Tracks) < 2 {
		t.Fatalf("need two tracks, got %d", len(m.Tracks))
	}
	m.playing = m.Tracks[0]

	next, cmd := m.press(controlThumbDown)
	m = drain(t, next.(Model), cmd)

	if m.playing.VideoID != m.Tracks[1].VideoID {
		t.Errorf("playing %q, want it to have moved to %q",
			m.playing.VideoID, m.Tracks[1].VideoID)
	}
	if m.Tracks[0].Rating != RatingDown {
		t.Errorf("the dislike did not land: %v", m.Tracks[0].Rating)
	}
	if len(au.loaded) == 0 {
		t.Error("nothing was loaded, so it did not really move on")
	}
}

// Taking a dislike off is not a reason to skip anything.
func TestUndislikingStaysPut(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.setTracks(fromAPI(lib.tracks["LM"]))
	m.Tracks[0].Rating = RatingDown
	m.playing = m.Tracks[0]

	next, cmd := m.press(controlThumbDown)
	m = drain(t, next.(Model), cmd)

	if m.playing.VideoID != m.Tracks[0].VideoID {
		t.Errorf("playing %q, want to have stayed on %q",
			m.playing.VideoID, m.Tracks[0].VideoID)
	}
	if m.Tracks[0].Rating != RatingNone {
		t.Errorf("the dislike was not taken off: %v", m.Tracks[0].Rating)
	}
}

// Disliking a row further down the list says something about that row, not
// about the next three minutes.
func TestDislikingAnotherRowLeavesPlaybackAlone(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.setTracks(fromAPI(lib.tracks["LM"]))
	m.playing = m.Tracks[0]
	m.trackCursor = 1

	next, cmd := m.Update(keyPress("-"))
	m = drain(t, next.(Model), cmd)

	if m.playing.VideoID != m.Tracks[0].VideoID {
		t.Errorf("playing %q, want it untouched on %q",
			m.playing.VideoID, m.Tracks[0].VideoID)
	}
	if m.Tracks[1].Rating != RatingDown {
		t.Errorf("the dislike did not land on the cursor row: %v", m.Tracks[1].Rating)
	}
	if len(au.loaded) != 0 {
		t.Errorf("it loaded %v", au.loaded)
	}
}

// And a like never moves on, whatever it is on.
func TestLikingWhatIsPlayingStaysPut(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.setTracks(fromAPI(lib.tracks["LM"]))
	m.playing = m.Tracks[0]

	next, cmd := m.press(controlThumbUp)
	m = drain(t, next.(Model), cmd)

	if m.playing.VideoID != m.Tracks[0].VideoID {
		t.Errorf("a like moved playback to %q", m.playing.VideoID)
	}
	if len(au.loaded) != 0 {
		t.Errorf("a like loaded %v", au.loaded)
	}
}

// The button component: three states, one place that decides how each reads.
// Default and active look the same for now and that is the point — the states
// exist so the look can change in one place later.
func TestTheButtonComponentsThreeStates(t *testing.T) {
	const label = "Prev (p)"

	def := renderButton(label, buttonDefault, nil)
	act := renderButton(label, buttonActive, nil)
	off := renderButton(label, buttonDisabled, nil)

	if def != padded(label) {
		t.Errorf("the default state draws %q, want just the label", def)
	}
	if act != def {
		t.Errorf("active draws %q where default draws %q; without a hue they are the same",
			act, def)
	}
	// Given one, active is drawn in it and default is not: the ratings say
	// which rating they are that way, and nothing else passes a hue.
	hued := renderButton(label, buttonActive, liked)
	if hued == def {
		t.Error("active with a hue draws the same as default")
	}
	if !sgrCodes(hued)[likedFG] {
		t.Errorf("active with a hue is not drawn in it: %v", sgrCodes(hued))
	}
	if plain(hued) != padded(label) {
		t.Errorf("a hue changed the label to %q", plain(hued))
	}
	if got, want := lipgloss.Width(hued), buttonWidth(label); got != want {
		t.Errorf("a hued button is %d cells, buttonWidth is %d", got, want)
	}
	if plain(renderButton(label, buttonDefault, liked)) != padded(label) ||
		sgrCodes(renderButton(label, buttonDefault, liked))[likedFG] {
		t.Error("a hue reached a button that is not active")
	}
	if off == def {
		t.Error("disabled draws the same as default")
	}
	if !sgrCodes(off)[faintSGR] {
		t.Errorf("disabled is not dimmed: %v", sgrCodes(off))
	}
	if plain(off) != padded(label) {
		t.Errorf("disabled changed the label to %q", plain(off))
	}
	// Every state is the same width, or a click lands on the wrong button.
	for _, s := range []string{def, act, off} {
		if got := lipgloss.Width(s); got != buttonWidth(label) {
			t.Errorf("%q is %d cells, buttonWidth is %d", s, got, buttonWidth(label))
		}
	}
}

// A button is its label and nothing else. The space it used to carry either
// side was the only indent on the screen: the transport sat a cell to the
// right of the list above it and the bar below it, which is exactly the sort
// of thing that is invisible until it is gone.
func TestAButtonHasNoAirOfItsOwn(t *testing.T) {
	if buttonPadding != 0 {
		t.Errorf("buttonPadding is %d, want none", buttonPadding)
	}
	for _, label := range []string{labelPrevious, labelClose, labelHelp} {
		if got := renderButton(label, buttonDefault, nil); got != label {
			t.Errorf("%q drew %q, want the label alone", label, got)
		}
		if got, want := buttonWidth(label), lipgloss.Width(label); got != want {
			t.Errorf("%q is %d cells, the label is %d", label, got, want)
		}
	}

	// So the row of them starts where the list and the bar start.
	m, _, _, _ := playingModel(t)
	if row := strings.TrimRight(plain(controlsLine(m)), " "); !strings.HasPrefix(row, labelPrevious) {
		t.Errorf("the buttons are indented: %q", row)
	}
	if b, _ := buttonAt(m, controlPrevious); b.start != contentLeft {
		t.Errorf("the first button starts at column %d, want %d", b.start, contentLeft)
	}
}

// The popover's way out goes through the same component, so changing how a
// button reads changes that one too.
func TestThePopoverButtonIsTheSameComponent(t *testing.T) {
	m, _, _, _ := menuModel(t)
	m = openVia(t, m, menuArtist)

	header := strings.Split(m.renderModal(), "\n")[1]
	if !strings.Contains(plain(header), padded(labelClose)) {
		t.Errorf("the way out is not drawn through the component: %q", plain(header))
	}
	_, _, width, ok := m.modalCloseButton()
	if !ok {
		t.Fatal("no way out")
	}
	if want := buttonWidth(labelClose); width != want {
		t.Errorf("its hitbox is %d, the component draws %d", width, want)
	}
}
