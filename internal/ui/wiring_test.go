package ui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cllpse/youtuimusic/internal/player"
	"github.com/cllpse/youtuimusic/internal/stream"
	"github.com/cllpse/youtuimusic/internal/ytm"
)

// These exercise the wiring between the model and the backends, with fakes
// in place of the network and mpv. The point is the plumbing — that a
// keystroke reaches the right call and the answer lands in the right field.

type fakeLibrary struct {
	playlists []ytm.Playlist
	tracks    map[string][]ytm.Track
	results   []ytm.Track
	err       error

	askedFor []string
	rated    []struct {
		videoID string
		rating  ytm.Rating
	}
	rateErr error
}

func (f *fakeLibrary) LibraryPlaylists(context.Context) ([]ytm.Playlist, error) {
	return f.playlists, f.err
}

func (f *fakeLibrary) PlaylistTracks(_ context.Context, id string) ([]ytm.Track, error) {
	f.askedFor = append(f.askedFor, id)
	if f.err != nil {
		return nil, f.err
	}
	return f.tracks[id], nil
}

func (f *fakeLibrary) Search(_ context.Context, query string) ([]ytm.Track, error) {
	f.askedFor = append(f.askedFor, "search:"+query)
	return f.results, f.err
}

func (f *fakeLibrary) Rate(_ context.Context, videoID string, r ytm.Rating) error {
	f.rated = append(f.rated, struct {
		videoID string
		rating  ytm.Rating
	}{videoID, r})
	return f.rateErr
}

type fakeStreams struct {
	prefetched []string
	resolved   []string
	err        error
}

func (f *fakeStreams) Resolve(_ context.Context, id string) (stream.Track, error) {
	f.resolved = append(f.resolved, id)
	if f.err != nil {
		return stream.Track{}, f.err
	}
	return stream.Track{VideoID: id, URL: "https://stream/" + id, Duration: 3 * time.Minute}, nil
}

func (f *fakeStreams) Prefetch(_ context.Context, id string) {
	f.prefetched = append(f.prefetched, id)
}

type fakeAudio struct {
	loaded  []string
	toggles int
	seeks   []float64
	events  chan player.Event
}

func newFakeAudio(evs ...player.Event) *fakeAudio {
	ch := make(chan player.Event, len(evs)+1)
	for _, e := range evs {
		ch <- e
	}
	// Closing means watchEvents stops rather than blocking a test forever.
	close(ch)
	return &fakeAudio{events: ch}
}

func (f *fakeAudio) Load(url string) error       { f.loaded = append(f.loaded, url); return nil }
func (f *fakeAudio) TogglePause() error          { f.toggles++; return nil }
func (f *fakeAudio) Seek(s float64) error        { f.seeks = append(f.seeks, s); return nil }
func (f *fakeAudio) Events() <-chan player.Event { return f.events }

func wired(t *testing.T, lib *fakeLibrary, st *fakeStreams, au *fakeAudio) Model {
	t.Helper()
	m := New(Services{Library: lib, Streams: st, Audio: au})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return sized.(Model)
}

// drain runs commands and folds their messages back in, the way the runtime
// does, following batches until everything settles.
func drain(t *testing.T, m Model, cmds ...tea.Cmd) Model {
	t.Helper()
	queue := append([]tea.Cmd{}, cmds...)
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 100 {
			t.Fatal("commands did not settle")
		}
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		msg := cmd()
		if msg == nil {
			continue
		}
		if b, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, b...)
			continue
		}
		next, out := m.Update(msg)
		m, queue = next.(Model), append(queue, out)
	}
	return m
}

func library() *fakeLibrary {
	return &fakeLibrary{
		playlists: []ytm.Playlist{{ID: "LM", Title: "Liked Music"}, {ID: "PL1", Title: "Favorites"}},
		tracks: map[string][]ytm.Track{
			"LM": {
				{VideoID: "a", Title: "Alpha", Artist: "A", Duration: time.Minute},
				{VideoID: "b", Title: "Beta", Artist: "B", Duration: 2 * time.Minute},
			},
		},
		results: []ytm.Track{{VideoID: "z", Title: "Found", Artist: "Z"}},
	}
}

// Startup should leave something on screen without any keystrokes.
func TestInitLoadsPlaylistsAndOpensTheFirst(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := drain(t, wired(t, lib, st, newFakeAudio()), New(Services{
		Library: lib, Streams: st, Audio: newFakeAudio(),
	}).Init())

	if len(m.Playlists) != 2 || m.Playlists[0].Title != "Liked Music" {
		t.Fatalf("playlists = %+v", m.Playlists)
	}
	if len(m.Tracks) != 2 || m.Tracks[0].Title != "Alpha" {
		t.Fatalf("tracks = %+v", m.Tracks)
	}
	if m.loading {
		t.Error("still reporting itself as loading")
	}
	// The first row is warmed so pressing play on it is instant.
	if len(st.prefetched) == 0 || st.prefetched[0] != "a" {
		t.Errorf("prefetched = %v, want the first row", st.prefetched)
	}
}

