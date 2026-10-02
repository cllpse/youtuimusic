package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
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
	// next is handed out with every listing, so a test can stage a second
	// page; morePage is what More then returns.
	next     ytm.Continuation
	morePage []ytm.Track
	moreErr  error

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

func (f *fakeLibrary) PlaylistTracks(_ context.Context, id string) (ytm.Page, error) {
	f.askedFor = append(f.askedFor, id)
	if f.err != nil {
		return ytm.Page{}, f.err
	}
	return f.pageFor(id), nil
}

func (f *fakeLibrary) AlbumTracks(_ context.Context, id string) (ytm.Page, error) {
	f.askedFor = append(f.askedFor, "album:"+id)
	if f.err != nil {
		return ytm.Page{}, f.err
	}
	return f.pageFor(id), nil
}

func (f *fakeLibrary) ArtistPage(_ context.Context, id string) (ytm.Page, error) {
	f.askedFor = append(f.askedFor, "artist:"+id)
	if f.err != nil {
		return ytm.Page{}, f.err
	}
	return f.pageFor(id), nil
}

func (f *fakeLibrary) Search(_ context.Context, query string) (ytm.Page, error) {
	f.askedFor = append(f.askedFor, "search:"+query)
	if f.err != nil {
		return ytm.Page{}, f.err
	}
	return ytm.Page{Tracks: f.results, Next: f.next}, nil
}

// Radio answers with whatever was staged for the seed's mix, or with the seed
// and one track after it, which is the shape a mix has: the track you started
// from and what follows it.
func (f *fakeLibrary) Radio(_ context.Context, videoID string) (ytm.Page, error) {
	f.askedFor = append(f.askedFor, "radio:"+videoID)
	if f.err != nil {
		return ytm.Page{}, f.err
	}
	if staged, ok := f.tracks[ytm.RadioID(videoID)]; ok {
		return ytm.Page{Tracks: staged, Next: f.next}, nil
	}
	return ytm.Page{Tracks: []ytm.Track{
		{VideoID: videoID, Title: "Seed", Artist: "A"},
		{VideoID: videoID + "-mix", Title: "Mixed", Artist: "B"},
	}, Next: f.next}, nil
}

// More hands back whatever was staged for the next page, once.
func (f *fakeLibrary) More(context.Context, ytm.Continuation) (ytm.Page, error) {
	f.askedFor = append(f.askedFor, "more")
	if f.moreErr != nil {
		return ytm.Page{}, f.moreErr
	}
	page := ytm.Page{Tracks: f.morePage}
	f.morePage, f.next = nil, ytm.Continuation{}
	return page, nil
}

// pageFor is a listing, carrying the staged continuation so that a test can
// ask for a second page.
func (f *fakeLibrary) pageFor(id string) ytm.Page {
	return ytm.Page{Tracks: f.tracks[id], Next: f.next}
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
	forgotten  []string
	err        error
}

func (f *fakeStreams) Resolve(_ context.Context, id string) (stream.Track, error) {
	f.resolved = append(f.resolved, id)
	if f.err != nil {
		return stream.Track{}, f.err
	}
	return stream.Track{VideoID: id, URL: "https://stream/" + id, Duration: 3 * time.Minute}, nil
}

func (f *fakeStreams) Prefetch(id string) {
	f.prefetched = append(f.prefetched, id)
}

func (f *fakeStreams) Forget(id string) {
	f.forgotten = append(f.forgotten, id)
}

