package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := State{Playlist: "VLPL1", Cursor: 12, Offset: 4,
		Sort: "artist", Descending: true, Repeat: "one"}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := Load(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// Nothing remembered is the ordinary first run, not a failure.
func TestNothingRememberedIsTheZeroValue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := Load(); got != (State{}) {
		t.Errorf("got %+v from an empty home", got)
	}
}

// A file that cannot be read means the same as no file: open at the top.
func TestARuinedFileIsIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(); got != (State{}) {
		t.Errorf("a ruined file produced %+v", got)
	}
}

// Positions are hints, and a negative one would index out of a list.
func TestNegativePositionsAreClamped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, _ := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path,
		[]byte(`{"cursor":-5,"offset":-2,"playlist":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if got.Cursor != 0 || got.Offset != 0 {
		t.Errorf("cursor %d offset %d, want both clamped", got.Cursor, got.Offset)
	}
	if got.Playlist != "x" {
		t.Errorf("the rest of the state was dropped: %+v", got)
	}
}

func TestSaveLeavesNoTemporaryFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(State{Playlist: "a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path, _ := Path()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory holds %d files, want just the state", len(entries))
	}
}
