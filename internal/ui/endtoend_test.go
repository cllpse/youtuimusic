package ui

import (
	"context"
	"os/exec"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cllpse/youtuimusic/internal/player"
	"github.com/cllpse/youtuimusic/internal/stream"
)

// The bug this covers was invisible to a fake. The fake announced the end of
// a track the way the code expected; mpv announces it another way, so
// playback stopped after one track and repeat did nothing. Nothing short of
// a real mpv would have caught it, so this drives one.

// lavfiStreams resolves every track to a one-second generated silence, so
// the test needs no network and makes no noise.
type lavfiStreams struct{}

func (lavfiStreams) Resolve(_ context.Context, id string) (stream.Track, error) {
	return stream.Track{
		VideoID:  id,
		URL:      "av://lavfi:anullsrc=r=8000:cl=mono:d=1",
		Duration: time.Second,
	}, nil
}
func (lavfiStreams) Prefetch(context.Context, string) {}

// run drives the model until the condition holds, and fails if it never
// does.
func run(t *testing.T, m Model, cmd tea.Cmd, until func(Model) bool, within time.Duration) Model {
	t.Helper()
	m, held := pump(t, m, cmd, until, within)
	if !held {
		t.Fatalf("gave up waiting; playing=%q", m.playing.VideoID)
	}
	return m
}

// pump drives the model the way the runtime would: commands run on their own
// goroutines and their messages are folded back in. It reports whether the
// condition held before the time ran out.
func pump(t *testing.T, m Model, cmd tea.Cmd, until func(Model) bool, within time.Duration) (Model, bool) {
	t.Helper()
	msgs := make(chan tea.Msg, 256)

	var exec func(tea.Cmd)
	exec = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if batched, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range batched {
					exec(sub)
				}
				return
			}
			if msg != nil {
				msgs <- msg
			}
		}()
	}
	exec(cmd)

	deadline := time.After(within)
	for {
		if until(m) {
			return m, true
		}
		select {
		case msg := <-msgs:
			next, out := m.Update(msg)
			m = next.(Model)
			exec(out)
		case <-deadline:
			return m, false
		}
	}
}

func realPlayer(t *testing.T) *player.Player {
	t.Helper()
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p, err := player.New(ctx)
	if err != nil {
		t.Fatalf("starting mpv: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func twoTracks() []Track {
	return []Track{
		{VideoID: "first", Title: "First"},
		{VideoID: "second", Title: "Second"},
	}
}

func TestARealTrackEndingAdvances(t *testing.T) {
	m := New(Services{Streams: lavfiStreams{}, Audio: realPlayer(t)})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = sized.(Model)
	m.Tracks = twoTracks()

	started, cmd := m.start(m.Tracks[0])
	m = run(t, started.(Model), batch(m.watchEvents(), cmd),
		func(m Model) bool { return m.playing.VideoID == "second" },
		20*time.Second)

	if m.playing.VideoID != "second" {
		t.Fatalf("playing %q", m.playing.VideoID)
	}
}

// With repeat on, the end of the last track goes back to the first rather
// than stopping.
func TestARealTrackEndingWrapsUnderRepeatAll(t *testing.T) {
	m := New(Services{Streams: lavfiStreams{}, Audio: realPlayer(t)})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = sized.(Model)
	m.Tracks, m.repeat = twoTracks(), RepeatAll

	started, cmd := m.start(m.Tracks[1]) // the last one
	m = run(t, started.(Model), batch(m.watchEvents(), cmd),
		func(m Model) bool { return m.playing.VideoID == "first" },
		20*time.Second)

	if m.playing.VideoID != "first" {
		t.Fatalf("playing %q, want a wrap to the first track", m.playing.VideoID)
	}
}

// And with repeat off, the end of the last track is the end.
func TestARealTrackEndingStopsAtTheEnd(t *testing.T) {
	m := New(Services{Streams: lavfiStreams{}, Audio: realPlayer(t)})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = sized.(Model)
	m.Tracks = twoTracks()

	started, cmd := m.start(m.Tracks[1])
	// Wait well past the end of the track and check it did not roll over.
	m, wrapped := pump(t, started.(Model), batch(m.watchEvents(), cmd),
		func(m Model) bool { return m.playing.VideoID == "first" },
		4*time.Second)
	if wrapped {
		t.Fatal("the list wrapped with repeat off")
	}
	if m.playing.VideoID != "second" {
		t.Fatalf("playing %q, want the last track still", m.playing.VideoID)
	}
}
