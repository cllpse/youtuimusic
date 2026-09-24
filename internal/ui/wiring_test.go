package ui

import (
	"context"
	"errors"
	"strings"
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

	if m.detour.typing {
		t.Error("still typing after enter")
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
