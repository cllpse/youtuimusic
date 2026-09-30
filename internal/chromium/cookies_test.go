package chromium

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The keyring is never consulted in tests: with no bus to reach,
// keyringPassword fails immediately, which is also the path a machine
// without a keyring takes.
func noKeyring(t *testing.T) {
	t.Helper()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
}

func sqlite3Bin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not on PATH; cannot build fixtures")
	}
	return p
}

// sealed encrypts a value the way Chromium does on Linux without a keyring:
// the v10 prefix, AES-CBC under the key stretched from "peanuts", and the
// domain hash newer versions prepend to the plaintext.
func sealed(t *testing.T, value, host string, bindHost bool) string {
	t.Helper()
	key, err := deriveKey([]byte(fallbackPassword), 1)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(value)
	if bindHost {
		sum := sha256.Sum256([]byte(host))
		plain = append(sum[:], plain...)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	for i := 0; i < pad; i++ {
		plain = append(plain, byte(pad))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plain)
	return hex.EncodeToString(append([]byte("v10"), out...))
}

// chromiumTime renders a Go time the way the cookie store stores it.
func chromiumTime(at time.Time) int64 { return stamp(at) }

// store writes a Chromium-shaped cookie database.
func store(t *testing.T, path string, rows ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	sql := `CREATE TABLE cookies (
		creation_utc INTEGER, host_key TEXT, name TEXT, value TEXT,
		path TEXT, expires_utc INTEGER, is_secure INTEGER,
		encrypted_value BLOB);
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

func row(host, name, plain, encHex string, expires int64) string {
	enc := "NULL"
	if encHex != "" {
		enc = "X'" + encHex + "'"
	}
	return fmt.Sprintf(
		"INSERT INTO cookies VALUES (0,'%s','%s','%s','/',%d,1,%s);",
		host, name, plain, expires, enc)
}

func TestReadsPlainAndEncryptedCookies(t *testing.T) {
	noKeyring(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "Cookies")
	future := chromiumTime(time.Now().Add(24 * time.Hour))
	store(t, path,
		row(".youtube.com", "PLAIN", "visible", "", future),
		row(".youtube.com", "SEALED", "", sealed(t, "hidden", ".youtube.com", false), future),
		row(".youtube.com", "BOUND", "", sealed(t, "tied", ".youtube.com", true), future),
	)

	got, err := Profile{Path: path}.Read("music.youtube.com", time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := map[string]string{"PLAIN": "visible", "SEALED": "hidden", "BOUND": "tied"}
	if len(got) != len(want) {
		t.Fatalf("got %d cookies, want %d: %+v", len(got), len(want), got)
	}
	for _, c := range got {
		if want[c.Name] != c.Value {
			t.Errorf("%s = %q, want %q", c.Name, c.Value, want[c.Name])
		}
	}
}

func TestSkipsExpiredAndForeignCookies(t *testing.T) {
	noKeyring(t)
	path := filepath.Join(t.TempDir(), "Cookies")
	now := time.Now()
	store(t, path,
		row(".youtube.com", "LIVE", "yes", "", chromiumTime(now.Add(time.Hour))),
		row(".youtube.com", "STALE", "no", "", chromiumTime(now.Add(-time.Hour))),
		row(".youtube.com", "SESSION", "yes", "", 0),
		row(".example.com", "OTHER", "no", "", chromiumTime(now.Add(time.Hour))),
		row("accounts.google.com", "EXACT", "no", "", chromiumTime(now.Add(time.Hour))),
	)

	got, err := Profile{Path: path}.Read("music.youtube.com", now)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, c := range got {
		if c.Value != "yes" {
			t.Errorf("%s should not have been sent", c.Name)
		}
	}
	if len(got) != 2 {
		t.Errorf("got %d cookies, want the live one and the session one: %+v", len(got), got)
	}
}

// Chromium's epoch is far enough back that naive time.Duration arithmetic
// overflows and reads every live cookie as long expired.
func TestExpirySurvivesTheEpochGap(t *testing.T) {
	for _, at := range []time.Time{
		time.Unix(0, 0).UTC(),
		time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
		time.Date(2038, 1, 19, 3, 14, 8, 0, time.UTC),
	} {
		if got := expiry(stamp(at)); !got.Equal(at) {
			t.Errorf("round trip of %s gave %s", at, got)
		}
	}
	if got := expiry(int64(0)); !got.IsZero() {
		t.Errorf("a session cookie got an expiry: %s", got)
	}
	// A cookie a year out must not read as stale.
	year := time.Now().Add(365 * 24 * time.Hour)
	if expiry(stamp(year)).Before(time.Now()) {
		t.Error("a cookie expiring next year reads as already expired")
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
		{"music.youtube.com", "youtube.com", false},
		{".google.com", "music.youtube.com", false},
		{".youtube.com", "notyoutube.com", false},
	}
	for _, c := range cases {
		if got := sends(c.hostKey, c.host); got != c.want {
			t.Errorf("sends(%q, %q) = %v, want %v", c.hostKey, c.host, got, c.want)
		}
	}
}

// A machine with several browsers should not be a coin toss: the one
// actually signed in wins, however many cookies the others hold.
func TestPrefersTheProfileThatIsSignedIn(t *testing.T) {
	noKeyring(t)
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("HOME", base)
	future := chromiumTime(time.Now().Add(24 * time.Hour))

	// Brave: plenty of cookies, no session.
	var noise []string
	for i := 0; i < 8; i++ {
		noise = append(noise, row(".youtube.com", fmt.Sprintf("JUNK%d", i), "x", "", future))
	}
	store(t, filepath.Join(base, "BraveSoftware", "Brave-Browser", "Default", "Cookies"), noise...)

	// Chromium: the one that matters, behind the Network directory.
	store(t, filepath.Join(base, "chromium", "Profile 1", "Network", "Cookies"),
		row(".youtube.com", bellwether, "the-session", "", future),
		row(".youtube.com", "SID", "also", "", future),
	)

	got, from, err := Cookies("music.youtube.com", time.Now())
	if err != nil {
		t.Fatalf("Cookies: %v", err)
	}
	if from.Browser.Name != "Chromium" || from.Name != "Profile 1" {
		t.Errorf("picked %s/%s", from.Browser.Name, from.Name)
	}
	if h := Header(got); h != "SID=also; "+bellwether+"=the-session" {
		t.Errorf("header = %q", h)
	}
}

// Cookies but no session is its own failure, not an empty library.
func TestSignedOutProfileSaysSo(t *testing.T) {
	noKeyring(t)
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("HOME", base)
	store(t, filepath.Join(base, "chromium", "Default", "Cookies"),
		row(".youtube.com", "YSC", "anonymous", "", 0))

	_, _, err := Cookies("music.youtube.com", time.Now())
	if err == nil {
		t.Fatal("a signed-out profile was reported as success")
	}
	if !errorsIs(err, ErrNoBrowser) {
		t.Errorf("err = %v, want ErrNoBrowser", err)
	}
}

// A browser installed as a Flatpak or Snap keeps its own config root; both
// have to be searched, not just ~/.config.
func TestFindsSandboxedInstalls(t *testing.T) {
	noKeyring(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	future := chromiumTime(time.Now().Add(time.Hour))

	flatpakDir := filepath.Join(home, ".var", "app", "org.chromium.Chromium", "config", "chromium")
	store(t, filepath.Join(flatpakDir, "Default", "Cookies"),
		row(".youtube.com", bellwether, "flatpak", "", future))
	snapDir := filepath.Join(home, "snap", "brave", "common", "BraveSoftware", "Brave-Browser")
	store(t, filepath.Join(snapDir, "Profile 1", "Cookies"),
		row(".youtube.com", "SID", "snap", "", future))

	found := map[string]string{}
	for _, b := range Installed() {
		found[b.Name] = b.Dir
	}
	if found["Chromium"] != flatpakDir {
		t.Errorf("Chromium dir = %q, want the flatpak one %q", found["Chromium"], flatpakDir)
	}
	if found["Brave"] != snapDir {
		t.Errorf("Brave dir = %q, want the snap one %q", found["Brave"], snapDir)
	}

	got, from, err := Cookies("music.youtube.com", time.Now())
	if err != nil {
		t.Fatalf("Cookies: %v", err)
	}
	if from.Browser.Name != "Chromium" {
		t.Errorf("picked %s, want the flatpak Chromium", from.Browser.Name)
	}
	if h := Header(got); h != bellwether+"=flatpak" {
		t.Errorf("header = %q", h)
	}
}

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