type fakeAudio struct {
	loaded   []string
	toggles  int
	paused   []bool
	pauseErr error
	seeks    []float64
	events   chan player.Event
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

func (f *fakeAudio) Load(url string) error { f.loaded = append(f.loaded, url); return nil }
func (f *fakeAudio) TogglePause() error    { f.toggles++; return nil }
func (f *fakeAudio) Seek(s float64) error  { f.seeks = append(f.seeks, s); return nil }
func (f *fakeAudio) SetPaused(p bool) error {
	f.paused = append(f.paused, p)
	return f.pauseErr
}
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
	if m.playing.Title != "Alpha" {
		t.Errorf("now playing = %q", m.playing.Title)
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

// Search fills the popover and leaves everything behind it alone.
func TestSearchFillsThePopover(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := wired(t, lib, st, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])
	beneath := len(m.Tracks)

	next, _ := m.Update(keyPress("/"))
	m = next.(Model)
	for _, k := range []string{"x", "t", "a", "l"} {
		next, _ = m.Update(keyPress(k))
		m = next.(Model)
	}
	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)

	// The keys stay in the box: results arrive with nothing chosen and down
	// is what goes into them.
	if !m.detour.typing {
		t.Error("enter took the keys out of the box")
	}
	if m.detour.cursor != noRow {
		t.Errorf("a result was chosen for the reader: %d", m.detour.cursor)
	}
	if len(m.detour.tracks) != 1 || m.detour.tracks[0].Title != "Found" {
		t.Fatalf("popover tracks = %+v", m.detour.tracks)
	}
	if len(m.Tracks) != beneath {
		t.Errorf("the list underneath changed to %d rows", len(m.Tracks))
	}
	if m.tabCount() != len(m.Playlists) {
		t.Errorf("the tab row grew to %d", m.tabCount())
	}
	if len(lib.askedFor) == 0 || lib.askedFor[len(lib.askedFor)-1] != "search:xtal" {
		t.Errorf("asked for %v", lib.askedFor)
	}
}

// mpv reporting the end of a file is what advances a playlist.
func TestTrackEndPlaysTheNextOne(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	au := newFakeAudio(player.Event{Name: player.EndFile, Data: "eof"})
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.playing = m.Tracks[0]

	m = drain(t, m, m.watchEvents())

	if len(st.resolved) == 0 || st.resolved[0] != "b" {
		t.Fatalf("resolved = %v, want the following track", st.resolved)
	}
	if m.playing.VideoID != "b" {
		t.Errorf("playing = %q, want b", m.playing.VideoID)
	}
}

// The last track ending is the end, not a crash.
func TestTrackEndAtTheEndOfTheListStops(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	au := newFakeAudio(player.Event{Name: player.EndFile, Data: "eof"})
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.playing = m.Tracks[1]

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

func TestSpaceTogglesPauseWhileSomethingPlays(t *testing.T) {
	lib, au := library(), newFakeAudio()
	m := wired(t, lib, &fakeStreams{}, au)
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.playing = m.Tracks[0]

	next, cmd := m.Update(keyPress(" "))
	drain(t, next.(Model), cmd)

	if au.toggles != 1 {
		t.Fatalf("toggles = %d", au.toggles)
	}
}

// With nothing playing there is nothing to pause, so it is a play button.
func TestSpaceStartsTheHighlightedTrack(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(keyPress(" "))
	m = drain(t, next.(Model), cmd)

	if au.toggles != 0 {
		t.Errorf("toggled pause with nothing playing")
	}
	if len(au.loaded) != 1 || m.playing.VideoID != "a" {
		t.Fatalf("loaded %v, playing %q", au.loaded, m.playing.VideoID)
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
	// It still asks the terminal what colour it is — that is about the
	// display and not about the services — but nothing else. Draining it
	// must not reach a service that is not there.
	m = drain(t, m, m.Init())
	if m.Err != nil {
		t.Errorf("startup with no services failed: %v", m.Err)
	}
	m.Tracks = []Track{{VideoID: "a", Title: "Alpha"}}
	for _, k := range []string{"enter", "+", "-", " ", "down", "/"} {
		next, cmd := m.Update(keyPress(k))
		m = next.(Model)
		// A command is allowed as long as it does not reach a service: a
		// search focuses a text input, which blinks its cursor, and that is
		// display-only like the colour request.
		if cmd != nil {
			m = drain(t, m, cmd)
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

// mpv keeps its pause flag across a load. Starting a track while paused
// left the screen claiming it played while nothing came out, and only
// toggling pause twice got it going.
func TestPlayingClearsPause(t *testing.T) {
	lib, st, au := library(), &fakeStreams{}, newFakeAudio()
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])

	// Play, pause, then pick the next track.
	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	next, cmd = m.Update(keyPress(" "))
	m = drain(t, next.(Model), cmd)
	m.Paused = true

	next, cmd = m.Update(keyPress("j"))
	m = drain(t, next.(Model), cmd)
	next, cmd = m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)

	if len(au.loaded) != 2 {
		t.Fatalf("loaded %v, want two tracks", au.loaded)
	}
	if len(au.paused) == 0 || au.paused[len(au.paused)-1] {
		t.Errorf("SetPaused calls were %v; the new track was left paused", au.paused)
	}
	if m.Paused {
		t.Error("the model still reports paused")
	}
}

// If the player cannot be unpaused, that is a silent track, so say so.
func TestAFailureToUnpauseIsReported(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	au := newFakeAudio()
	au.pauseErr = errors.New("socket closed")
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])

	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)

	if m.Err == nil {
		t.Fatal("no error reported")
	}
	if !strings.Contains(m.Err.Error(), "socket closed") {
		t.Errorf("err = %v, want the failure to unpause", m.Err)
	}
}