func TestMovingToATabLoadsIt(t *testing.T) {
	lib := library()
	lib.tracks["PL1"] = []ytm.Track{{VideoID: "c", Title: "Gamma"}}
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}, {ID: "PL1", Title: "Favorites"}}

	next, cmd := m.Update(keyPress("l"))
	m = drain(t, next.(Model), cmd)

	if m.tabCursor != 1 {
		t.Fatalf("tab cursor = %d", m.tabCursor)
	}
	if len(m.Tracks) != 1 || m.Tracks[0].Title != "Gamma" {
		t.Fatalf("tracks = %+v", m.Tracks)
	}
}

// A tab already visited comes back without asking the server again.
func TestGoingBackToATabIsServedFromMemory(t *testing.T) {
	lib := library()
	lib.tracks["PL1"] = []ytm.Track{{VideoID: "c", Title: "Gamma"}}
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}, {ID: "PL1", Title: "Favorites"}}

	for _, key := range []string{"l", "h", "l"} {
		next, cmd := m.Update(keyPress(key))
		m = drain(t, next.(Model), cmd)
	}
	requests := 0
	for _, id := range lib.askedFor {
		if id == "PL1" {
			requests++
		}
	}
	if requests != 1 {
		t.Errorf("asked for PL1 %d times, want once: %v", requests, lib.askedFor)
	}
	if len(m.Tracks) != 1 || m.Tracks[0].Title != "Gamma" {
		t.Fatalf("tracks = %+v", m.Tracks)
	}
}

func TestEnterOnATrackResolvesAndPlays(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)

	if len(st.resolved) != 1 || st.resolved[0] != "a" {
		t.Fatalf("resolved = %v", st.resolved)
	}
	if len(au.loaded) != 1 || au.loaded[0] != "https://stream/a" {
		t.Fatalf("loaded = %v", au.loaded)
	}
	if m.NowPlaying != "A — Alpha" {
		t.Errorf("now playing = %q", m.NowPlaying)
	}
	if m.Length != 3*time.Minute {
		t.Errorf("length = %v, want the resolved stream's", m.Length)
	}
	// The following track is warmed so a skip costs nothing.
	if len(st.prefetched) == 0 || st.prefetched[len(st.prefetched)-1] != "b" {
		t.Errorf("prefetched = %v, want the next track", st.prefetched)
	}
}

// A failed resolve must say so rather than pretend to play.
func TestAResolveFailureSurfaces(t *testing.T) {
	lib, st := library(), &fakeStreams{err: errors.New("yt-dlp exploded")}
	au := newFakeAudio()
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)

	if m.Err == nil {
		t.Fatal("no error reported")
	}
	if len(au.loaded) != 0 {
		t.Errorf("loaded %v despite the failure", au.loaded)
	}
}

