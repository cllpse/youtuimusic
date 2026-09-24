package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cllpse/youtuimusic/internal/ytm"
)

// isolate puts a test in an empty world: its own home, its own config root
// so no real browser is read, and no bus so no keyring is consulted.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	return home
}

// browserWith writes a Chromium cookie store holding the named cookies.
func browserWith(t *testing.T, home string, cookies map[string]string) {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not on PATH; cannot build fixtures")
	}
	dir := filepath.Join(home, ".config", "chromium", "Default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sql := `CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT,
		expires_utc INTEGER, encrypted_value BLOB);` + "\n"
	for name, value := range cookies {
		sql += fmt.Sprintf(
			"INSERT INTO cookies VALUES ('.youtube.com','%s','%s',0,NULL);\n", name, value)
	}
	cmd := exec.Command(bin, filepath.Join(dir, "Cookies"))
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
}

func TestSaveRoundTrips(t *testing.T) {
	isolate(t)
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
	isolate(t)
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
	isolate(t)
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
	home := isolate(t)
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

// The browser is the point: whatever it holds wins over a saved file,
// because the saved one is the copy that goes stale.
func TestTheBrowserWinsOverASavedSession(t *testing.T) {
	home := isolate(t)
	if err := Save(ytm.Session{Cookie: "saved=old", UserAgent: "mine", AuthUser: "3"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	browserWith(t, home, map[string]string{"__Secure-1PSIDTS": "live"})

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Cookie != "__Secure-1PSIDTS=live" {
		t.Errorf("cookie = %q, want the browser's", got.Cookie)
	}
	// What the cookie store cannot know is carried over rather than lost.
	if got.UserAgent != "mine" || got.AuthUser != "3" {
		t.Errorf("the saved details were dropped: %+v", got)
	}

	// And the fallback was refreshed, so it is current next time too.
	t.Setenv(EnvPath, mustOwn(t))
	saved, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if saved.Cookie != "__Secure-1PSIDTS=live" {
		t.Errorf("the saved copy was not refreshed: %q", saved.Cookie)
	}
}

// No browser is not a failure while a saved session is there.
func TestASavedSessionIsTheFallback(t *testing.T) {
	isolate(t)
	if err := Save(ytm.Session{Cookie: "saved=1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Cookie != "saved=1" {
		t.Errorf("cookie = %q", got.Cookie)
	}
}

// With neither, the error has to name both, or the reason is a guess.
func TestNeitherSourceReportsBoth(t *testing.T) {
	isolate(t)
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error with no browser and no file")
	}
	for _, want := range []string{"browser", "no saved session"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// An explicit file is taken at its word: no browser is consulted.
func TestAnExplicitFileIsUsedAlone(t *testing.T) {
	home := isolate(t)
	browserWith(t, home, map[string]string{"__Secure-1PSIDTS": "live"})

	path := filepath.Join(home, "picked.json")
	if err := os.WriteFile(path, []byte(`{"cookie":"picked=1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPath, path)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Cookie != "picked=1" {
		t.Errorf("cookie = %q, want the file that was named", got.Cookie)
	}
}

func mustOwn(t *testing.T) string {
	t.Helper()
	path, err := Own()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