// Reaching the end of a list is the same gesture as asking for more of it.
func TestWalkingOntoTheLastRowFetchesTheNextPage(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = fromUI(rows(3))
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}
	lib.morePage = fromUI([]Track{{VideoID: "z", Title: "from page two"}})

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)

	if !m.more.More() {
		t.Fatal("the list does not know there is more")
	}
	if m.rowCount() != len(m.Tracks)+1 {
		t.Fatalf("row count = %d, want one more than the tracks", m.rowCount())
	}

	// Walk down past the last track and onto the offer.
	for range len(m.Tracks) {
		next, cmd := m.Update(keyPress("j"))
		m = drain(t, next.(Model), cmd)
	}
	// The page has arrived by now, so the cursor is on a track again — the
	// row it landed on turned into one.
	if len(m.Tracks) != 4 || m.Tracks[3].Title != "from page two" {
		t.Fatalf("tracks = %+v", m.Tracks)
	}
	// That was the last page, so the offer is gone.
	if m.more.More() {
		t.Error("it still offers another page")
	}
	if m.loadingMore {
		t.Error("still marked as loading")
	}
}

// Clicking the offer does the same thing as walking onto it.
func TestClickingTheOfferFetchesTheNextPage(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = fromUI(rows(3))
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}
	lib.morePage = fromUI([]Track{{VideoID: "z", Title: "from page two"}})

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)

	next, cmd := m.Update(click(trackX, trackRow(len(m.Tracks))))
	m = drain(t, next.(Model), cmd)

	if len(m.Tracks) != 4 {
		t.Fatalf("tracks = %+v", m.Tracks)
	}
}

// Two moves onto the offer must not send two requests.
func TestTheNextPageIsOnlyAskedForOnce(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = fromUI(rows(2))
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)

	// Land on the offer without letting the reply arrive.
	m.trackCursor = len(m.Tracks)
	first, _ := m.fetchMore(false)
	m = first.(Model)
	if !m.loadingMore {
		t.Fatal("the first ask did not start")
	}
	before := len(lib.askedFor)
	second, cmd := m.fetchMore(false)
	if cmd != nil {
		t.Error("a second ask went out while the first was in flight")
	}
	_ = second
	if len(lib.askedFor) != before {
		t.Errorf("asked for %v", lib.askedFor)
	}
}

// The load-more row carries the same spinner as everything else, and it has
// to animate. Scrolling onto it used to mark loadingMore without ever arming
// the tick loop, so the spinner was drawn frozen.
func TestTheLoadMoreRowKeepsTheSpinnerMoving(t *testing.T) {
	m := sample()
	m.more = ytm.Continuation{Endpoint: "browse", Token: "more"}
	m.loadingMore = true

	before := m.spin.View()
	next, cmd := m.Update(spinner.TickMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("the spinner stopped while a page was still coming")
	}
	if m.spin.View() == before {
		t.Error("the spinner did not advance")
	}
}

// Asking for the next page has to arm the spinner, not just wait for one
// already running: the loop is started by whichever wait begins first.
func TestAskingForTheNextPageArmsTheSpinner(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = fromUI(rows(2))
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)

	m.trackCursor = len(m.Tracks)
	// wired dropped the commands its resize produced, the spinner's first
	// tick among them, so the loop the model thinks is running is not.
	m.spinning = false
	next, cmd := m.fetchMore(false)
	m = next.(Model)
	if !m.loadingMore {
		t.Fatal("the ask did not start")
	}
	if cmd == nil {
		t.Fatal("asking for a page produced no command")
	}

	// Update arms the spinner on the way out of whatever message began the
	// wait, so it is looked for there.
	type nothing struct{}
	armed, cmd := m.Update(nothing{})
	if _, ticked := spinnerTick(cmd); !ticked || !armed.(Model).spinning {
		t.Error("the next-page wait did not arm the spinner")
	}
}

