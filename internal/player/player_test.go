package player

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// These tests drive a real mpv process. They are the proof that the IPC
// approach works end to end; if mpv is missing there is nothing to prove.
func requireMPV(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv not installed")
	}
}

func newPlayer(t *testing.T) *Player {
	t.Helper()
	requireMPV(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestStartsAndAnswers(t *testing.T) {
	p := newPlayer(t)
	start := time.Now()
	if _, err := p.command("get_property", "mpv-version"); err != nil {
		t.Fatalf("get mpv-version: %v", err)
	}
	t.Logf("round trip: %v", time.Since(start))
}

func TestVolumeRoundTrips(t *testing.T) {
	p := newPlayer(t)
	if err := p.SetVolume(42); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}
	got, err := p.Volume()
	if err != nil {
		t.Fatalf("Volume: %v", err)
	}
	if got != 42 {
		t.Fatalf("volume = %d, want 42", got)
	}
}

func TestPauseRoundTrips(t *testing.T) {
	p := newPlayer(t)
	if err := p.SetPaused(true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	paused, err := p.Paused()
	if err != nil {
		t.Fatalf("Paused: %v", err)
	}
	if !paused {
		t.Fatal("expected paused")
	}
	if err := p.TogglePause(); err != nil {
		t.Fatalf("TogglePause: %v", err)
	}
	if paused, _ = p.Paused(); paused {
		t.Fatal("expected resumed after toggle")
	}
}

// The whole point of using IPC over polling: state arrives as events.
func TestObservedPropertiesArriveAsEvents(t *testing.T) {
	p := newPlayer(t)
	if err := p.SetVolume(37); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-p.Events():
			if !ok {
				t.Fatal("event channel closed")
			}
			if ev.Name == "volume" {
				if v, isFloat := ev.Data.(float64); isFloat && int(v) == 37 {
					return // observed without polling
				}
			}
		case <-deadline:
			t.Fatal("no volume event within 5s")
		}
	}
}

func TestPositionIsZeroWhenIdle(t *testing.T) {
	p := newPlayer(t)
	pos, err := p.Position()
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if pos != 0 {
		t.Fatalf("idle position = %v, want 0", pos)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	requireMPV(t)
	p, err := New(context.Background())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := p.command("get_property", "pause"); err == nil {
		t.Fatal("expected commands to fail after Close")
	}
}

// silence is a generated audio source, so this needs no file and makes no
// noise.
const silence = "av://lavfi:anullsrc=r=44100:cl=mono"

// mpv's pause flag belongs to the player, not to the file: loading another
// one while paused leaves it paused. The UI relies on knowing this — it
// clears pause after every load, or a track started while paused would sit
// there silently.
func TestPauseSurvivesALoad(t *testing.T) {
	p := newPlayer(t)

	if err := p.Load(silence); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := p.SetPaused(true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	if err := p.Load(silence); err != nil {
		t.Fatalf("second Load: %v", err)
	}

	paused, err := p.Paused()
	if err != nil {
		t.Fatalf("Paused: %v", err)
	}
	if !paused {
		t.Skip("this mpv clears pause on load; the UI clearing it anyway is harmless")
	}

	// And clearing it afterwards is what actually gets the track going.
	if err := p.SetPaused(false); err != nil {
		t.Fatalf("SetPaused(false): %v", err)
	}
	if paused, err = p.Paused(); err != nil || paused {
		t.Fatalf("still paused after clearing: %v, %v", paused, err)
	}
}
