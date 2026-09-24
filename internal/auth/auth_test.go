package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cllpse/youtuimusic/internal/ytm"
)

func TestSaveRoundTrips(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := ytm.Session{
		Cookie:    "__Secure-3PAPISID=a; __Secure-1PSIDTS=b",
		UserAgent: "test", AuthUser: "0",
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path, _ := Own()
	t.Setenv(EnvPath, path)
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("round trip lost something:\n got %+v\nwant %+v", got, want)
	}
}

// The file is the whole account; it must not be readable by anyone else.
func TestSaveIsPrivate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(ytm.Session{Cookie: "x=1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path, _ := Own()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode is %o, want 600", perm)
	}
	if dir, err := os.Stat(filepath.Dir(path)); err == nil {
		if perm := dir.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("the directory is %o, want nothing for others", perm)
		}
	}
}

// A refresh overwrites a session that is already there, and leaves no
// half-written file behind if it is interrupted.
func TestSaveReplacesCleanly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(ytm.Session{Cookie: "old=1", UserAgent: "u"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Save(ytm.Session{Cookie: "new=2"}); err != nil {
		t.Fatalf("Save again: %v", err)
	}

	path, _ := Own()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var headers map[string]string
	if err := json.Unmarshal(raw, &headers); err != nil {
		t.Fatalf("not a header map: %v", err)
	}
	if headers["cookie"] != "new=2" {
		t.Errorf("cookie = %q", headers["cookie"])
	}
	// An empty field is left out rather than written as "".
	if _, ok := headers["user-agent"]; ok {
		t.Errorf("an empty header was written: %v", headers)
	}

	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".session-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// Our own file wins over the Python player's, so a session we refresh is
// the one that gets used.
func TestOwnIsPreferred(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	other := filepath.Join(home, ".config", "ytm-player")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "auth.json"),
		[]byte(`{"cookie":"theirs=1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(ytm.Session{Cookie: "ours=1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Cookie != "ours=1" {
		t.Errorf("loaded %q, want ours", got.Cookie)
	}
}