// A page that fails to arrive says so and lets the offer be taken again.
func TestAFailedPageIsReported(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = fromUI(rows(2))
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}
	lib.moreErr = errors.New("no")

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)

	m.trackCursor = len(m.Tracks)
	// wired dropped the commands its resize produced, the spinner's first
	// tick among them, so the loop the model thinks is running is not.
	m.spinning = false
	next, cmd := m.fetchMore(false)
	m = drain(t, next.(Model), cmd)

	if m.Err == nil {
		t.Fatal("no error reported")
	}
	if m.loadingMore {
		t.Error("still marked as loading, so the offer can never be taken again")
	}
	if !m.more.More() {
		t.Error("the offer was thrown away on a failure")
	}
}

// Scrolling to the end of a list asks for more of it, as walking to the end
// does. A reader using the wheel should not have to reach for the keyboard.
func TestScrollingToTheEndFetchesTheNextPage(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = fromUI(rows(40))
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}
	lib.morePage = fromUI([]Track{{VideoID: "z", Title: "from page two"}})

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)
	before := len(m.Tracks)

	for range 40 {
		if len(m.Tracks) != before {
			break
		}
		next, cmd := m.Update(wheel(trackX, trackRow(0), tea.MouseWheelDown))
		m = drain(t, next.(Model), cmd)
	}

	if len(m.Tracks) != before+1 {
		t.Fatalf("tracks = %d, want the next page appended", len(m.Tracks))
	}
	// The selection stayed put, as the wheel always leaves it.
	if m.trackCursor != 0 {
		t.Errorf("the wheel moved the cursor to %d", m.trackCursor)
	}
}

// And the popover pages on the wheel too.
func TestScrollingThePopoverFetchesItsNextPage(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["MPREbCherry"] = fromUI(rows(30))
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}
	lib.morePage = fromUI([]Track{{VideoID: "z", Title: "from page two"}})

	m := wired(t, lib, st, newFakeAudio())
	m.Tracks = fromAPI([]ytm.Track{{VideoID: "a", Title: "Poly", AlbumID: "MPREbCherry"}})
	next, cmd := m.Update(rightClick(trackX, trackRow(0)))
	m = drain(t, next.(Model), cmd)
	x, y := rowAt(m, menuAlbum)
	next, cmd = m.Update(click(x, y))
	m = drain(t, next.(Model), cmd)
	before := len(m.detour.tracks)

	inside := m.width / 2
	_, my, _, _ := m.modalBounds()
	for range 40 {
		if len(m.detour.tracks) != before {
			break
		}
		next, cmd := m.Update(wheel(inside, my+modalHeader+headerRows+1, tea.MouseWheelDown))
		m = drain(t, next.(Model), cmd)
	}

	if len(m.detour.tracks) != before+1 {
		t.Fatalf("popover tracks = %d, want the next page", len(m.detour.tracks))
	}
	if len(m.Tracks) != 1 {
		t.Errorf("the page landed on the list behind it: %d rows", len(m.Tracks))
	}
}

// Sorting half a list puts the wrong rows at the top, so a sort fetches the
// rest of the listing and stays sorted as it arrives.
func TestSortingCompletesTheListing(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = []ytm.Track{{VideoID: "m", Title: "Middle"}}
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}
	lib.morePage = []ytm.Track{{VideoID: "a", Title: "Aardvark"}}

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)
	if len(m.Tracks) != 1 || !m.more.More() {
		t.Fatalf("expected one row and more to come: %d rows", len(m.Tracks))
	}

	next, cmd := m.Update(keyPress("s")) // sort by title
	m = drain(t, next.(Model), cmd)

	if len(m.Tracks) != 2 {
		t.Fatalf("the rest was not fetched: %+v", m.Tracks)
	}
	// And the row that arrived second sorts to the top.
	if m.Tracks[0].Title != "Aardvark" {
		t.Errorf("order is %q then %q", m.Tracks[0].Title, m.Tracks[1].Title)
	}
	if m.more.More() {
		t.Error("it stopped before the end")
	}
}

// Unsorted, it does not: the rest is fetched when it is reached.
func TestAnUnsortedListingIsLeftAlone(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = fromUI(rows(3))
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}
	lib.morePage = fromUI(rows(3))

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)

	if len(m.Tracks) != 3 {
		t.Fatalf("fetched ahead without being asked: %d rows", len(m.Tracks))
	}
	for _, asked := range lib.askedFor {
		if asked == "more" {
			t.Error("asked for another page unprompted")
		}
	}
}