func TestRatingReachesTheAPI(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := wired(t, lib, st, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(keyPress("+"))
	m = drain(t, next.(Model), cmd)

	if m.Tracks[0].Rating != RatingUp {
		t.Errorf("row rating = %v", m.Tracks[0].Rating)
	}
	if len(lib.rated) != 1 || lib.rated[0].videoID != "a" || lib.rated[0].rating != ytm.RatingUp {
		t.Fatalf("rated = %+v", lib.rated)
	}
}

// Pressing the same thumb twice clears it, and that reaches the API too.
func TestRatingTwiceClearsItRemotely(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := wired(t, lib, st, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(keyPress("+"))
	m = drain(t, next.(Model), cmd)
	next, cmd = m.Update(keyPress("+"))
	m = drain(t, next.(Model), cmd)

	if m.Tracks[0].Rating != RatingNone {
		t.Errorf("row rating = %v, want cleared", m.Tracks[0].Rating)
	}
	if len(lib.rated) != 2 || lib.rated[1].rating != ytm.RatingNone {
		t.Fatalf("rated = %+v", lib.rated)
	}
}

// The row changes before the call, so a failure has to put it back.
func TestAFailedRatingIsPutBack(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.rateErr = errors.New("no")
	m := wired(t, lib, st, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.Tracks[0].Rating = RatingDown

	next, cmd := m.Update(keyPress("+"))
	if next.(Model).Tracks[0].Rating != RatingUp {
		t.Fatal("the row should change immediately, before the call")
	}
	m = drain(t, next.(Model), cmd)

	if m.Tracks[0].Rating != RatingDown {
		t.Errorf("rating = %v, want the previous value back", m.Tracks[0].Rating)
	}
	if m.Err == nil {
		t.Error("the failure was not reported")
	}
}

func TestSearchReplacesTheTable(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := wired(t, lib, st, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, _ := m.Update(keyPress("/"))
	m = next.(Model)
	for _, k := range []string{"x", "t", "a", "l"} {
		next, _ = m.Update(keyPress(k))
		m = next.(Model)
	}
	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)

	if m.Searching {
		t.Error("still in search mode after enter")
	}
	if len(m.Tracks) != 1 || m.Tracks[0].Title != "Found" {
		t.Fatalf("tracks = %+v", m.Tracks)
	}
	if got, _ := m.SelectedPlaylist(); got.Title != "xtal" {
		t.Errorf("front tab = %q, want the search", got.Title)
	}
	if len(lib.askedFor) == 0 || lib.askedFor[len(lib.askedFor)-1] != "search:xtal" {
		t.Errorf("asked for %v", lib.askedFor)
	}
}

// mpv reporting the end of a file is what advances a playlist.
func TestTrackEndPlaysTheNextOne(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	au := newFakeAudio(player.Event{Name: "eof-reached", Data: true})
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.playingID = "a"

	m = drain(t, m, m.watchEvents())

	if len(st.resolved) == 0 || st.resolved[0] != "b" {
		t.Fatalf("resolved = %v, want the following track", st.resolved)
	}
	if m.playingID != "b" {
		t.Errorf("playing = %q, want b", m.playingID)
	}
}

// The last track ending is the end, not a crash.
func TestTrackEndAtTheEndOfTheListStops(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	au := newFakeAudio(player.Event{Name: "eof-reached", Data: true})
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.playingID = "b"

	m = drain(t, m, m.watchEvents())

	if len(st.resolved) != 0 {
		t.Fatalf("resolved %v after the last track", st.resolved)
	}
}

func TestPlaybackEventsUpdateTheBar(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio(
		player.Event{Name: "time-pos", Data: 12.5},
		player.Event{Name: "duration", Data: 200.0},
		player.Event{Name: "pause", Data: true},
	))
	m = drain(t, m, m.watchEvents())

	if m.Position != 12500*time.Millisecond {
		t.Errorf("position = %v", m.Position)
	}
	if m.Length != 200*time.Second {
		t.Errorf("length = %v", m.Length)
	}
	if !m.Paused {
		t.Error("not paused")
	}
}

// Cursor movement arms a delayed prefetch rather than resolving at once:
// holding a cursor key down a playlist would otherwise start a yt-dlp
// process per row.
func TestMovingTheCursorPrefetches(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := wired(t, lib, st, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(keyPress("down"))
	drain(t, next.(Model), cmd)

	if len(st.prefetched) != 1 || st.prefetched[0] != "b" {
		t.Fatalf("prefetched = %v", st.prefetched)
	}
}

// Moving between tabs must not resolve anything: those are playlists.
func TestTabMovementDoesNotPrefetch(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM"}, {ID: "PL1"}}
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(keyPress("l"))
	drain(t, next.(Model), cmd)

	if len(st.prefetched) != 0 {
		t.Fatalf("prefetched %v from a tab move", st.prefetched)
	}
}

func TestSpaceTogglesPause(t *testing.T) {
	au := newFakeAudio()
	m := wired(t, library(), &fakeStreams{}, au)

	next, cmd := m.Update(keyPress(" "))
	drain(t, next.(Model), cmd)

	if au.toggles != 1 {
		t.Fatalf("toggles = %d", au.toggles)
	}
}

// A signed-out session answers with an error rather than an empty library;
// it has to be visible, not swallowed.
func TestAFailedFetchIsReported(t *testing.T) {
	lib := &fakeLibrary{err: ytm.ErrSignedOut}
	m := drain(t, wired(t, lib, &fakeStreams{}, newFakeAudio()), New(Services{Library: lib}).Init())

	if m.Err == nil {
		t.Fatal("no error reported")
	}
	if m.loading {
		t.Error("still reporting itself as loading")
	}
	if !errors.Is(m.Err, ytm.ErrSignedOut) {
		t.Errorf("err = %v", m.Err)
	}
}

// The view tests build models with no services at all; that must be inert
// rather than a nil dereference.
func TestZeroServicesIsInert(t *testing.T) {
	m := New(Services{})
	if cmd := m.Init(); cmd != nil {
		t.Error("a model with no services should start nothing")
	}
	m.Tracks = []Track{{VideoID: "a", Title: "Alpha"}}
	for _, k := range []string{"enter", "+", "-", " ", "down", "/"} {
		next, cmd := m.Update(keyPress(k))
		m = next.(Model)
		if cmd != nil {
			t.Errorf("key %q produced a command with no services", k)
		}
	}
}

// keyPress builds the keystroke the runtime would deliver.
func keyPress(k string) tea.KeyPressMsg {
	switch k {
	case "tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})
	case "up":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyUp})
	case "down":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
	default:
		return tea.KeyPressMsg(tea.Key{Code: rune(k[0]), Text: k})
	}
}
