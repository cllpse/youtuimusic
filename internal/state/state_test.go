package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := State{Playlist: "VLPL1", Playing: "dQw4w9WgXcQ"}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := Load(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A playlist was open but nothing was playing, which is an ordinary way to
// close the app.
func TestAPlaylistWithNothingPlaying(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(State{Playlist: "LM"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := Load()
	if got.Playlist != "LM" || got.Playing != "" {
		t.Errorf("got %+v", got)
	}
	// An empty field is left out of the file rather than written as "".
	path, _ := Path()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\"playing\""; strings.Contains(string(raw), want) {
		t.Errorf("the file carries an empty %s: %s", want, raw)
	}
}

// Nothing remembered is the first run, not a failure.
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

// The theme is remembered beside the playlist, so a reader who chose the
// colours does not choose them again every launch.
func TestColourRoundTrips(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(State{Playlist: "LM", Colour: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := Load()
	if !got.Colour || got.Playlist != "LM" {
		t.Errorf("got %+v, want the playlist and colour on", got)
	}
}

// A file from before monochrome was the default says "mono" or nothing at
// all; either way it opens in monochrome now.
func TestAnOldFileOpensInMonochrome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, _ := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{`{"playlist":"LM"}`, `{"playlist":"LM","mono":true}`} {
		if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := Load(); got.Colour {
			t.Errorf("%s opened in colour", old)
		}
	}
}

// The default monochrome theme is the absence of the field rather than a
// false written out.
func TestThemeIsLeftOutWhenItIsTheDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(State{Playlist: "LM"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path, _ := Path()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "colour") {
		t.Errorf("the file carries the default theme: %s", raw)
	}
}
