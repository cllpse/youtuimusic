package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cllpse/youtuimusic/internal/jar"
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

// session is the two cookies a signed-in profile has, both set to value.
func session(value string) map[string]string {
	return map[string]string{jar.Bellwether: value, jar.APISID: value}
}

// header is what session(value) renders as.
func header(value string) string {
	return jar.Bellwether + "=" + value + "; " + jar.APISID + "=" + value
}

// browserWith writes a Chromium cookie store holding the named cookies.
func browserWith(t *testing.T, home string, cookies map[string]string) {
	browserUsedAt(t, home, cookies, time.Time{})
}

// browserUsedAt is browserWith with the cookies last used at a given time.
func browserUsedAt(t *testing.T, home string, cookies map[string]string, used time.Time) {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not on PATH; cannot build fixtures")
	}
	dir := filepath.Join(home, ".config", "chromium", "Default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var access int64
	if !used.IsZero() {
		access = (used.Unix() + 11644473600) * 1e6 // Chromium's 1601 epoch
	}
	sql := `CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT,
		expires_utc INTEGER, last_access_utc INTEGER, encrypted_value BLOB);` + "\n"
	for name, value := range cookies {
		sql += fmt.Sprintf(
			"INSERT INTO cookies VALUES ('.youtube.com','%s','%s',0,%d,NULL);\n", name, value, access)
	}
	cmd := exec.Command(bin, filepath.Join(dir, "Cookies"))
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
}

// firefoxWith writes a Firefox cookie store holding the named cookies, with
// a profiles.ini pointing at it.
func firefoxWith(t *testing.T, home string, cookies map[string]string) {
	firefoxUsedAt(t, home, cookies, time.Time{})
}

// firefoxUsedAt is firefoxWith with the cookies last used at a given time.
func firefoxUsedAt(t *testing.T, home string, cookies map[string]string, used time.Time) {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not on PATH; cannot build fixtures")
	}
	root := filepath.Join(home, ".mozilla", "firefox")
	dir := filepath.Join(root, "abc.default-release")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var access int64
	if !used.IsZero() {
		access = used.UnixMicro()
	}
	sql := `CREATE TABLE moz_cookies (id INTEGER PRIMARY KEY, name TEXT,
		value TEXT, host TEXT, path TEXT, expiry INTEGER, lastAccessed INTEGER);` + "\n"
	for name, value := range cookies {
		sql += fmt.Sprintf(
			"INSERT INTO moz_cookies (name,value,host,path,expiry,lastAccessed) VALUES ('%s','%s','.youtube.com','/',0,%d);\n",
			name, value, access)
	}
	cmd := exec.Command(bin, filepath.Join(dir, "cookies.sqlite"))
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
	ini := "[Profile0]\nName=default\nIsRelative=1\nPath=abc.default-release\nDefault=1\n"
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0o600); err != nil {
		t.Fatal(err)
	}
}

// With no Chromium browser signed in, the Firefox store is the one read.
func TestFirefoxIsUsedWhenThereIsNoChromiumBrowser(t *testing.T) {
	home := isolate(t)
	firefoxWith(t, home, session("fox"))

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Cookie != header("fox") {
		t.Errorf("cookie = %q, want the Firefox session", got.Cookie)
	}
}

// When both engines are signed in, the session used last wins, whichever
// browser holds it.
func TestTheFreshestSessionWins(t *testing.T) {
	now := time.Now()
	home := isolate(t)
	browserUsedAt(t, home, session("chromium"), now.Add(-30*24*time.Hour))
	firefoxUsedAt(t, home, session("fox"), now)
	if got, err := Load(); err != nil || got.Cookie != header("fox") {
		t.Errorf("Load = %q, %v; want the Firefox session used today", got.Cookie, err)
	}

	home = isolate(t)
	browserUsedAt(t, home, session("chromium"), now)
	firefoxUsedAt(t, home, session("fox"), now.Add(-time.Hour))
	if got, err := Load(); err != nil || got.Cookie != header("chromium") {
		t.Errorf("Load = %q, %v; want the Chromium session used today", got.Cookie, err)
	}
}

// A profile with the session cookie but not the one requests are signed
// with cannot make a request, and is not used.
func TestHalfASessionIsNotUsed(t *testing.T) {
	home := isolate(t)
	browserWith(t, home, map[string]string{jar.Bellwether: "half"})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), jar.APISID) {
		t.Errorf("err = %v, want one naming the missing %s", err, jar.APISID)
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

// A session captured by another tool is not a source: it goes stale, and
// handing it back as live starts the app into a library that fails every
// request.
func TestAnotherToolsFileIsNotASource(t *testing.T) {
	home := isolate(t)
	other := filepath.Join(home, ".config", "ytm-player")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "auth.json"),
		[]byte(`{"cookie":"theirs=1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load used another tool's session instead of failing")
	}
}

// The browser is the point: whatever it holds wins over a saved file,
// because the saved one is the copy that goes stale.
func TestTheBrowserWinsOverASavedSession(t *testing.T) {
	home := isolate(t)
	if err := Save(ytm.Session{Cookie: "saved=old", UserAgent: "mine", AuthUser: "3"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	browserWith(t, home, session("live"))

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Cookie != header("live") {
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
	if saved.Cookie != header("live") {
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
	browserWith(t, home, session("live"))

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
