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
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cllpse/youtuimusic/internal/tool"
)

// URLs are signed and expire. YouTube's are good for around six hours; five
// is a safe margin that still makes replays instant.
const cacheTTL = 5 * time.Hour

// failureTTL is how long a resolve that failed is remembered. yt-dlp failures
// are usually transient or permanent for one track; either way, retrying within
// a few minutes just spawns the same process for the same answer.
const failureTTL = 5 * time.Minute

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

// failure is a resolve that did not work, kept for a little while so retrying
// it does not spawn yt-dlp again for the same answer.
type failure struct {
	err     error
	expires time.Time
}

// Resolver resolves stream URLs and caches them. It is safe for concurrent
// use, and collapses concurrent requests for the same id into one yt-dlp run
// so a prefetch and a play never duplicate work.
type Resolver struct {
	mu       sync.Mutex
	cache    map[string]entry
	inFlight map[string]*call
	failures map[string]failure

	// Binary is the yt-dlp executable name. Overridable for tests.
	Binary string
	// Format is the yt-dlp format selector.
	Format string
	// CachePath is where resolved URLs are written so they survive a restart.
	// Empty disables persistence.
	CachePath string

	// saveMu serialises writes of the cache file: resolves finish
	// concurrently, and two writers racing on one file can leave it holding
	// the older snapshot.
	saveMu sync.Mutex

	// prefetchSlots caps how many prefetches run yt-dlp at once, and
	// prefetchGen is the newest one asked for. See Prefetch.
	prefetchSlots chan struct{}
	prefetchGen   atomic.Uint64
}

type call struct {
	done  chan struct{}
	track Track
	err   error
}

// New returns a Resolver with sensible defaults. Persistence is opt-in via
// CachePath so that a test or a one-off use does not write to the user's
// config; main wires it up with DefaultCachePath.
func New() *Resolver {
	return &Resolver{
		cache:         make(map[string]entry),
		inFlight:      make(map[string]*call),
		failures:      make(map[string]failure),
		prefetchSlots: make(chan struct{}, maxPrefetches),
		// System, not Path: yt-dlp has to keep up with YouTube, so a newer
		// copy on PATH beats the one bundled in the archive.
		Binary: tool.System("yt-dlp"),
		// Prefer opus (itag 251, ~136kbps) and fall back to whatever audio
		// exists. Never a video stream.
		Format: "bestaudio[acodec=opus]/bestaudio/best",
	}
}

// DefaultCachePath is where resolved URLs live: the user's cache directory,
// since losing them costs one yt-dlp run per track and nothing else. An error
// means no home directory, which just disables persistence.
func DefaultCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "youtuimusic", "streams.json")
}

// Load reads the persisted cache and drops anything already expired.
func (r *Resolver) Load() { r.load() }

// persistedEntry is one cached URL on disk. The track is held whole so a
// restart can play it with the metadata it was resolved with.
type persistedEntry struct {
	Track   Track     `json:"track"`
	Expires time.Time `json:"expires"`
}

