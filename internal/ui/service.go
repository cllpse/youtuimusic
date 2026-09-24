package ui

import (
	"context"
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
	PlaylistTracks(ctx context.Context, playlistID string) ([]ytm.Track, error)
	AlbumTracks(ctx context.Context, browseID string) ([]ytm.Track, error)
	ArtistTracks(ctx context.Context, browseID string) ([]ytm.Track, error)
	Search(ctx context.Context, query string) ([]ytm.Track, error)
	Rate(ctx context.Context, videoID string, r ytm.Rating) error
}

// Streams turns a video id into a playable URL.
type Streams interface {
	Resolve(ctx context.Context, videoID string) (stream.Track, error)
	Prefetch(ctx context.Context, videoID string)
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
		id     string // the tab these belong to
		tracks []ytm.Track
	}
	searchMsg struct {
		query  string
		tracks []ytm.Track
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
			ts  []ytm.Track
			err error
		)
		switch p.kind {
		case tabAlbum:
			ts, err = lib.AlbumTracks(ctx, p.ID)
		case tabArtist:
			ts, err = lib.ArtistTracks(ctx, p.ID)
		default:
			ts, err = lib.PlaylistTracks(ctx, p.ID)
		}
		if err != nil {
			return errMsg{fmt.Errorf("%s: %w", p.Title, err)}
		}
		return tracksMsg{id: p.ID, tracks: ts}
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
		ts, err := lib.Search(ctx, query)
		if err != nil {
			return errMsg{fmt.Errorf("searching %q: %w", query, err)}
		}
		return searchMsg{query: query, tracks: ts}
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

// prefetch warms the cache for a row the cursor is sitting on. A cold
// resolve is ~2.3s and a cached one ~600ns, so this is where the
// responsiveness of pressing play actually comes from.
func (m Model) prefetch(videoID string) tea.Cmd {
	streams := m.services.Streams
	if streams == nil || videoID == "" {
		return nil
	}
	return func() tea.Msg {
		// Prefetch returns immediately and keeps working, so the context has
		// to outlive this command; a timer releases it instead of a defer.
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(resolveTimeout, cancel)
		streams.Prefetch(ctx, videoID)
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

// watchEvents delivers one mpv property change. Each one re-arms the watch,
// which is how a channel becomes a stream of messages in this architecture.
func (m Model) watchEvents() tea.Cmd {
	audio := m.services.Audio
	if audio == nil {
		return nil
	}
	events := audio.Events()
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return nil
		}
		return eventMsg(ev)
	}
}
