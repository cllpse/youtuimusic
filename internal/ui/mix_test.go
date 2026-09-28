package ui

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cllpse/youtuimusic/internal/player"
	"github.com/cllpse/youtuimusic/internal/state"
	"github.com/cllpse/youtuimusic/internal/ytm"
)

// mixModel is a wired model on a playlist, with a track under the cursor to
// build a mix from.
func mixModel(t *testing.T) (Model, *fakeLibrary, *fakeAudio) {
	t.Helper()
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.Playlists = []Playlist{
		{ID: likedPlaylistID, Title: "Liked Music"},
		{ID: "PL1", Title: "Favorites"},
	}
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.showingID = likedPlaylistID
	return m, lib, au
}

// A mix opens as a tab of its own, in front of the library's: it is where the
// music is, and it is not one of yours to come back to.
func TestStartingAMixOpensATabInFront(t *testing.T) {
	m, lib, _ := mixModel(t)
	seed := m.Tracks[0]

	next, cmd := m.press(controlMix)
	m = drain(t, next.(Model), cmd)

	if m.tabCount() != 3 {
		t.Fatalf("%d tabs, want the library's two and a mix", m.tabCount())
	}
	if got := m.tabAt(0); got.ID != ytm.RadioID(seed.VideoID) || got.kind != tabMix {
		t.Fatalf("the first tab is %+v", got)
	}
	if m.tabAt(1).ID != likedPlaylistID || m.tabAt(2).ID != "PL1" {
		t.Errorf("the library's tabs moved: %v %v", m.tabAt(1), m.tabAt(2))
	}
	if m.tabCursor != 0 {
		t.Errorf("the cursor is on tab %d, want the mix", m.tabCursor)
	}

	// Built from the seed, by the one call that builds one.
	var asked string
	for _, call := range lib.askedFor {
		if strings.HasPrefix(call, "radio:") { // the API's word for it
			asked = call
		}
	}
	if want := "radio:" + seed.VideoID; asked != want {
		t.Errorf("asked %v, want %q", lib.askedFor, want)
	}
	// And its tracks are on screen.
	if len(m.Tracks) != 2 || m.Tracks[0].VideoID != seed.VideoID {
		t.Fatalf("the mix is %+v", m.Tracks)
	}
	if m.showingID != ytm.RadioID(seed.VideoID) {
		t.Errorf("showing %q", m.showingID)
	}
}

// It plays the track it was built from, which is what starting a mix means —
// unless that track is already playing, where starting it again would be a
// worse answer than doing nothing.
func TestAMixPlaysItsSeedUnlessItIsAlreadyPlaying(t *testing.T) {
	m, _, au := mixModel(t)
	next, cmd := m.press(controlMix)
	m = drain(t, next.(Model), cmd)
	if len(au.loaded) != 1 {
		t.Fatalf("loaded %v, want the seed", au.loaded)
	}
	if m.playing.VideoID != m.Tracks[0].VideoID {
		t.Errorf("playing %q, want the seed", m.playing.VideoID)
	}

	again, cmd := m.press(controlMix)
	m = drain(t, again.(Model), cmd)
	if len(au.loaded) != 1 {
		t.Errorf("loaded %v; the seed was already playing", au.loaded)
	}
}

// A mix is built from what is playing, or from what is under the cursor when
// nothing is — the rule the play button follows. Without either there is
// nothing to build from, and the button says so.
func TestTheMixButtonNeedsATrack(t *testing.T) {
	m, _, _ := mixModel(t)
	if b, _ := buttonAt(m, controlMix); b.state != buttonDefault {
		t.Errorf("a row under the cursor leaves the mix button %v", b.state)
	}
	if seed, ok := m.mixSeed(); !ok || seed.VideoID != m.Tracks[0].VideoID {
		t.Errorf("the seed is %+v, want the highlighted row", seed)
	}

	m.playing = m.Tracks[1]
	if seed, _ := m.mixSeed(); seed.VideoID != m.Tracks[1].VideoID {
		t.Errorf("the seed is %+v, want what is playing", seed)
	}

	empty := m
	empty.Tracks, empty.playing = nil, Track{}
	if b, _ := buttonAt(empty, controlMix); b.state != buttonDisabled {
		t.Errorf("the mix button is %v with nothing to build from", b.state)
	}
	if next, _ := empty.press(controlMix); next.(Model).mix.ID != "" {
		t.Error("it started a mix from nothing")
	}
}

