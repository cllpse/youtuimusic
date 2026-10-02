package gecko

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cllpse/youtuimusic/internal/jar"
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
		host TEXT, path TEXT, expiry INTEGER, lastAccessed INTEGER,
		isSecure INTEGER, isHttpOnly INTEGER);
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

func read(t *testing.T, path string, now time.Time) jar.Jar {
	t.Helper()
	got, err := Profile{Path: path}.Read("music.youtube.com", now)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return got
}

func TestReadsCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.sqlite")
	future := time.Now().Add(24 * time.Hour).Unix()
	store(t, path,
		row(".youtube.com", "__Secure-1PSIDTS", "the-session", future),
		row(".youtube.com", "SID", "also", future),
		row("example.com", "OTHER", "nope", future),
	)

	if h := read(t, path, time.Now()).Header(); h != "SID=also; __Secure-1PSIDTS=the-session" {
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

	if h := read(t, path, now).Header(); h != "LIVE=yes; SESSION=yes" {
		t.Errorf("header = %q", h)
	}
}

// A container's cookies, and partitioned ones, share the table with the
// default ones; only the default ones are a request from the app.
func TestSkipsContainerAndOffPathCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.sqlite")
	store(t, path,
		row(".youtube.com", "SID", "default", 0),
		`INSERT INTO moz_cookies (originAttributes,host,name,value,expiry) VALUES ('^userContextId=2','.youtube.com','SID','work',0);`,
		`INSERT INTO moz_cookies (originAttributes,host,name,value,expiry) VALUES ('^partitionKey=%28https%2Cexample.com%29','.youtube.com','EMBED','x',0);`,
		`INSERT INTO moz_cookies (host,name,value,path,expiry) VALUES ('.youtube.com','WATCH','x','/watch',0);`,
	)
	if h := read(t, path, time.Now()).Header(); h != "SID=default" {
		t.Errorf("header = %q, want the default container's SID alone", h)
	}
}

// lastAccessed is microseconds since the Unix epoch.
func TestCarriesLastAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.sqlite")
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	store(t, path, fmt.Sprintf(
		`INSERT INTO moz_cookies (host,name,value,expiry,lastAccessed) VALUES ('.youtube.com','SID','x',0,%d);`,
		at.UnixMicro()))
	got := read(t, path, time.Now())
	if len(got.Cookies) != 1 || !got.Cookies[0].LastAccess.Equal(at) {
		t.Errorf("last access = %+v, want %s", got.Cookies, at)
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

// Every profile with cookies for the host is returned, signed in or not;
// choosing between them is jar.Pick's job.
func TestJarsReadsEveryProfile(t *testing.T) {
	dir := t.TempDir()
	store(t, filepath.Join(dir, "noisy.default", "cookies.sqlite"), row(".youtube.com", "YSC", "x", 0))
	store(t, filepath.Join(dir, "real.default-release", "cookies.sqlite"), row(".youtube.com", jar.Bellwether, "s", 0))
	store(t, filepath.Join(dir, "other.default", "cookies.sqlite"), row(".example.com", "OTHER", "x", 0))

	jars, err := jarsIn([]Browser{{Name: "Firefox", Dir: dir}}, "music.youtube.com", time.Now())
	if err != nil {
		t.Fatalf("Jars: %v", err)
	}
	got := map[string]bool{}
	for _, j := range jars {
		got[j.Name()] = true
	}
	if len(got) != 2 || !got["Firefox/noisy.default"] || !got["Firefox/real.default-release"] {
		t.Errorf("jars = %v", got)
	}
}

func TestNoFirefoxSaysSo(t *testing.T) {
	if _, err := jarsIn(nil, "music.youtube.com", time.Now()); !errors.Is(err, ErrNoBrowser) {
		t.Errorf("err = %v, want ErrNoBrowser", err)
	}
}
