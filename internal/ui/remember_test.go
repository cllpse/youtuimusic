package ui

import (
	"testing"
	"time"

	"github.com/cllpse/youtuimusic/internal/state"
	"github.com/cllpse/youtuimusic/internal/ytm"
)

// opened starts the app the way the runtime does, having been told where
// it last was.
func opened(t *testing.T, lib *fakeLibrary, st state.State) Model {
	t.Helper()
	m := wired(t, lib, &fakeStreams{}, newFakeAudio()).Restore(st)
	return drain(t, m, m.Init())
}

// longList gives the second playlist enough rows to have somewhere to
// scroll to.
func longList() *fakeLibrary {
	lib := library()
	var many []ytm.Track
	for i := 0; i < 40; i++ {
		many = append(many, ytm.Track{
			VideoID: string(rune('a' + i%26)), Title: "Track", Duration: time.Minute,
		})
	}
	lib.tracks["PL1"] = many
	return lib
}

func TestItOpensWhereItClosed(t *testing.T) {
	m := opened(t, longList(), state.State{Playlist: "PL1", Cursor: 12, Offset: 8})

	if got, _ := m.SelectedPlaylist(); got.ID != "PL1" {
		t.Errorf("opened on %q, want the remembered playlist", got.ID)
	}
	if m.trackCursor != 12 {
		t.Errorf("cursor = %d, want 12", m.trackCursor)
	}
	// The row has to actually be on screen, not merely selected.
	if m.trackCursor < m.trackOffset || m.trackCursor >= m.trackOffset+m.listHeight() {
		t.Errorf("row %d is off screen: offset %d, height %d",
			m.trackCursor, m.trackOffset, m.listHeight())
	}
}

func TestTheOrderAndRepeatComeBackToo(t *testing.T) {
	m := opened(t, library(), state.State{
		Playlist: "LM", Sort: "artist", Descending: true, Repeat: "one"})

	if m.sort.by != sortArtist || !m.sort.desc {
		t.Errorf("sort = %+v, want artist descending", m.sort)
	}
	if m.repeat != RepeatOne {
		t.Errorf("repeat = %v, want one", m.repeat)
	}
}

// A playlist that is gone must not leave the app on a blank tab.
func TestAVanishedPlaylistOpensAtTheStart(t *testing.T) {
	m := opened(t, library(), state.State{Playlist: "PLgone", Cursor: 5})

	if m.tabCursor != 0 {
		t.Errorf("tab cursor = %d, want the first tab", m.tabCursor)
	}
	if got, _ := m.SelectedPlaylist(); got.ID != "LM" {
		t.Errorf("opened on %q", got.ID)
	}
	// The position belonged to the missing playlist, so it is not reused.
	if m.trackCursor != 0 {
		t.Errorf("cursor = %d, want the top", m.trackCursor)
	}
}

// A listing that has since got shorter must not select past its end.
func TestAPositionPastTheEndClamps(t *testing.T) {
	m := opened(t, library(), state.State{Playlist: "LM", Cursor: 900})

	if m.trackCursor >= len(m.Tracks) {
		t.Errorf("cursor %d is past the %d tracks", m.trackCursor, len(m.Tracks))
	}
}

// Nothing remembered is the first run, and it opens the way it always did.
func TestWithNothingRememberedItOpensAtTheTop(t *testing.T) {
	m := opened(t, library(), state.State{})

	if m.tabCursor != 0 || m.trackCursor != 0 {
		t.Errorf("tab %d cursor %d, want the top of the first tab", m.tabCursor, m.trackCursor)
	}
	if m.sort.by != sortNone {
		t.Errorf("sort = %+v, want the arrival order", m.sort)
	}
}

// Quitting writes what is on screen, and starting again finds it.
func TestQuittingRecordsWhereItWas(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := opened(t, longList(), state.State{})
	m.tabCursor = 1
	m, _ = m.showTab()
	m = drain(t, m, nil)
	m.trackCursor, m.trackOffset = 7, 3
	m.sort = sortSpec{by: sortLength, desc: true}
	m.repeat = RepeatAll

	next, _ := m.Update(keyPress("ctrl+c"))
	_ = next

	got := state.Load()
	want := state.State{Playlist: "PL1", Cursor: 7, Offset: 3,
		Sort: "length", Descending: true, Repeat: "all"}
	if got != want {
		t.Errorf("recorded %+v,\n    want %+v", got, want)
	}
}

// The round trip is the point: what quitting wrote is what opening reads.
func TestTheRecordedStateIsRestorable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	first := opened(t, longList(), state.State{})
	first.tabCursor = 1
	first, _ = first.showTab()
	first = drain(t, first, nil)
	first.trackCursor = 11
	first.scroll()
	first.Update(keyPress("ctrl+c"))

	second := opened(t, longList(), state.Load())
	if got, _ := second.SelectedPlaylist(); got.ID != "PL1" {
		t.Errorf("reopened on %q", got.ID)
	}
	if second.trackCursor != 11 {
		t.Errorf("cursor = %d, want 11", second.trackCursor)
	}
}

func TestSortAndRepeatNamesRoundTrip(t *testing.T) {
	for _, c := range []sortColumn{sortNone, sortTitle, sortArtist, sortLength, sortAdded} {
		if got := parseSortColumn(c.name()); got != c {
			t.Errorf("sort %v became %v via %q", c, got, c.name())
		}
	}
	for _, r := range []Repeat{RepeatOff, RepeatAll, RepeatOne} {
		if got := parseRepeat(r.name()); got != r {
			t.Errorf("repeat %v became %v via %q", r, got, r.name())
		}
	}
	// An unknown name is not a crash and not a silent wrong column.
	if got := parseSortColumn("colour"); got != sortNone {
		t.Errorf("an unknown column became %v", got)
	}
}