// Starting another replaces the first: two tabs with the same name and no way
// to tell them apart is not a feature.
func TestAnotherMixReplacesTheFirst(t *testing.T) {
	m, _, _ := mixModel(t)
	next, cmd := m.press(controlMix)
	m = drain(t, next.(Model), cmd)
	first := m.mix.ID

	m.trackCursor = 1
	m.playing = Track{}
	next, cmd = m.press(controlMix)
	m = drain(t, next.(Model), cmd)

	if m.mix.ID == first {
		t.Error("the second mix did not replace the first")
	}
	if m.tabCount() != 3 {
		t.Errorf("%d tabs, want one mix and the library's two", m.tabCount())
	}
	if _, cached := m.cache[first]; cached {
		t.Error("the first mix is still cached")
	}
}

// A mix is cyan at both ends of its page and down the side of it, the way the
// liked playlist is magenta: its label, the line under the list, the scrollbar.
func TestAMixIsCyanAtBothEndsOfItsPage(t *testing.T) {
	m, _, _ := mixModel(t)
	m = sized(m, 120, 20)
	answered, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}})
	m = answered.(Model)

	next, cmd := m.press(controlMix)
	m = drain(t, next.(Model), cmd)
	m.Tracks = rows(100) // long enough for a scrollbar

	lines := strings.Split(m.View().Content, "\n")
	if got := lines[1]; !sgrCodes(got)[mixFG] {
		t.Errorf("the tab's label is not cyan: %q", got)
	}
	if got := lines[m.playerTop()]; !sgrCodes(got)[mixFG] {
		t.Errorf("the line under the list is not cyan: %q", got)
	}
	// A row with nothing selected or playing on it: the only colour it can
	// carry is the bar.
	if got := lines[tabsHeight+headerRows+4]; !sgrCodes(got)[mixFG] {
		t.Errorf("the scrollbar is not cyan: %q", got)
	}
	// The tab row says which tab is in front the way it always does, in the
	// shape of it rather than the colour.
	if !strings.HasPrefix(plain(lines[2]), "╯") {
		t.Errorf("the mix is not the tab in front: %q", plain(lines[2]))
	}
	// And the button that started it wears the same colour.
	row := lines[m.controlsRow()]
	for _, run := range styledRuns(row) {
		if strings.Contains(run.text, strings.TrimSpace(labelMix)) &&
			!strings.Contains(run.codes, mixFG) {
			t.Errorf("the mix button is drawn %s: %q", run.codes, row)
		}
	}
}

// What is remembered is a page to come back to, and a mix is not one: it is
// gone when the app closes and its id names nothing the next time.
func TestAMixIsNotRemembered(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	m, _, _ := mixModel(t)
	m.tabCursor = 1 // a library playlist
	m.playing = m.Tracks[0]
	m.record()
	before := state.Load()
	if before.Playlist != "PL1" {
		t.Fatalf("recorded %+v, want the playlist in front", before)
	}

	next, cmd := m.press(controlMix)
	m = drain(t, next.(Model), cmd)
	m.record()

	after := state.Load()
	if after.Playlist != before.Playlist {
		t.Errorf("recorded %q from a mix, want the last playlist %q",
			after.Playlist, before.Playlist)
	}
}

// The width the row needs grew with the button, so a narrow one gives up its
// modes from the right rather than dropping the lot.
func TestANarrowRowKeepsTheTransport(t *testing.T) {
	for _, width := range []int{40, 50, 60, 70, 80} {
		m := sized(sample(), width, 20)
		m.Tracks = rows(4)
		m.playing = m.Tracks[0]

		var seen []control
		for _, b := range m.controlButtons() {
			seen = append(seen, b.control)
			if b.end > width {
				t.Errorf("width %d: %v runs past the edge", width, b.control)
			}
		}
		if got := lipgloss.Width(plain(controlsLine(m))); got != width {
			t.Errorf("width %d: the row renders %d cells", width, got)
		}
		// Whatever fits, the transport is what fits first.
		if len(seen) > 0 && seen[0] != controlPrevious {
			t.Errorf("width %d: the row starts with %v", width, seen[0])
		}
		if len(seen) > 0 && len(seen) < 3 {
			t.Errorf("width %d: %v, want at least the transport", width, seen)
		}
	}
}

