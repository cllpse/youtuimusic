// Command youtuimusic is a small YouTube Music TUI.
package main

import (
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cllpse/youtuimusic/internal/ui"
)

// fixtures stand in until internal/ytm can sign in. They exist so the layout
// can be built and looked at before real data lands on it.
func fixtures() ui.Model {
	m := ui.New()
	m.Playlists = []ui.Playlist{
		{ID: "LM", Title: "Liked Music", Count: 536},
		{ID: "PL1", Title: "Favorites", Count: 1590},
		{ID: "PL2", Title: "TikTok Songs", Count: 6},
		{ID: "PL3", Title: "1au anthology", Count: 72},
		{ID: "PL4", Title: "Discover Mix", Count: 50},
	}
	m.Tracks = []ui.Track{
		{VideoID: "a", Title: "Poly", Artist: "DAPHNI", Duration: 6*time.Minute + 14*time.Second, Rating: ui.RatingUp},
		{VideoID: "b", Title: "Waves", Artist: "Normani", Duration: 3*time.Minute + 22*time.Second},
		{VideoID: "c", Title: "Starman", Artist: "David Bowie", Duration: 4*time.Minute + 16*time.Second},
		{VideoID: "d", Title: "Eyes Without A Face", Artist: "Billy Idol", Duration: 4*time.Minute + 58*time.Second},
		{VideoID: "e", Title: "Torn", Artist: "Natalie Imbruglia", Duration: 4*time.Minute + 5*time.Second, Rating: ui.RatingDown},
	}
	m.NowPlaying = "DAPHNI — Poly"
	m.Position = 2*time.Minute + 8*time.Second
	m.Length = 6*time.Minute + 14*time.Second
	return m
}

func main() {
	if _, err := tea.NewProgram(fixtures()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "youtuimusic:", err)
		os.Exit(1)
	}
}
