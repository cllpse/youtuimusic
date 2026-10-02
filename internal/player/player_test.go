package player

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
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
	got, err := p.floatProperty(PropVolume)
	if err != nil {
		t.Fatalf("volume: %v", err)
	}
	if got != 42 {
		t.Fatalf("volume = %v, want 42", got)
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
	if err := p.SetPaused(true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-p.Events():
			if !ok {
				t.Fatal("event channel closed")
			}
			if ev.Name == PropPause && ev.Data == true {
				return // observed without polling
			}
		case <-deadline:
			t.Fatal("no pause event within 5s")
		}
	}
}

// mpv going away on its own — killed, crashed — closes Events, so the app
// learns playback is gone, and fails commands instead of leaving them to
// time out.
func TestMPVExitingShutsThePlayerDown(t *testing.T) {
	p := newPlayer(t)
	_ = p.cmd.Process.Kill()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-p.Events():
			if ok {
				continue
			}
			if _, err := p.command("get_property", "pause"); err == nil ||
				!strings.Contains(err.Error(), "went away") {
				t.Errorf("command after mpv exited: %v", err)
			}
			return
		case <-deadline:
			t.Fatal("Events was not closed after mpv exited")
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

// The end of a track is reported by mpv's end-file event, not by the
// eof-reached property. The property is unavailable by the time anyone could
// read it — mpv unloads the file in the same breath — so an observer waiting
// for it to be true waits forever, and playback stops at the end of the
// first track.
//
// end-file also says why, which the caller has to respect: loading a
// replacement ends the previous file too, and treating that as the end of a
// track would run away through the playlist.
func TestEndFileReportsWhyPlaybackStopped(t *testing.T) {
	const oneSecond = "av://lavfi:anullsrc=r=8000:cl=mono:d=1"
	const long = "av://lavfi:anullsrc=r=8000:cl=mono:d=30"

	// reasonAfter runs setup and returns the first end-file reason it sees.
	reasonAfter := func(t *testing.T, setup func(p *Player)) string {
		t.Helper()
		p := newPlayer(t)
		setup(p)
		deadline := time.After(10 * time.Second)
		for {
			select {
			case ev, ok := <-p.Events():
				if !ok {
					t.Fatal("events ended before end-file")
				}
				if ev.Name == EndFile {
					reason, _ := ev.Data.(string)
					return reason
				}
				if ev.Name == "eof-reached" {
					if b, ok := ev.Data.(bool); ok && b {
						t.Error("eof-reached came through as true; " +
							"if mpv has started doing that, the comment above is stale")
					}
				}
			case <-deadline:
				t.Fatal("no end-file event")
			}
		}
	}

	if got := reasonAfter(t, func(p *Player) { _ = p.Load(oneSecond) }); got != "eof" {
		t.Errorf("a track running out gave %q, want eof", got)
	}

	got := reasonAfter(t, func(p *Player) {
		_ = p.Load(long)
		time.Sleep(500 * time.Millisecond)
		_ = p.Load(long) // replace it
	})
	if got == "eof" {
		t.Error("replacing a track reported eof; advancing on that runs away through the playlist")
	}
	if got != "stop" {
		t.Errorf("replacing a track gave %q, want stop", got)
	}
}

// A file mpv cannot open ends with reason "error" and says why, which is
// what lets the app retry or report instead of sitting silent.
func TestAFileThatWillNotOpenSaysWhy(t *testing.T) {
	p := newPlayer(t)
	if err := p.Load(filepath.Join(t.TempDir(), "missing.opus")); err != nil {
		t.Fatalf("Load: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-p.Events():
			if ev.Name != EndFile {
				continue
			}
			if ev.Data != "error" || ev.Err == "" {
				t.Fatalf("end-file = %+v, want reason error with mpv's explanation", ev)
			}
			return
		case <-deadline:
			t.Fatal("no end-file within 5s")
		}
	}
}
