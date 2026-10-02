package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cllpse/youtuimusic/internal/player"
	"github.com/cllpse/youtuimusic/internal/stream"
	"github.com/cllpse/youtuimusic/internal/ytm"
)

// Timeouts bound every call the UI makes, so nothing can wedge the event
// loop. Resolving is slower than an API call because it runs yt-dlp.
const (
	requestTimeout = 30 * time.Second
	resolveTimeout = 60 * time.Second
)

// Library is the part of the YouTube Music client the UI drives. These are
// interfaces so the model can be exercised without a network or an mpv.
type Library interface {
	LibraryPlaylists(ctx context.Context) ([]ytm.Playlist, error)
	PlaylistTracks(ctx context.Context, playlistID string) (ytm.Page, error)
	AlbumTracks(ctx context.Context, browseID string) (ytm.Page, error)
	ArtistPage(ctx context.Context, browseID string) (ytm.Page, error)
	Search(ctx context.Context, query string) (ytm.Page, error)
	Radio(ctx context.Context, videoID string) (ytm.Page, error)
	More(ctx context.Context, from ytm.Continuation) (ytm.Page, error)
	Rate(ctx context.Context, videoID string, r ytm.Rating) error
}

// Streams turns a video id into a playable URL.
type Streams interface {
	Resolve(ctx context.Context, videoID string) (stream.Track, error)
	// Prefetch warms the cache for a track without blocking or reporting.
	Prefetch(videoID string)
	// Forget drops a cached URL that would not play, so the next Resolve
	// asks again.
	Forget(videoID string)
}

// Audio is the running player.
type Audio interface {
	Load(url string) error
	TogglePause() error
	SetPaused(paused bool) error
	Seek(seconds float64) error
	Events() <-chan player.Event
}

// Services are the backends the UI drives. A zero Services yields a model
// that renders what is put into it and talks to nothing, which is what the
// view tests use.
type Services struct {
	Library Library
	Streams Streams
	Audio   Audio
}

// Messages carry the result of everything that happens off the event loop.
type (
	playlistsMsg []ytm.Playlist
	tracksMsg    struct {
		id   string // the tab these belong to
		page ytm.Page
	}
	searchMsg struct {
		query string
		page  ytm.Page
	}
	// moreMsg is the next page of a listing. Only one can be waiting at a
	// time, but the reader does not wait with it: by the time it lands the
	// tab or the popover that asked may have been swapped for another, so it
	// names the listing it belongs to. inDetour says which of the two lists
	// asked, for the rare id the main list and a popover could share.
	moreMsg struct {
		id       string
		page     ytm.Page
		inDetour bool
		// autoplay is set when the page was fetched to keep a track that ran
		// out going. The arrival then advances instead of just appending.
		autoplay bool
		err      error
	}
	ratedMsg struct {
		videoID string
		// applied is what was asked for, previous what to put back on a
		// failure.
		applied  Rating
		previous Rating
		err      error
	}
	playingMsg struct {
		track  Track
		length time.Duration
	}
	eventMsg player.Event
	errMsg   struct{ err error }
)

// api maps the UI's thumbs state onto the client's.
func (r Rating) api() ytm.Rating {
	switch r {
	case RatingUp:
		return ytm.RatingUp
	case RatingDown:
		return ytm.RatingDown
	default:
		return ytm.RatingNone
	}
}

// fromAPI converts a client track into a row.
func fromAPI(ts []ytm.Track) []Track {
	out := make([]Track, 0, len(ts))
	for _, t := range ts {
		out = append(out, Track{
			VideoID:  t.VideoID,
			Title:    t.Title,
			Artist:   t.Artist,
			Duration: t.Duration,
			Rating:   fromAPIRating(t.Rating),
			Album:    t.Album,
			AlbumID:  t.AlbumID,
			ArtistID: t.ArtistID,
		})
	}
	return out
}

func fromAPIRating(r ytm.Rating) Rating {
	switch r {
	case ytm.RatingUp:
		return RatingUp
	case ytm.RatingDown:
		return RatingDown
	default:
		return RatingNone
	}
}

func (m Model) fetchPlaylists() tea.Cmd {
	lib := m.services.Library
	if lib == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		pls, err := lib.LibraryPlaylists(ctx)
		if err != nil {
			return errMsg{err}
		}
		return playlistsMsg(pls)
	}
}

func (m Model) fetchTracks(p Playlist) tea.Cmd {
	lib := m.services.Library
	if lib == nil || p.ID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		var (
			page ytm.Page
			err  error
		)
		switch p.kind {
		case tabAlbum:
			page, err = lib.AlbumTracks(ctx, p.ID)
		case tabArtist:
			page, err = lib.ArtistPage(ctx, p.ID)
		case tabMix:
			page, err = lib.Radio(ctx, ytm.RadioSeed(p.ID))
		default:
			page, err = lib.PlaylistTracks(ctx, p.ID)
		}
		if err != nil {
			return errMsg{fmt.Errorf("%s: %w", p.Title, err)}
		}
		return tracksMsg{id: p.ID, page: page}
	}
}

