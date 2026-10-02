// Package state remembers what was playing and where, so that reopening the
// app can pick the track back up rather than starting over.
//
// This exists because reopening is not always the user's idea: a session
// that has gone stale is fixed in the browser and then here, and losing what
// you were listening to every time is the sort of small tax that makes the
// fix feel worse than the problem.
//
// It holds three things and no more: the playlist that was open, the track
// that was playing in it, and whether the reader had switched the accent
// colours on. The app opens in monochrome; a reader who chose colour should
// not have to choose it again every launch.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// State is what is worth coming back to.
type State struct {
	// Playlist is the tab that was open, by id.
	Playlist string `json:"playlist,omitempty"`
	// Playing is the track that was playing, by video id. Empty when
	// nothing was.
	Playing string `json:"playing,omitempty"`
	// Colour is whether the accent colours were on. It is stored this way
	// round because monochrome is the default, so the zero value — a fresh
	// install, or a file from before the default changed — opens in it.
	Colour bool `json:"colour,omitempty"`
}

// Path is where the state is kept. It sits beside the session rather than
// with the cache: it is small, it is the user's, and it should survive
// anything that clears caches.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "youtuimusic", "state.json"), nil
}

// Load reads the remembered state.
//
// It reports no error: every way this can fail — no file yet, a file from a
// newer version, a half-written one — means the same thing, which is that
// there is nothing to come back to. Refusing to start over that would be
// worse than opening at the top.
func Load() State {
	path, err := Path()
	if err != nil {
		return State{}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}
	}
	return s
}

// Save writes the state, through a temporary file and a rename so that a
// crash mid-write leaves the previous state rather than a truncated file.
func Save(s State) error {
	path, err := Path()
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	blob, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	return nil
}
