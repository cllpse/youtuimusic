package ytm

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Reads the session the Python app already captured. Temporary: youtuimusic
// will do its own extraction.
func liveSession(t *testing.T) Session {
	t.Helper()
	if os.Getenv("YTM_NET") != "1" {
		t.Skip("set YTM_NET=1")
	}
	raw, err := os.ReadFile(os.Getenv("HOME") + "/.config/ytm-player/auth.json")
	if err != nil {
		t.Skipf("no session on disk: %v", err)
	}
	var h map[string]string
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("parse auth.json: %v", err)
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