func (m Model) runSearch(query string) tea.Cmd {
	lib := m.services.Library
	if lib == nil || query == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		page, err := lib.Search(ctx, query)
		if err != nil {
			return errMsg{fmt.Errorf("searching %q: %w", query, err)}
		}
		return searchMsg{query: query, page: page}
	}
}

// loadMore fetches the page after the one a listing has read so far.
func (m Model) loadMore(id string, from ytm.Continuation, inDetour, autoplay bool) tea.Cmd {
	lib := m.services.Library
	if lib == nil || !from.More() {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		page, err := lib.More(ctx, from)
		return moreMsg{id: id, page: page, inDetour: inDetour, autoplay: autoplay, err: err}
	}
}

func (m Model) rate(videoID string, r, previous Rating) tea.Cmd {
	lib := m.services.Library
	if lib == nil || videoID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		return ratedMsg{
			videoID:  videoID,
			applied:  r,
			previous: previous,
			err:      lib.Rate(ctx, videoID, r.api()),
		}
	}
}

// request plays a track and marks the wait for it — but only when there is a
// play to wait for. A model with no player says nothing is loading rather than
// LOADING for ever.
func (m *Model) request(t Track) tea.Cmd {
	cmd := m.play(t)
	if cmd != nil {
		m.requested = t.VideoID
	}
	return cmd
}

func (m Model) play(t Track) tea.Cmd {
	streams, audio := m.services.Streams, m.services.Audio
	if streams == nil || audio == nil || t.VideoID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
		defer cancel()
		s, err := streams.Resolve(ctx, t.VideoID)
		if err != nil {
			return errMsg{fmt.Errorf("resolving %s: %w", t.Title, err)}
		}
		if err := audio.Load(s.URL); err != nil {
			return errMsg{fmt.Errorf("playing %s: %w", t.Title, err)}
		}
		// mpv keeps its pause flag across a load, so a track started while
		// paused would sit there silently — the screen claiming it plays
		// while nothing comes out.
		if err := audio.SetPaused(false); err != nil {
			return errMsg{fmt.Errorf("playing %s: %w", t.Title, err)}
		}
		return playingMsg{track: t, length: s.Duration}
	}
}

// replay resolves a track afresh and plays it: its cached URL is forgotten
// first, so this is a new one rather than the one that just failed.
func (m Model) replay(t Track) tea.Cmd {
	streams, play := m.services.Streams, m.play(t)
	if streams == nil || play == nil {
		return nil
	}
	return func() tea.Msg {
		streams.Forget(t.VideoID)
		return play()
	}
}

// prefetch warms the cache for a row the cursor is sitting on. A cold
// resolve is ~2.3s and a cached one ~600ns, so this is where the
// responsiveness of pressing play actually comes from.
//
// The resolve keeps working after this command returns, so the Resolver owns
// the context's lifetime; there is nothing here to leak.
func (m Model) prefetch(videoID string) tea.Cmd {
	streams := m.services.Streams
	if streams == nil || videoID == "" {
		return nil
	}
	return func() tea.Msg {
		streams.Prefetch(videoID)
		return nil
	}
}

// seek jumps to an absolute position.
func (m Model) seek(to time.Duration) tea.Cmd {
	audio := m.services.Audio
	if audio == nil {
		return nil
	}
	return func() tea.Msg {
		if err := audio.Seek(to.Seconds()); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m Model) togglePause() tea.Cmd {
	audio := m.services.Audio
	if audio == nil {
		return nil
	}
	return func() tea.Msg {
		if err := audio.TogglePause(); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// errPlayerGone is what the stream of player events ending means. Nothing
// closes it while the app runs except mpv going away, and nothing here starts
// another one.
var errPlayerGone = errors.New("mpv exited — restart youtuimusic to play again")

// watchEvents delivers one mpv property change. Each one re-arms the watch,
// which is how a channel becomes a stream of messages in this architecture —
// except the end of the stream, which is reported once and not watched again.
//
// Positions that would not change the screen are passed over here, before they
// become a message. mpv reports one about ten times a second, the runtime
// draws a whole frame for every message, and the bar row only says whole
// seconds and whole cells: nine frames in ten were the last one again. What
// the screen shows is taken when the watch is armed, which is every time one
// is delivered, so a skipped position is always one the screen already says.
//
// A drag is no reason to pass one over, though handleEvent ignores every
// position while one is on: this watch is already waiting when the button
// comes up and could not hear about it, so skipping for the drag would hold
// the bar where the drag left it until something other than a position came.
func (m Model) watchEvents() tea.Cmd {
	audio := m.services.Audio
	if audio == nil {
		return nil
	}
	events := audio.Events()
	frame := m.barFrame()
	shown := frame(m.Position)
	return func() tea.Msg {
		for ev := range events {
			if f, ok := ev.Data.(float64); ok && ev.Name == player.PropTimePos &&
				frame(seconds(f)) == shown {
				continue
			}
			return eventMsg(ev)
		}
		return errMsg{errPlayerGone}
	}
}

// seconds is mpv's float seconds as a duration.
func seconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }
