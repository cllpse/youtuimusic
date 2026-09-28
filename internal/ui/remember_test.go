package ui

import (
	"testing"
	"time"

	"github.com/cllpse/youtuimusic/internal/state"
	"github.com/cllpse/youtuimusic/internal/ytm"
)

// opened starts the app the way the runtime does, having been told what it
// was last playing.
func opened(t *testing.T, lib *fakeLibrary, st state.State) Model {
	t.Helper()
	m := wired(t, lib, &fakeStreams{}, newFakeAudio()).Restore(st)
	return drain(t, m, m.Init())
}

// longList gives the second playlist enough rows that the remembered track
// is not on screen by accident.
func longList() *fakeLibrary {
	lib := library()
	var many []ytm.Track
	for i := 0; i < 40; i++ {
		many = append(many, ytm.Track{
			VideoID:  "v" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Title:    "Track",
			Duration: time.Minute,
		})
	}
	lib.tracks["PL1"] = many
	return lib
}

func TestItOpensOnWhatWasPlaying(t *testing.T) {
	lib := longList()
	want := lib.tracks["PL1"][27].VideoID
	m := opened(t, lib, state.State{Playlist: "PL1", Playing: want})

	if got, _ := m.SelectedPlaylist(); got.ID != "PL1" {
		t.Errorf("opened on %q, want the remembered playlist", got.ID)
	}
	got, ok := m.SelectedTrack()
	if !ok {
		t.Fatal("nothing is selected")
	}
	if got.VideoID != want {
		t.Errorf("selected %q, want the remembered track %q", got.VideoID, want)
	}
	// And it is actually on screen, not merely selected.
	if m.trackCursor < m.trackOffset || m.trackCursor >= m.trackOffset+m.listHeight() {
		t.Errorf("row %d is off screen: offset %d, height %d",
			m.trackCursor, m.trackOffset, m.listHeight())
	}
}

// The point of putting it under the cursor: the play button resumes it,
// rather than anything starting on its own.
func TestNothingPlaysUntilAskedAndThenItIsTheRightTrack(t *testing.T) {
	lib := longList()
	want := lib.tracks["PL1"][27].VideoID
	m := opened(t, lib, state.State{Playlist: "PL1", Playing: want})

	if m.playing.VideoID != "" {
		t.Errorf("it started playing %q on its own", m.playing.VideoID)
	}

	next, cmd := m.press(controlPlayPause)
	m = drain(t, next.(Model), cmd)
	if m.playing.VideoID != want {
		t.Errorf("play started %q, want the remembered track %q", m.playing.VideoID, want)
	}
}

// A playlist that is gone must not leave the app on a blank tab.
func TestAVanishedPlaylistOpensAtTheStart(t *testing.T) {
	m := opened(t, library(), state.State{Playlist: "PLgone", Playing: "a"})

	if m.tabCursor != 0 {
		t.Errorf("tab cursor = %d, want the first tab", m.tabCursor)
	}
	if got, _ := m.SelectedPlaylist(); got.ID != "LM" {
		t.Errorf("opened on %q", got.ID)
	}
	if m.trackCursor != 0 {
		t.Errorf("cursor = %d, want the top", m.trackCursor)
	}
}

// A track dropped from the playlist since leaves the playlist restored and
// the cursor at the top.
func TestAVanishedTrackStillOpensThePlaylist(t *testing.T) {
	m := opened(t, longList(), state.State{Playlist: "PL1", Playing: "gone"})

	if got, _ := m.SelectedPlaylist(); got.ID != "PL1" {
		t.Errorf("opened on %q, want the remembered playlist", got.ID)
	}
	if m.trackCursor != 0 {
		t.Errorf("cursor = %d, want the top", m.trackCursor)
	}
}

