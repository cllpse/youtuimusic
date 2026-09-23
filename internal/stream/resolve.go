// Package stream turns a YouTube video id into a playable audio URL.
//
// It shells out to yt-dlp. That is a deliberate choice, not a shortcut:
// YouTube serves most tracks over SABR now, and InnerTube's player endpoint
// returns format lists with no URLs at all for them. Measured against a real
// library, a direct InnerTube resolve succeeded for 0 of 18 tracks while
// yt-dlp succeeded for all of them. Keeping up with PO tokens, client
// fallbacks and SABR is a full-time arms race that yt-dlp's community wins.
//
// Resolving costs roughly a second, so the design assumption is that callers
// prefetch: ask for a track before the user asks to hear it, and the cost
// disappears behind them moving the cursor.
package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/cllpse/youtuimusic/internal/ytm"
)

// URLs are signed and expire. YouTube's are good for around six hours; five
// is a safe margin that still makes replays instant.
const cacheTTL = 5 * time.Hour

// Track is a resolved, playable stream.
type Track struct {
	VideoID  string
	URL      string
	Title    string
	Duration time.Duration
	Codec    string
	Bitrate  float64
}

type entry struct {
	track   Track
	expires time.Time
}

// Resolver resolves stream URLs and caches them. It is safe for concurrent
// use, and collapses concurrent requests for the same id into one yt-dlp run
// so a prefetch and a play never duplicate work.
type Resolver struct {
	mu       sync.Mutex
	cache    map[string]entry
	inFlight map[string]*call

	// Binary is the yt-dlp executable name. Overridable for tests.
	Binary string
	// Format is the yt-dlp format selector.
	Format string
}

type call struct {
	done  chan struct{}
	track Track
	err   error
}

// New returns a Resolver with sensible defaults.
func New() *Resolver {
	return &Resolver{
		cache:    make(map[string]entry),
		inFlight: make(map[string]*call),
		Binary:   "yt-dlp",
		// Prefer opus (itag 251, ~136kbps) and fall back to whatever audio
		// exists. Never a video stream.
		Format: "bestaudio[acodec=opus]/bestaudio/best",
	}
}

// ErrNotResolved means yt-dlp ran but produced no usable URL.
var ErrNotResolved = errors.New("stream: no playable URL")

// Cached returns a previously resolved track if it is still valid. Callers on
// a latency-sensitive path (pressing play) should try this first — a hit is
// free, where a resolve is about a second.
func (r *Resolver) Cached(videoID string) (Track, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[videoID]
	if !ok || time.Now().After(e.expires) {
		return Track{}, false
	}
	return e.track, true
}

// Resolve returns a playable URL for videoID, from cache when possible.
// Concurrent calls for the same id share a single yt-dlp run.
func (r *Resolver) Resolve(ctx context.Context, videoID string) (Track, error) {
	if t, ok := r.Cached(videoID); ok {
		return t, nil
	}

	r.mu.Lock()
	if c, ok := r.inFlight[videoID]; ok {
		r.mu.Unlock()
		select {
		case <-c.done:
			return c.track, c.err
		case <-ctx.Done():
			return Track{}, ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	r.inFlight[videoID] = c
	r.mu.Unlock()

	c.track, c.err = r.run(ctx, videoID)
	close(c.done)

	r.mu.Lock()
	delete(r.inFlight, videoID)
	if c.err == nil {
		r.cache[videoID] = entry{track: c.track, expires: time.Now().Add(cacheTTL)}
	}
	r.mu.Unlock()
	return c.track, c.err
}

// Prefetch resolves in the background and discards the result; the point is
// the cache entry it leaves behind. Errors are intentionally dropped — a
// failed prefetch just means the later Resolve pays full price.
func (r *Resolver) Prefetch(ctx context.Context, videoID string) {
	if videoID == "" {
		return
	}
	if _, ok := r.Cached(videoID); ok {
		return
	}
	go func() { _, _ = r.Resolve(ctx, videoID) }()
}

// ytdlpOutput is the subset of --dump-single-json we care about.
type ytdlpOutput struct {
	URL      string  `json:"url"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
	ACodec   string  `json:"acodec"`
	ABR      float64 `json:"abr"`
}

func (r *Resolver) run(ctx context.Context, videoID string) (Track, error) {
	cmd := exec.CommandContext(ctx, r.Binary,
		"-f", r.Format,
		"--dump-single-json",
		"--no-warnings",
		"--no-playlist",
		"https://music.youtube.com/watch?v="+videoID,
	)
	// Some tracks need a signed-in session. yt-dlp reads browser cookies, and
	// its keyring choice depends on XDG_CURRENT_DESKTOP — see ytm.CookieEnv.
	cmd.Env = ytm.Environ()
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return Track{}, fmt.Errorf("yt-dlp %s: %s", videoID, firstLine(ee.Stderr))
		}
		return Track{}, fmt.Errorf("yt-dlp %s: %w", videoID, err)
	}

	var o ytdlpOutput
	if err := json.Unmarshal(out, &o); err != nil {
		return Track{}, fmt.Errorf("yt-dlp %s: parse: %w", videoID, err)
	}
	if o.URL == "" {
		return Track{}, fmt.Errorf("%w for %s", ErrNotResolved, videoID)
	}
	return Track{
		VideoID:  videoID,
		URL:      o.URL,
		Title:    o.Title,
		Duration: time.Duration(o.Duration * float64(time.Second)),
		Codec:    o.ACodec,
		Bitrate:  o.ABR,
	}, nil
}

func firstLine(b []byte) string {
	for i, c := range b {
		if c == '\n' {
			return string(b[:i])
		}
	}
	return string(b)
}