// Clearing the sort puts the listing back in the order it arrived in, which
// means that order has to be kept.
func TestClearingTheSortRestoresTheArrivalOrder(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = []ytm.Track{
		{VideoID: "z", Title: "Zulu"},
		{VideoID: "a", Title: "Alpha"},
	}

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked Music"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)
	if m.Tracks[0].Title != "Zulu" {
		t.Fatalf("arrived as %q first", m.Tracks[0].Title)
	}

	next, cmd := m.Update(keyPress("s"))
	m = drain(t, next.(Model), cmd)
	if m.Tracks[0].Title != "Alpha" {
		t.Fatalf("sorting gave %q first", m.Tracks[0].Title)
	}

	// Round the cycle and back to unsorted.
	for range 3 {
		next, cmd = m.Update(keyPress("s"))
		m = drain(t, next.(Model), cmd)
	}
	if m.sort.by != sortNone {
		t.Fatalf("cycled to %v", m.sort.by)
	}
	if m.Tracks[0].Title != "Zulu" {
		t.Errorf("the arrival order was not restored: %q first", m.Tracks[0].Title)
	}
}

// Going back to a tab has to bring its thread with it, or the listing can
// never be paged past what was read the first time.
func TestACachedTabKeepsItsPlaceInTheListing(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = fromUI(rows(2))
	lib.tracks["PL1"] = fromUI(rows(2))
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked"}, {ID: "PL1", Title: "Favorites"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)
	if !m.more.More() {
		t.Fatal("the first read has no thread to keep")
	}

	// Away and back again.
	for _, key := range []string{"l", "h"} {
		next, cmd := m.Update(keyPress(key))
		m = drain(t, next.(Model), cmd)
	}
	if !m.more.More() {
		t.Error("the thread was lost coming back, so the rest can never be read")
	}
	if m.rowCount() != len(m.Tracks)+1 {
		t.Errorf("no offer of the rest: %d rows for %d tracks", m.rowCount(), len(m.Tracks))
	}
}

// A tab whose load is still on its way when a popover opens over it is still
// loaded. The load used to fetch whatever was in front when it came due, and
// the answer was only put up if it was the thing in front: with a search open
// meanwhile, the tab was never fetched, or fetched and dropped, and it sat on
// the loader — with the spinner redrawing the screen — until it was left.
func TestATabLoadingUnderAPopoverStillLoads(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := wired(t, lib, st, newFakeAudio())

	next, cmd := m.Update(playlistsMsg(lib.playlists))
	// The search opens before the tab's load is due, and the load lands with
	// it still open.
	m = press(next.(Model), "/")
	m = drain(t, m, cmd)

	if !slices.Contains(lib.askedFor, "LM") {
		t.Fatalf("the tab was never fetched: asked for %v", lib.askedFor)
	}
	if len(m.Tracks) != 2 || m.showingID != "LM" {
		t.Fatalf("the tab's answer was not put up under the popover: %d rows, showing %q",
			len(m.Tracks), m.showingID)
	}
	if m.loading || m.busy() {
		t.Error("still waiting with nothing on its way")
	}

	m = press(m, "esc")
	if out := plain(m.View().Content); strings.Contains(out, loaderLabel) || !strings.Contains(out, "Alpha") {
		t.Errorf("closing the popover found the tab still loading:\n%s", out)
	}
}

// Moving to a tab already in memory cancels the load armed for the one before
// it. Left to come due, it fetched the tab in front again — over what was
// already there.
func TestMovingToAKeptTabCancelsTheLoadBeforeIt(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked"}, {ID: "PL1", Title: "Favorites"}}
	m.cache["PL1"] = cached{tracks: []Track{{VideoID: "p1", Title: "Kept"}}}

	opened, cmd := m.showTab()
	m, _ = opened.selectTab(1)
	m = drain(t, m, cmd)

	if len(lib.askedFor) != 0 {
		t.Errorf("a load came due after its tab was left: asked for %v", lib.askedFor)
	}
	if m.loading || len(m.Tracks) != 1 || m.Tracks[0].VideoID != "p1" {
		t.Errorf("the kept tab is not what is shown: loading=%v %+v", m.loading, m.Tracks)
	}
}