// A playlist with nothing playing is an ordinary way to have closed.
func TestAPlaylistAloneIsRestored(t *testing.T) {
	m := opened(t, longList(), state.State{Playlist: "PL1"})

	if got, _ := m.SelectedPlaylist(); got.ID != "PL1" {
		t.Errorf("opened on %q", got.ID)
	}
	if m.trackCursor != 0 {
		t.Errorf("cursor = %d, want the top", m.trackCursor)
	}
}

// Nothing remembered is the first run, and it opens the way it always did.
func TestWithNothingRememberedItOpensAtTheTop(t *testing.T) {
	m := opened(t, library(), state.State{})

	if m.tabCursor != 0 || m.trackCursor != 0 {
		t.Errorf("tab %d cursor %d, want the top of the first tab", m.tabCursor, m.trackCursor)
	}
}

// Quitting writes what was playing and where.
func TestQuittingRecordsWhatWasPlaying(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	lib := longList()
	m := opened(t, lib, state.State{})
	m.tabCursor = 1
	m, cmd := m.showTab()
	m = drain(t, m, cmd)
	m.trackCursor = 5
	next, cmd2 := m.press(controlPlayPause)
	m = drain(t, next.(Model), cmd2)

	m.Update(keyPress("ctrl+c"))

	got := state.Load()
	want := state.State{Playlist: "PL1", Playing: lib.tracks["PL1"][5].VideoID}
	if got != want {
		t.Errorf("recorded %+v,\n    want %+v", got, want)
	}
}

// Nothing playing still records the playlist, so reopening lands there.
func TestQuittingWithNothingPlayingRecordsThePlaylist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := opened(t, longList(), state.State{})
	m.tabCursor = 1
	m, cmd := m.showTab()
	m = drain(t, m, cmd)
	m.Update(keyPress("ctrl+c"))

	if got := state.Load(); got != (state.State{Playlist: "PL1"}) {
		t.Errorf("recorded %+v", got)
	}
}

// The round trip is the point: what quitting wrote is what opening resumes.
func TestTheRecordedTrackIsResumable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	lib := longList()
	first := opened(t, lib, state.State{})
	first.tabCursor = 1
	first, cmd := first.showTab()
	first = drain(t, first, cmd)
	first.trackCursor = 31
	next, cmd2 := first.press(controlPlayPause)
	first = drain(t, next.(Model), cmd2)
	want := first.playing.VideoID
	first.Update(keyPress("ctrl+c"))

	second := opened(t, longList(), state.Load())
	if got, _ := second.SelectedPlaylist(); got.ID != "PL1" {
		t.Errorf("reopened on %q", got.ID)
	}
	resumed, cmd := second.press(controlPlayPause)
	second = drain(t, resumed.(Model), cmd)
	if second.playing.VideoID != want {
		t.Errorf("resumed %q, want %q", second.playing.VideoID, want)
	}
}

// The theme is a display choice, so it comes back with the rest: a reader who
// dropped the colours does not choose that again every launch.
func TestTheThemeComesBackWithEverythingElse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := press(sample(), "m")
	if !m.mono {
		t.Fatal("the theme key did not switch to monochrome")
	}
	m.record()

	st := state.Load()
	if !st.Mono {
		t.Fatalf("the theme was not written: %+v", st)
	}

	restored := New(Services{}).Restore(st)
	if !restored.mono {
		t.Error("the restored model is not monochrome")
	}
	// The bar holds its colour rather than looking it up per frame, so it has
	// to have been rebuilt with the greys, not merely flagged.
	if got := restored.barFill()(0, 0); got != emphasis {
		t.Errorf("the restored bar is %v, want the bright foreground %v", got, emphasis)
	}
}

// And a model that opens in the colour theme gets the lit bar, so a restore
// that says nothing about the theme does not silently take the blue away.
func TestTheDefaultRestoreKeepsTheColourBar(t *testing.T) {
	m := New(Services{}).Restore(state.State{})
	if m.mono {
		t.Error("a restore of nothing turned monochrome on")
	}
	if got := m.barFill()(0, 0); got != live {
		t.Errorf("the bar is %v, want the player's colour %v", got, live)
	}
}
