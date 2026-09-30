package gecko

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func sqlite3Bin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not on PATH; cannot build fixtures")
	}
	return p
}

// store writes a Firefox-shaped cookie database.
func store(t *testing.T, path string, rows ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	sql := `CREATE TABLE moz_cookies (
		id INTEGER PRIMARY KEY, originAttributes TEXT, name TEXT, value TEXT,
		host TEXT, path TEXT, expiry INTEGER, isSecure INTEGER,
		isHttpOnly INTEGER);
	`
	for _, r := range rows {
		sql += r + "\n"
	}
	cmd := exec.Command(sqlite3Bin(t), path)
	cmd.Stdin = stringsReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
}

func stringsReader(s string) *os.File {
	r, w, _ := os.Pipe()
	go func() {
		_, _ = w.WriteString(s)
		_ = w.Close()
	}()
	return r
}

func row(host, name, value string, expiry int64) string {
	return fmt.Sprintf(
		"INSERT INTO moz_cookies (host,name,value,expiry,isSecure) VALUES ('%s','%s','%s',%d,1);",
		host, name, value, expiry)
}

func TestReadsCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.sqlite")
	future := time.Now().Add(24 * time.Hour).Unix()
	store(t, path,
		row(".youtube.com", "__Secure-1PSIDTS", "the-session", future),
		row(".youtube.com", "SID", "also", future),
		row("example.com", "OTHER", "nope", future),
	)

	got, err := Profile{Path: path}.Read("music.youtube.com", time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if h := Header(got); h != "SID=also; __Secure-1PSIDTS=the-session" {
		t.Errorf("header = %q", h)
	}
}

// An expired cookie and a cookie for another site are both skipped.
func TestSkipsExpiredAndForeignCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.sqlite")
	now := time.Now()
	store(t, path,
		row(".youtube.com", "LIVE", "yes", now.Add(time.Hour).Unix()),
		row(".youtube.com", "OLD", "no", now.Add(-time.Hour).Unix()),
		row(".other.com", "FOREIGN", "no", now.Add(time.Hour).Unix()),
		row(".youtube.com", "SESSION", "yes", 0),
	)

	got, err := Profile{Path: path}.Read("music.youtube.com", now)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if h := Header(got); h != "LIVE=yes; SESSION=yes" {
		t.Errorf("header = %q", h)
	}
}

func TestSendsFollowsCookieScoping(t *testing.T) {
	cases := []struct {
		hostKey, host string
		want          bool
	}{
		{".youtube.com", "music.youtube.com", true},
		{".youtube.com", "youtube.com", true},
		{"music.youtube.com", "music.youtube.com", true},
		{"www.youtube.com", "music.youtube.com", false},
		{".youtube.com", "notyoutube.com", false},
	}
	for _, c := range cases {
		if got := sends(c.hostKey, c.host); got != c.want {
			t.Errorf("sends(%q, %q) = %v, want %v", c.hostKey, c.host, got, c.want)
		}
	}
}

// profiles.ini is authoritative: relative and absolute paths both resolve.
func TestProfilesFromINI(t *testing.T) {
	dir := t.TempDir()
	store(t, filepath.Join(dir, "abc.default-release", "cookies.sqlite"), row(".youtube.com", "A", "1", 0))
	abs := t.TempDir()
	store(t, filepath.Join(abs, "cookies.sqlite"), row(".youtube.com", "B", "1", 0))

	ini := `[General]
StartWithLastProfile=1

[Profile0]
Name=default
IsRelative=1
Path=abc.default-release
Default=1

[Profile1]
Name=external
IsRelative=0
Path=%s
`
	if err := os.WriteFile(filepath.Join(dir, "profiles.ini"), []byte(fmt.Sprintf(ini, abs)), 0o600); err != nil {
		t.Fatal(err)
	}

	profiles := Browser{Name: "Firefox", Dir: dir}.Profiles()
	if len(profiles) != 2 {
		t.Fatalf("got %d profiles, want 2: %+v", len(profiles), profiles)
	}
	paths := map[string]bool{}
	for _, p := range profiles {
		paths[p.Path] = true
	}
	if !paths[filepath.Join(dir, "abc.default-release", "cookies.sqlite")] {
		t.Errorf("relative profile missing from %+v", profiles)
	}
	if !paths[filepath.Join(abs, "cookies.sqlite")] {
		t.Errorf("absolute profile missing from %+v", profiles)
	}
}

// profiles.ini can be missing; the profiles are then found by globbing.
func TestProfilesFallBackToGlob(t *testing.T) {
	dir := t.TempDir()
	store(t, filepath.Join(dir, "xyz.default", "cookies.sqlite"), row(".youtube.com", "A", "1", 0))

	profiles := Browser{Name: "Firefox", Dir: dir}.Profiles()
	if len(profiles) != 1 {
		t.Fatalf("got %d profiles, want 1", len(profiles))
	}
	if filepath.Base(profiles[0].Name) != "xyz.default" {
		t.Errorf("profile = %q", profiles[0].Name)
	}
}

// A profile with the bellwether wins over one that merely has more cookies,
// even when it is not first on disk.
func TestPrefersTheProfileThatIsSignedIn(t *testing.T) {
	dir := t.TempDir()
	future := time.Now().Add(24 * time.Hour).Unix()

	// Signed out, but noisy.
	var noise []string
	for i := 0; i < 8; i++ {
		noise = append(noise, row(".youtube.com", fmt.Sprintf("JUNK%d", i), "x", future))
	}
	store(t, filepath.Join(dir, "noisy.default", "cookies.sqlite"), noise...)

	store(t, filepath.Join(dir, "real.default-release", "cookies.sqlite"),
		row(".youtube.com", Bellwether, "the-session", future),
		row(".youtube.com", "SID", "also", future),
	)
	// Make the noisy one newer, so it would win on recency alone.
	now := time.Now()
	if err := os.Chtimes(filepath.Join(dir, "noisy.default", "cookies.sqlite"), now, now); err != nil {
		t.Fatal(err)
	}

	ini := `[Profile0]
Name=noisy
IsRelative=1
Path=noisy.default
[Profile1]
Name=real
IsRelative=1
Path=real.default-release
`
	if err := os.WriteFile(filepath.Join(dir, "profiles.ini"), []byte(ini), 0o600); err != nil {
		t.Fatal(err)
	}

	got, from, err := cookiesIn([]Browser{{Name: "Firefox", Dir: dir}}, "music.youtube.com", time.Now())
	if err != nil {
		t.Fatalf("Cookies: %v", err)
	}
	if from.Name != "real.default-release" {
		t.Errorf("picked %q, want the signed-in profile", from.Name)
	}
	if h := Header(got); h != "SID=also; "+Bellwether+"=the-session" {
		t.Errorf("header = %q", h)
	}
}

func TestSignedOutProfileSaysSo(t *testing.T) {
	dir := t.TempDir()
	store(t, filepath.Join(dir, "default", "cookies.sqlite"),
		row(".youtube.com", "YSC", "anonymous", 0))

	_, _, err := cookiesIn([]Browser{{Name: "Firefox", Dir: dir}}, "music.youtube.com", time.Now())
	if err == nil {
		t.Fatal("a signed-out profile was reported as success")
	}
	if !errors.Is(err, ErrNoBrowser) {
		t.Errorf("err = %v, want ErrNoBrowser", err)
	}
}
