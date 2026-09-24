// Package state remembers where the app was when it was last closed, so
// that reopening it comes back to the same place rather than to the top of
// the first playlist.
//
// This exists because reopening is not always the user's idea: a session
// that has gone stale is fixed in the browser and then here, and losing
// your place every time is the sort of small tax that makes the fix feel
// worse than the problem.
//
// The fields are strings and plain ints rather than the app's own enums, so
// the file stays readable and keeps its meaning if those enums are ever
// renumbered.
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
	// Cursor is the highlighted row and Offset the first one on screen.
	// Both are hints: a listing that has since got shorter clamps them.
	Cursor int `json:"cursor,omitempty"`
	Offset int `json:"offset,omitempty"`
	// Sort is the column the list was ordered by, by name, empty for the
	// order it arrived in.
	Sort       string `json:"sort,omitempty"`
	Descending bool   `json:"descending,omitempty"`
	// Repeat is "off", "all" or "one".
	Repeat string `json:"repeat,omitempty"`
}

// Path is where the state is kept. It is beside the session rather than
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
// there is nowhere particular to return to. Refusing to start over that
// would be worse than opening at the top.
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
	if s.Cursor < 0 {
		s.Cursor = 0
	}
	if s.Offset < 0 {
		s.Offset = 0
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
