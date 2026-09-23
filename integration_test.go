package youtuimusic_test

// End-to-end proof of the playback chain: yt-dlp resolves a URL, mpv plays it,
// and playback position advances. If this passes, Phase 1 is real.
//
// Opt-in: needs the network, yt-dlp and mpv.
//
//	YTM_NET=1 go test -run TestPlaybackChain -v ./...

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/cllpse/youtuimusic/internal/player"
	"github.com/cllpse/youtuimusic/internal/stream"
)

func TestPlaybackChain(t *testing.T) {
	if os.Getenv("YTM_NET") != "1" {
		t.Skip("set YTM_NET=1 to run the end-to-end playback test")
	}
	for _, bin := range []string{"yt-dlp", "mpv"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resolveStart := time.Now()
	track, err := stream.New().Resolve(ctx, "dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	t.Logf("resolved in %v: %s (%s, %.0f kbps)",
		time.Since(resolveStart), track.Title, track.Codec, track.Bitrate)

	p, err := player.New(ctx)
	if err != nil {
		t.Fatalf("start player: %v", err)
	}
	defer func() { _ = p.Close() }()

	// Silent: this runs in CI and on someone's machine.
	if err := p.SetVolume(0); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}

	loadStart := time.Now()
	if err := p.Load(track.URL); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Wait for audio to actually be advancing, reported via observed events
	// rather than polled.
	var firstAudio time.Duration
	deadline := time.After(30 * time.Second)
	for firstAudio == 0 {
		select {
		case ev, ok := <-p.Events():
			if !ok {
				t.Fatal("player events closed before playback started")
			}
			if ev.Name == "time-pos" {
				if pos, isFloat := ev.Data.(float64); isFloat && pos > 1.0 {
					firstAudio = time.Since(loadStart)
				}
			}
		case <-deadline:
			t.Fatal("playback never passed 1s")
		}
	}
	t.Logf("first audio after %v", firstAudio)

	dur, err := p.Duration()
	if err != nil {
		t.Fatalf("Duration: %v", err)
	}
	if dur < 60 {
		t.Fatalf("duration = %vs, expected a full track", dur)
	}

	// Pause must actually stop the clock.
	if err := p.SetPaused(true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	at, _ := p.Position()
	time.Sleep(700 * time.Millisecond)
	after, _ := p.Position()
	if after != at {
		t.Fatalf("position moved while paused: %v -> %v", at, after)
	}

	// And seeking must land where we asked.
	if err := p.SetPaused(false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := p.Seek(60); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	pos, _ := p.Position()
	if pos < 55 || pos > 75 {
		t.Fatalf("after seeking to 60s, position = %v", pos)
	}
	t.Logf("seek landed at %.1fs of %.0fs", pos, dur)
}
