package ytm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// liveSession reads the session the app saved on its last run — or the file
// YOUTUIMUSIC_SESSION names, as the app does. It reads the file itself
// rather than going through internal/auth, which imports this package.
func liveSession(t *testing.T) Session {
	t.Helper()
	if os.Getenv("YTM_NET") != "1" {
		t.Skip("set YTM_NET=1")
	}
	path := os.Getenv("YOUTUIMUSIC_SESSION")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home directory: %v", err)
		}
		path = filepath.Join(home, ".config", "youtuimusic", "session.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no session on disk (run youtuimusic once): %v", err)
	}
	var h map[string]string
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return Session{
		Cookie:    h["cookie"],
		UserAgent: h["user-agent"],
		AuthUser:  h["x-goog-authuser"],
	}
}

func TestLiveLibraryPlaylists(t *testing.T) {
	c := NewClient(liveSession(t))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	pls, err := c.LibraryPlaylists(ctx)
	if err != nil {
		t.Fatalf("LibraryPlaylists: %v", err)
	}
	t.Logf("%d playlists in %v", len(pls), time.Since(start))
	for _, p := range pls {
		t.Logf("   %-40s %s", p.ID, p.Title)
	}
	if len(pls) == 0 {
		t.Fatal("no playlists")
	}
}

func TestLivePlaylistTracks(t *testing.T) {
	c := NewClient(liveSession(t))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pls, err := c.LibraryPlaylists(ctx)
	if err != nil {
		t.Fatalf("LibraryPlaylists: %v", err)
	}
	var target string
	for _, p := range pls {
		if p.Title == "TikTok Songs" {
			target = p.ID
		}
	}
	if target == "" {
		t.Skip("expected playlist not present")
	}

	start := time.Now()
	page, err := c.PlaylistTracks(ctx, target)
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	tracks := page.Tracks
	t.Logf("%d tracks in %v", len(tracks), time.Since(start))
	for _, tr := range tracks {
		t.Logf("   %-12s %-28s %-24s %v", tr.VideoID, trunc(tr.Title, 28), trunc(tr.Artist, 24), tr.Duration)
	}
	if len(tracks) == 0 {
		t.Fatal("no tracks")
	}
	if tracks[0].Duration == 0 {
		t.Error("duration did not parse")
	}
}

func TestLiveSearch(t *testing.T) {
	c := NewClient(liveSession(t))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	page, err := c.Search(ctx, "aphex twin xtal")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	got := page.Tracks
	t.Logf("%d results in %v", len(got), time.Since(start))
	for i, tr := range got {
		if i == 5 {
			break
		}
		t.Logf("   %-12s %-38s %s", tr.VideoID, trunc(tr.Title, 38), trunc(tr.Artist, 30))
	}
	if len(got) == 0 {
		t.Fatal("no results")
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Mutates the account: likes a track and then removes the like, so the net
// effect is nothing. Opt-in twice over (YTM_NET and YTM_MUTATE) because it
// touches real user data.
func TestLiveRateRoundTrip(t *testing.T) {
	if os.Getenv("YTM_MUTATE") != "1" {
		t.Skip("set YTM_MUTATE=1 to run the test that rates a real track")
	}
	c := NewClient(liveSession(t))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const victim = "bbEtARu1YZc" // "her room"

	start := time.Now()
	if err := c.Rate(ctx, victim, RatingUp); err != nil {
		t.Fatalf("thumbs up: %v", err)
	}
	t.Logf("thumbs up in %v", time.Since(start))

	start = time.Now()
	if err := c.Rate(ctx, victim, RatingNone); err != nil {
		t.Fatalf("clear rating: %v", err)
	}
	t.Logf("cleared in %v — net effect on the account is zero", time.Since(start))
}