// A next page belongs to the listing that asked for it. Moving to another tab
// while it was on its way put one playlist's page on the end of another's,
// gave the second the first's place in the listing, and kept both for good.
func TestAPageLandsOnTheListingThatAskedForIt(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.tracks["LM"] = fromUI(rows(2))
	lib.next = ytm.Continuation{Endpoint: "browse", Token: "more"}
	lib.morePage = fromUI([]Track{{VideoID: "z", Title: "from page two"}})

	m := wired(t, lib, st, newFakeAudio())
	m.Playlists = []Playlist{{ID: "LM", Title: "Liked"}, {ID: "PL1", Title: "Favorites"}}
	opened, cmd := m.showTab()
	m = drain(t, opened, cmd)
	m.cache["PL1"] = cached{tracks: []Track{{VideoID: "p1", Title: "Kept"}}}

	m.trackCursor = len(m.Tracks)
	// wired dropped the commands its resize produced, the spinner's first
	// tick among them, so the loop the model thinks is running is not.
	m.spinning = false
	next, cmd := m.fetchMore(false)
	m, _ = next.(Model).selectTab(1)
	m = drain(t, m, cmd)

	if len(m.Tracks) != 1 || m.Tracks[0].VideoID != "p1" {
		t.Errorf("the page landed on the tab in front: %+v", m.Tracks)
	}
	if kept := m.cache["PL1"].tracks; len(kept) != 1 {
		t.Errorf("the page was kept as the other tab's: %+v", kept)
	}
	if m.more.More() {
		t.Error("the tab in front took the other's place in its listing")
	}
	if kept := m.cache["LM"].tracks; len(kept) != 3 || kept[2].VideoID != "z" {
		t.Errorf("the tab that asked did not keep its page: %+v", kept)
	}
}

// A listing that runs out while another tab is in front pages on in the
// listing that was playing. It used to ask for the page of the tab in front,
// which had nothing in it to follow the track with, and playback stopped.
func TestPlaybackPagesOnInItsOwnListing(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.morePage = fromUI([]Track{{VideoID: "a2", Title: "Next in A"}})
	au := newFakeAudio(player.Event{Name: player.EndFile, Data: "eof"})

	m := wired(t, lib, st, au)
	m.Playlists = []Playlist{{ID: "A", Title: "A"}, {ID: "B", Title: "B"}}
	m.cache["A"] = cached{
		tracks: []Track{{VideoID: "a1", Title: "Last in A"}},
		next:   ytm.Continuation{Endpoint: "browse", Token: "more"},
	}
	m.cache["B"] = cached{tracks: []Track{{VideoID: "b1", Title: "Only in B"}}}
	m, _ = m.showTab()
	started, _ := m.start(m.Tracks[0])
	m, _ = started.(Model).selectTab(1)

	m = drain(t, m, m.watchEvents())

	if m.playing.VideoID != "a2" {
		t.Fatalf("playing %q after the end of A's page, want A's next track", m.playing.VideoID)
	}
	if len(m.Tracks) != 1 || len(m.cache["B"].tracks) != 1 {
		t.Errorf("A's page landed on B: %+v", m.Tracks)
	}
}

// A search result plays on into the next result. A search had no id, so a
// track started from one was started from nowhere, and what followed it was
// looked for in the list underneath.
func TestASearchResultPlaysOnIntoTheNext(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	lib.results = []ytm.Track{{VideoID: "z1", Title: "One"}, {VideoID: "z2", Title: "Two"}}
	au := newFakeAudio(player.Event{Name: player.EndFile, Data: "eof"})
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])

	m = press(m, "/", "x")
	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	m = press(m, "down", "enter")
	if m.playing.VideoID != "z1" {
		t.Fatalf("playing %q, want the first result", m.playing.VideoID)
	}

	m = drain(t, m, m.watchEvents())
	if m.playing.VideoID != "z2" {
		t.Errorf("playing %q after the first result, want the second", m.playing.VideoID)
	}
}

// An answer to a query the reader has since replaced is not the answer. It
// used to be put up whenever it landed, over the newer results.
func TestAnOlderSearchDoesNotOverwriteANewerOne(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	m = press(m, "/")
	m.detour.searched = "newer"
	m.detour.loading = true

	m = drain(t, m, func() tea.Msg {
		return searchMsg{query: "older", page: ytm.Page{Tracks: []ytm.Track{{VideoID: "old"}}}}
	})
	if len(m.detour.tracks) != 0 {
		t.Errorf("the older answer was put up: %+v", m.detour.tracks)
	}
	if !m.detour.loading {
		t.Error("the older answer ended the newer one's wait")
	}

	m = drain(t, m, func() tea.Msg {
		return searchMsg{query: "newer", page: ytm.Page{Tracks: []ytm.Track{{VideoID: "new"}}}}
	})
	if len(m.detour.tracks) != 1 || m.detour.tracks[0].VideoID != "new" || m.detour.loading {
		t.Errorf("the newer answer was not put up: %+v", m.detour.tracks)
	}
}