// A mix is endless, so it pages like any other listing: the server hands back a
// token with every page and the list offers the next one at the bottom.
func TestAMixOffersItsNextPage(t *testing.T) {
	m, lib, _ := mixModel(t)
	lib.next = ytm.Continuation{Endpoint: "next", Token: "more-of-the-mix"}
	lib.morePage = fromUI([]Track{{VideoID: "z", Title: "further in"}})

	next, cmd := m.press(controlMix)
	m = drain(t, next.(Model), cmd)

	if !m.more.More() {
		t.Fatal("the mix does not know there is more of it")
	}
	if m.more.Endpoint != "next" {
		t.Errorf("it would ask %q, want the endpoint a queue continues at", m.more.Endpoint)
	}
	if m.rowCount() != len(m.Tracks)+1 {
		t.Fatalf("row count = %d, want one more than the tracks", m.rowCount())
	}
	if last := plain(m.table(m.width, m.bodyHeight()).rows()[len(m.Tracks)]); !strings.Contains(last, labelLoadMore) {
		t.Errorf("the last row is %q, want the offer", last)
	}

	// Walking onto that row takes it.
	before := len(m.Tracks)
	for range before {
		at, cmd := m.Update(keyPress("j"))
		m = drain(t, at.(Model), cmd)
	}
	if len(m.Tracks) != before+1 || m.Tracks[before].Title != "further in" {
		t.Fatalf("tracks = %+v", m.Tracks)
	}
}

// A mix is endless, so a track running out at the end of what has been fetched
// must ask for more of it rather than stop the radio. This is the whole point
// of a mix: it goes on.
func TestAMixGoesOnWhenThePageRunsOut(t *testing.T) {
	m, lib, _ := mixModel(t)
	lib.next = ytm.Continuation{Endpoint: "next", Token: "on-and-on"}
	lib.morePage = fromUI([]Track{{VideoID: "after", Title: "After"}})

	next, cmd := m.press(controlMix)
	m = drain(t, next.(Model), cmd)

	// Play the last track already fetched and let it run out.
	last := m.Tracks[len(m.Tracks)-1]
	started, play := m.start(last)
	m = drain(t, started.(Model), play)

	ended, cmd := m.Update(eventMsg(player.Event{Name: player.EndFile, Data: "eof"}))
	m = drain(t, ended.(Model), cmd)

	if m.playing.VideoID != "after" {
		t.Fatalf("playing %q, want the next page's track", m.playing.VideoID)
	}
}

// Switching tabs is not stopping the music. A track that runs out while the
// reader is looking somewhere else still advances, from the listing it was
// started from rather than from whatever is on screen.
func TestSwitchingTabsDoesNotStopTheMix(t *testing.T) {
	m, lib, _ := mixModel(t)
	lib.tracks["PL1"] = []ytm.Track{{VideoID: "p1", Title: "One"}}

	next, cmd := m.press(controlMix)
	m = drain(t, next.(Model), cmd)
	seed := m.playing.VideoID
	if seed == "" {
		t.Fatal("the mix did not start playing")
	}

	switched, cmd := m.selectTab(2)
	m = drain(t, switched, cmd)
	if m.showingID == m.mix.ID {
		t.Fatal("still showing the mix")
	}

	ended, cmd := m.Update(eventMsg(player.Event{Name: player.EndFile, Data: "eof"}))
	m = drain(t, ended.(Model), cmd)

	if m.playing.VideoID == seed {
		t.Errorf("playback did not advance from %q", seed)
	}
	if m.playing.VideoID != seed+"-mix" {
		t.Errorf("playing %q, want the track after the seed", m.playing.VideoID)
	}
}

// A mix is the server's order and it is endless, so there is no order of the
// reader's to complete and nothing to page through looking for one.
func TestAMixIsNotSorted(t *testing.T) {
	m, _, _ := mixModel(t)
	next, cmd := m.press(controlMix)
	m = drain(t, next.(Model), cmd)

	if sorted := press(m, "s"); sorted.sort.by != sortNone {
		t.Errorf("a mix was sorted by %v", sorted.sort.by)
	}
	if reversed := press(m, "S"); reversed.sort.by != sortNone {
		t.Errorf("a mix was reverse-sorted by %v", reversed.sort.by)
	}
}

// A listing held in memory can go stale while the app is open. R throws it
// away and asks again, keeping the reader's place.
func TestRefreshRefetchesTheList(t *testing.T) {
	m, lib, _ := mixModel(t)
	before := len(lib.askedFor)

	next, cmd := m.Update(keyPress("R"))
	m = drain(t, next.(Model), cmd)

	if len(lib.askedFor) <= before {
		t.Fatalf("nothing was refetched: %v", lib.askedFor)
	}
	if last := lib.askedFor[len(lib.askedFor)-1]; last != likedPlaylistID {
		t.Errorf("refetched %q, want the tab in front %q", last, likedPlaylistID)
	}
}
