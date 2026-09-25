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

func controlsLine(m Model) string {
	return strings.Split(m.View().Content, "\n")[m.controlsRow()]
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

func TestControlsSitLeftCentreAndRight(t *testing.T) {
	m, _, _, _ := playingModel(t)
	row := plain(controlsLine(m))

	// The rendered row includes the box's own border columns.
	if got := lipgloss.Width(row); got != m.width {
		t.Fatalf("the controls row is %d cells, want %d", got, m.width)
	}

	// Transport against the left edge of the box, in order.
	for i, c := range []control{controlPrevious, controlPlayPause, controlNext} {
		b, ok := buttonAt(m, c)
		if !ok {
			t.Fatalf("control %v is missing", c)
		}
		if want := contentLeft + i*(buttonWidth+buttonGap); b.start != want {
			t.Errorf("control %v starts at %d, want %d", c, b.start, want)
		}
	}

	// Thumbs centred on the row.
	up, _ := buttonAt(m, controlThumbUp)
	down, _ := buttonAt(m, controlThumbDown)
	// Within a cell: a group an odd number of columns wide cannot straddle
	// the middle exactly.
	span := down.end - up.start
	if middle, want := up.start+span/2, contentLeft+m.contentWidth()/2; middle < want-1 || middle > want+1 {
		t.Errorf("the thumbs are centred on %d, want %d", middle, want)
	}

	// Repeat against the right edge.
	rep, _ := buttonAt(m, controlRepeat)
	if want := contentLeft + m.contentWidth(); rep.end != want {
		t.Errorf("repeat ends at %d, want the inside of the right border %d", rep.end, want)
	}

	// And they are where the row actually draws them.
	for _, b := range m.controlButtons() {
		if !strings.Contains(row[colToByte(row, b.start):colToByte(row, b.end)], b.icon) {
			t.Errorf("control %v is not drawn in its own columns %d-%d", b.control, b.start, b.end)
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

func TestPlayPauseIconFollowsTheState(t *testing.T) {
	m, _, _, _ := playingModel(t)
	if got := m.playPauseIcon(); got != iconPause {
		t.Errorf("playing shows %q, want the pause bars", got)
	}
	m.Paused = true
	if got := m.playPauseIcon(); got != iconPlay {
		t.Errorf("paused shows %q, want the play triangle", got)
	}
	m.playing = Track{}
	if got := m.playPauseIcon(); got != iconPlay {
		t.Errorf("idle shows %q, want the play triangle", got)
	}
}

func TestThumbIconsFollowTheRating(t *testing.T) {
	m, _, _, _ := playingModel(t)

	up, _ := buttonAt(m, controlThumbUp)
	down, _ := buttonAt(m, controlThumbDown)
	if up.icon != iconThumbUpOff || down.icon != iconThumbDownOff {
		t.Errorf("unrated shows %q/%q, want the outlines", up.icon, down.icon)
	}
	if up.lit || down.lit {
		t.Error("unrated thumbs should not be lit")
	}

	m.playing.Rating = RatingUp
	up, _ = buttonAt(m, controlThumbUp)
	down, _ = buttonAt(m, controlThumbDown)
	if up.icon != iconThumbUp || !up.lit {
		t.Errorf("rated up shows %q lit=%v", up.icon, up.lit)
	}
	if down.icon != iconThumbDownOff {
		t.Errorf("the other thumb changed to %q", down.icon)
	}

	m.playing.Rating = RatingDown
	down, _ = buttonAt(m, controlThumbDown)
	if down.icon != iconThumbDown || !down.lit {
		t.Errorf("rated down shows %q lit=%v", down.icon, down.lit)
	}
}

func TestRepeatCyclesThroughItsThreeStates(t *testing.T) {
	m, _, _, _ := playingModel(t)
	want := []struct {
		state Repeat
		icon  string
		lit   bool
	}{
		{RepeatAll, iconRepeatAll, true},
		{RepeatOne, iconRepeatOne, true},
		{RepeatOff, iconRepeatOff, false},
	}
	if b, _ := buttonAt(m, controlRepeat); b.icon != iconRepeatOff || b.lit {
		t.Fatalf("starts at %q lit=%v, want repeat-off unlit", b.icon, b.lit)
	}
	for _, step := range want {
		next, _ := m.Update(keyPress("r"))
		m = next.(Model)
		if m.repeat != step.state {
			t.Fatalf("repeat = %v, want %v", m.repeat, step.state)
		}
		b, _ := buttonAt(m, controlRepeat)
		if b.icon != step.icon || b.lit != step.lit {
			t.Errorf("%v shows %q lit=%v, want %q lit=%v", step.state, b.icon, b.lit, step.icon, step.lit)
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
	// The border columns are not buttons either.
	for _, x := range []int{0, m.width - 1} {
		if where, _ := m.hit(x, m.controlsRow()); where != regionNone {
			t.Errorf("column %d is on the border but answered %v", x, where)
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

// Idle, the transport has nothing to act on and says so.
func TestTransportIsUnlitWithNothingPlaying(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	for _, c := range []control{controlPrevious, controlPlayPause, controlNext} {
		b, ok := buttonAt(m, c)
		if !ok {
			t.Fatalf("control %v is missing", c)
		}
		if b.lit {
			t.Errorf("control %v is lit with nothing playing", c)
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
	b, _ := buttonAt(m, controlThumbUp)
	next, cmd := m.Update(click(b.start+1, m.controlsRow()))
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

// A live button fills with the accent; an idle one is only its icon,
// dimmed. Neither draws anything that has to be read as punctuation.
func TestButtonsFillWhenLive(t *testing.T) {
	m, _, _, _ := playingModel(t)
	row := plain(controlsLine(m))

	if strings.ContainsAny(row, "[]") {
		t.Errorf("brackets are drawn: %q", row)
	}
	if !strings.Contains(row, " "+iconPrevious+" ") {
		t.Errorf("the icon is not padded: %q", row)
	}
	if strings.Contains(row, iconPrevious+"  "+iconPause) {
		t.Errorf("buttons are touching: %q", row)
	}

	codes := sgrCodes(controlsLine(m))
	if !codes["44"] {
		t.Errorf("no live button is filled with the accent: %v", codes)
	}
	if !codes["97"] {
		t.Errorf("a filled button has no light text on it: %v", codes)
	}
	// The repeat button is off, so it is dim rather than filled.
	if !codes["90"] {
		t.Errorf("nothing on the row is dimmed: %v", codes)
	}
}

// Idle, a button is only its icon, dimmed: there is nothing to press.
func TestIdleButtonsAreOnlyDimIcons(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	codes := sgrCodes(controlsLine(m))
	if !codes["90"] {
		t.Errorf("the idle row is not dimmed: %v", codes)
	}
	if codes["44"] {
		t.Errorf("an idle button is filled: %v", codes)
	}
}

// Live, it fills with the accent.
func TestALitButtonIsFilled(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	next, _ := m.Update(keyPress("r")) // repeat on lights its button
	if codes := sgrCodes(controlsLine(next.(Model))); !codes["44"] {
		t.Errorf("a lit button is not filled: %v", codes)
	}
}

// What is drawn has to be as wide as what is clicked, or a click lands on
// the wrong button.
func TestAButtonIsAsWideAsItsHitbox(t *testing.T) {
	for _, lit := range []bool{false, true} {
		if got := lipgloss.Width(renderButton(iconPlay, lit)); got != buttonWidth {
			t.Errorf("lit=%v: the button draws %d cells, buttonWidth is %d",
				lit, got, buttonWidth)
		}
	}
}

// Switching repeat on fills its button without touching the others.
func TestTurningSomethingOnFillsItsButton(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	if sgrCodes(controlsLine(m))["44"] {
		t.Fatal("something is already filled")
	}
	next, _ := m.Update(keyPress("r"))
	m = next.(Model)
	if !sgrCodes(controlsLine(m))["44"] {
		t.Errorf("repeat on did not fill its button: %v", sgrCodes(controlsLine(m)))
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

	// Unlike the playing track from the controls.
	b, _ := buttonAt(m, controlThumbUp)
	next, cmd := m.Update(click(b.start+1, m.controlsRow()))
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
	b, _ := buttonAt(m, controlThumbUp)
	next, cmd := m.Update(click(b.start+1, m.controlsRow()))
	m = drain(t, next.(Model), cmd)

	if len(lib.askedFor) != asked+1 {
		t.Errorf("asked for %v; want one more fetch", lib.askedFor)
	}
}