// A popover stepped back to while it is still waiting picks up its answer if
// that arrived while something else was in front of it.
func TestAPopoverSteppedBackToCatchesUp(t *testing.T) {
	m, _, _, _ := menuModel(t)
	m = openVia(t, m, menuAlbum)
	album := m.detour.tab

	m = press(m, "R")
	if !m.detour.loading {
		t.Fatal("the refetch is not waiting")
	}
	refetch := m.fetchTracks(album)
	m, _ = m.enterDetour(Playlist{ID: "UCdaphni", Title: "DAPHNI", kind: tabArtist})
	m = drain(t, m, refetch)

	m, _ = m.leaveDetour()
	if m.detour.tab.ID != album.ID {
		t.Fatalf("stepped back to %q", m.detour.tab.ID)
	}
	if m.detour.loading || len(m.detour.tracks) != 1 {
		t.Errorf("the album is still waiting on an answer it was given: loading=%v %+v",
			m.detour.loading, m.detour.tracks)
	}
}

// mpv going away ends the stream of events, and that is said rather than
// swallowed — once, and without watching a stream that has ended.
func TestThePlayerGoingAwayIsReported(t *testing.T) {
	m := wired(t, library(), &fakeStreams{}, newFakeAudio())
	m.loading = true

	msg := m.watchEvents()()
	next, cmd := m.Update(msg)
	m = next.(Model)
	if !errors.Is(m.Err, errPlayerGone) {
		t.Fatalf("err = %v, want the player gone", m.Err)
	}
	if cmd != nil {
		t.Error("an ended stream was watched again")
	}
	// It is not a fetch, so it ends no wait on one.
	if !m.loading {
		t.Error("the player going away ended a wait on the network")
	}
}

// Positions that would not change the screen do not become messages: the row
// says whole seconds and whole cells, and every message is a frame.
func TestOnlyPositionsTheScreenShowsAreDelivered(t *testing.T) {
	au := newFakeAudio(
		player.Event{Name: player.PropTimePos, Data: 12.2},
		player.Event{Name: player.PropTimePos, Data: 12.6},
		player.Event{Name: player.PropPause, Data: true},
		player.Event{Name: player.PropTimePos, Data: 12.9},
		player.Event{Name: player.PropTimePos, Data: 13.1},
	)
	m := wired(t, library(), &fakeStreams{}, au)
	// Long enough that a cell of the bar is several seconds, so it is the
	// second that decides here.
	m.Length = 1000 * time.Second

	var delivered []player.Event
	cmd := m.watchEvents()
	for cmd != nil {
		msg := cmd()
		if ev, ok := msg.(eventMsg); ok {
			delivered = append(delivered, player.Event(ev))
		}
		next, follow := m.Update(msg)
		m, cmd = next.(Model), follow
	}

	want := []player.Event{
		{Name: player.PropTimePos, Data: 12.2},
		// 12.6 reads 0:12 on the same cells as 12.2.
		{Name: player.PropPause, Data: true}, // anything but a position always goes
		// 12.9 likewise.
		{Name: player.PropTimePos, Data: 13.1},
	}
	if !slices.Equal(delivered, want) {
		t.Errorf("delivered %v, want %v", delivered, want)
	}
	if m.Position != 13100*time.Millisecond {
		t.Errorf("position = %v", m.Position)
	}
}

// A position that moves the bar by a cell is a frame even inside one second:
// on a short track a cell is less than a second long.
func TestAPositionThatMovesTheBarIsDelivered(t *testing.T) {
	au := newFakeAudio(
		player.Event{Name: player.PropTimePos, Data: 12.2},
		player.Event{Name: player.PropTimePos, Data: 12.9},
	)
	m := wired(t, library(), &fakeStreams{}, au)
	m.Length = 20 * time.Second

	first := m.watchEvents()()
	_, cmd := m.Update(first)
	if ev, ok := cmd().(eventMsg); !ok || ev.Data != 12.9 {
		t.Errorf("a position a cell further along was skipped: got %v after %v", ev, first)
	}
}