// load reads the persisted cache and drops anything already expired.
func (r *Resolver) load() {
	if r.CachePath == "" {
		return
	}
	raw, err := os.ReadFile(r.CachePath)
	if err != nil {
		return
	}
	var stored map[string]persistedEntry
	if err := json.Unmarshal(raw, &stored); err != nil {
		return
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, e := range stored {
		if now.Before(e.Expires) {
			r.cache[id] = entry{track: e.Track, expires: e.Expires}
		}
	}
}

// save writes the live cache to disk. It copies under the lock and writes
// outside it, so a slow disk does not hold up a resolve; saveMu keeps two
// saves from interleaving, and the rename means a reader only ever sees a
// whole file.
func (r *Resolver) save() {
	if r.CachePath == "" {
		return
	}
	r.saveMu.Lock()
	defer r.saveMu.Unlock()

	r.mu.Lock()
	stored := make(map[string]persistedEntry, len(r.cache))
	for id, e := range r.cache {
		stored[id] = persistedEntry{Track: e.track, Expires: e.expires}
	}
	r.mu.Unlock()

	raw, err := json.Marshal(stored)
	if err != nil {
		return
	}
	dir := filepath.Dir(r.CachePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".streams-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(raw)
	if closeErr := tmp.Close(); err != nil || closeErr != nil {
		return
	}
	_ = os.Rename(tmp.Name(), r.CachePath)
}

// prune drops expired entries, so the cache — and the file written from it
// on every resolve — holds the last few hours of listening rather than the
// whole session. Called with mu held.
func (r *Resolver) prune(now time.Time) {
	for id, e := range r.cache {
		if now.After(e.expires) {
			delete(r.cache, id)
		}
	}
	for id, f := range r.failures {
		if now.After(f.expires) {
			delete(r.failures, id)
		}
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

// Forget drops a cached URL that turned out not to play.
//
// A URL is signed to last hours, but YouTube can refuse it long before that
// — it is tied to the address it was resolved from, so a new network is
// enough — and a cache that kept handing it out would leave the track
// unplayable until it expired, across restarts too. The file is rewritten
// so the next launch does not bring it back.
func (r *Resolver) Forget(videoID string) {
	r.mu.Lock()
	_, had := r.cache[videoID]
	delete(r.cache, videoID)
	r.mu.Unlock()
	if had {
		r.save()
	}
}

// cachedFailure returns a recent failure for videoID, if there is one.
func (r *Resolver) cachedFailure(videoID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.failures[videoID]
	if !ok || time.Now().After(f.expires) {
		return nil
	}
	return f.err
}

// Resolve returns a playable URL for videoID, from cache when possible.
// Concurrent calls for the same id share a single yt-dlp run.
func (r *Resolver) Resolve(ctx context.Context, videoID string) (Track, error) {
	if t, ok := r.Cached(videoID); ok {
		return t, nil
	}
	if err := r.cachedFailure(videoID); err != nil {
		return Track{}, err
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
	// A run cut short by its caller says nothing about the track, so it is
	// reported as the cancellation it was and not remembered as a failure:
	// one slow moment must not make the track unplayable for minutes.
	cancelled := ctx.Err() != nil
	if cancelled {
		c.err = fmt.Errorf("yt-dlp %s: %w", videoID, ctx.Err())
	}
	close(c.done)

	now := time.Now()
	r.mu.Lock()
	delete(r.inFlight, videoID)
	r.prune(now)
	switch {
	case c.err == nil:
		r.cache[videoID] = entry{track: c.track, expires: now.Add(cacheTTL)}
	case !cancelled:
		r.failures[videoID] = failure{err: c.err, expires: now.Add(failureTTL)}
	}
	r.mu.Unlock()
	if c.err == nil {
		r.save()
	}
	return c.track, c.err
}

// prefetchTimeout bounds a background resolve. Prefetch owns this so the
// caller does not have to keep a context alive past the command that started
// it.
const prefetchTimeout = 60 * time.Second

// maxPrefetches is how many prefetches may run yt-dlp at once. Each is a
// Python process costing tens of megabytes and a second or two of CPU, and
// a cursor stepping down a playlist asks for a new one every few hundred
// milliseconds.
const maxPrefetches = 2

// Prefetch resolves in the background and discards the result; the point is
// the cache entry it leaves behind. Errors are intentionally dropped — a
// failed prefetch just means the later Resolve reads the stored failure.
//
// At most maxPrefetches run at once. One that has to wait for a slot is
// dropped when it gets one if a newer prefetch was asked for meanwhile: the
// cursor has moved on, and the row it left is not worth a process. A running
// prefetch is never cancelled, because a play of the same track may have
// joined it.
func (r *Resolver) Prefetch(videoID string) {
	if videoID == "" {
		return
	}
	if _, ok := r.Cached(videoID); ok {
		return
	}
	if r.cachedFailure(videoID) != nil {
		return
	}
	generation := r.prefetchGen.Add(1)
	go func() {
		r.prefetchSlots <- struct{}{}
		defer func() { <-r.prefetchSlots }()
		if r.prefetchGen.Load() != generation {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), prefetchTimeout)
		defer cancel()
		_, _ = r.Resolve(ctx, videoID)
	}()
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
	// A user's yt-dlp config may read browser cookies, and then its keyring
	// choice depends on XDG_CURRENT_DESKTOP — see CookieEnv.
	cmd.Env = environ()
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