// A stream mpv refuses is usually a cached URL YouTube has stopped honouring.
// The first refusal forgets it and resolves the track again; the screen must
// not sit at 0:00 claiming to play.
func TestARefusedStreamIsResolvedAgain(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	refused := player.Event{Name: player.EndFile, Data: "error", Err: "loading failed"}
	au := newFakeAudio(refused)
	m := wired(t, lib, st, au)
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.playing = m.Tracks[0]

	m = drain(t, m, m.watchEvents())

	if len(st.forgotten) != 1 || st.forgotten[0] != "a" {
		t.Fatalf("forgotten = %v, want the refused track's URL", st.forgotten)
	}
	if len(st.resolved) != 1 || st.resolved[0] != "a" {
		t.Fatalf("resolved = %v, want the same track again", st.resolved)
	}
	if m.playing.VideoID != "a" || m.Err != nil {
		t.Errorf("playing %q with err %v, want a playing again", m.playing.VideoID, m.Err)
	}
}

// A track refused twice is the track's problem: it is said, not retried for
// ever. Stepped through by hand, because the fake player's stream ends once
// its events run out, and that end is reported over whatever was on screen.
func TestAStreamRefusedTwiceIsReported(t *testing.T) {
	lib, st := library(), &fakeStreams{}
	refused := player.Event{Name: player.EndFile, Data: "error", Err: "loading failed"}
	m := wired(t, lib, st, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.playing = m.Tracks[0]

	next, _ := m.Update(eventMsg(refused)) // the first refusal retries
	m = next.(Model)
	if m.retried != "a" || m.Err != nil {
		t.Fatalf("after one refusal: retried=%q err=%v", m.retried, m.Err)
	}
	// The retry gets as far as playing, and the same track keeps its mark.
	next, _ = m.Update(playingMsg{track: m.playing, length: time.Minute})
	m = next.(Model)
	next, _ = m.Update(eventMsg(refused))
	m = next.(Model)

	if m.Err == nil || !strings.Contains(m.Err.Error(), "loading failed") {
		t.Errorf("err = %v, want mpv's reason on screen", m.Err)
	}
	// Asking for it again earns it another retry.
	again, _ := m.start(m.playing)
	if again.(Model).retried != "" {
		t.Error("playing the track again did not reset its retry")
	}
}

// The block's word through a track's start: LOADING while its stream is
// resolved and while mpv opens it, PLAYING once mpv says it is running, and
// LOADING again if the network stalls it. Paused and stopped are never
// mistaken for a wait.
func TestTheStatusSaysLoadingUntilTheTrackSounds(t *testing.T) {
	lib := library()
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m.Tracks = fromAPI(lib.tracks["LM"])
	m.loading = false // the library is in; only playback is measured here
	word := func(m Model) string { w, _ := m.statusState(); return w }
	step := func(msg tea.Msg) {
		t.Helper()
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	idle := func(core, active bool) {
		step(eventMsg(player.Event{Name: player.PropCoreIdle, Data: core}))
		step(eventMsg(player.Event{Name: player.PropIdleActive, Data: active}))
	}
	idle(true, true) // mpv with nothing loaded

	started, _ := m.start(m.Tracks[0])
	m = started.(Model)
	if w := word(m); w != "LOADING" {
		t.Fatalf("resolving: %q, want LOADING", w)
	}
	step(playingMsg{track: m.Tracks[0], length: time.Minute})
	idle(true, false) // handed to mpv, which is opening it
	if w := word(m); w != "LOADING" {
		t.Fatalf("opening: %q, want LOADING", w)
	}
	idle(false, false)
	if w := word(m); w != "PLAYING" {
		t.Fatalf("sounding: %q, want PLAYING", w)
	}
	idle(true, false) // the cache ran dry
	if w := word(m); w != "LOADING" {
		t.Fatalf("stalled: %q, want LOADING", w)
	}
	step(eventMsg(player.Event{Name: player.PropPause, Data: true}))
	if w := word(m); w != "PAUSED" {
		t.Fatalf("paused: %q, want PAUSED", w)
	}

	// A resolve that fails ends the wait; the error says the rest.
	started, _ = m.start(m.Tracks[1])
	m = started.(Model)
	step(errMsg{errors.New("yt-dlp failed")})
	if m.requested != "" || word(m) != "ERROR" {
		t.Errorf("after a failed resolve: requested=%q word=%q", m.requested, word(m))
	}
}
