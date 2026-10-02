package chromium

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cllpse/youtuimusic/internal/jar"
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

// stored renders a Go time the way the cookie store stores it. The package
// no longer writes one, so the inverse lives here for the round trip.
func stored(at time.Time) int64 { return (at.Unix() + epochGapSeconds) * 1e6 }

// store writes a Chromium-shaped cookie database.
func store(t *testing.T, path string, rows ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	sql := `CREATE TABLE cookies (
		creation_utc INTEGER, host_key TEXT, top_frame_site_key TEXT,
		name TEXT, value TEXT, path TEXT, expires_utc INTEGER,
		is_secure INTEGER, last_access_utc INTEGER, encrypted_value BLOB);
	`
	for _, r := range rows {
		sql += r + "\n"
	}
	cmd := exec.Command(sqlite3Bin(t), path)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
}

// cookie is one row, with the columns a test cares about.
type cookie struct {
	host, partition, name, value, path, encHex string
	expires, lastAccess                        int64
}

func (c cookie) row() string {
	enc := "NULL"
	if c.encHex != "" {
		enc = "X'" + c.encHex + "'"
	}
	if c.path == "" {
		c.path = "/"
	}
	return fmt.Sprintf(
		"INSERT INTO cookies VALUES (0,'%s','%s','%s','%s','%s',%d,1,%d,%s);",
		c.host, c.partition, c.name, c.value, c.path, c.expires, c.lastAccess, enc)
}

func row(host, name, plain, encHex string, expires int64) string {
	return cookie{host: host, name: name, value: plain, encHex: encHex, expires: expires}.row()
}

// noKeys is the key source a test without a keyring gets: only the
// fallback and empty-password keys, which is what keys() yields then.
func noKeys(t *testing.T) func() (keySet, error) {
	t.Helper()
	noKeyring(t)
	return Browser{Name: "Test"}.keys
}

func read(t *testing.T, path string, now time.Time) jar.Jar {
	t.Helper()
	got, err := Profile{Path: path}.Read("music.youtube.com", now, noKeys(t))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return got
}

func values(j jar.Jar) map[string]string {
	out := map[string]string{}
	for _, c := range j.Cookies {
		out[c.Name] = c.Value
	}
	return out
}

func TestReadsPlainAndEncryptedCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Cookies")
	future := stored(time.Now().Add(24 * time.Hour))
	store(t, path,
		row(".youtube.com", "PLAIN", "visible", "", future),
		row(".youtube.com", "SEALED", "", sealed(t, "hidden", ".youtube.com", false), future),
		row(".youtube.com", "BOUND", "", sealed(t, "tied", ".youtube.com", true), future),
	)

	got := values(read(t, path, time.Now()))
	want := map[string]string{"PLAIN": "visible", "SEALED": "hidden", "BOUND": "tied"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
}

func TestSkipsExpiredAndForeignCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Cookies")
	now := time.Now()
	store(t, path,
		row(".youtube.com", "LIVE", "yes", "", stored(now.Add(time.Hour))),
		row(".youtube.com", "STALE", "no", "", stored(now.Add(-time.Hour))),
		row(".youtube.com", "SESSION", "yes", "", 0),
		row(".example.com", "OTHER", "no", "", stored(now.Add(time.Hour))),
		row("accounts.google.com", "EXACT", "no", "", stored(now.Add(time.Hour))),
	)

	got := values(read(t, path, now))
	for name, value := range got {
		if value != "yes" {
			t.Errorf("%s should not have been sent", name)
		}
	}
	if len(got) != 2 {
		t.Errorf("got %v, want the live one and the session one", got)
	}
}

// A partitioned cookie is the embedding site's, and a cookie scoped to some
// other path is not sent to the API; neither belongs in the header.
func TestSkipsPartitionedAndOffPathCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Cookies")
	store(t, path,
		cookie{host: ".youtube.com", name: "FIRST", value: "yes"}.row(),
		cookie{host: ".youtube.com", partition: "https://example.com", name: "EMBEDDED", value: "no"}.row(),
		cookie{host: ".youtube.com", name: "ELSEWHERE", value: "no", path: "/watch"}.row(),
		cookie{host: ".youtube.com", name: "API", value: "yes", path: "/youtubei"}.row(),
	)
	got := values(read(t, path, time.Now()))
	if len(got) != 2 || got["FIRST"] != "yes" || got["API"] != "yes" {
		t.Errorf("got %v, want FIRST and API only", got)
	}
}

// When the last access is recorded, it is carried, since that is what
// decides between two signed-in profiles.
func TestCarriesLastAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Cookies")
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	store(t, path, cookie{host: ".youtube.com", name: "SID", value: "x", lastAccess: stored(at)}.row())
	got := read(t, path, time.Now())
	if len(got.Cookies) != 1 || !got.Cookies[0].LastAccess.Equal(at) {
		t.Errorf("last access = %+v, want %s", got.Cookies, at)
	}
}

// A key that unpads by luck but yields noise is not taken; the next one is.
func TestDecryptSkipsAKeyThatProducesNoise(t *testing.T) {
	right, err := deriveKey([]byte(fallbackPassword), 1)
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := hex.DecodeString(sealed(t, "the-value", ".youtube.com", false))
	for i := 0; i < 2000; i++ {
		wrong, _ := deriveKey([]byte(fmt.Sprintf("wrong-%d", i)), 1)
		if plain, err := decryptWith(blob[3:], ".youtube.com", wrong); err != nil || jar.Valid(plain) {
			continue
		}
		// This key passes the padding check and would have been used.
		got, err := decrypt(blob, ".youtube.com", keySet{"v10": {wrong, right}})
		if err != nil || got != "the-value" {
			t.Fatalf("decrypt = %q, %v; want the right key's value", got, err)
		}
		return
	}
	t.Skip("no wrong key happened to unpad in 2000 tries")
}

// A session whose cookies cannot be decrypted is a keyring problem, and the
// error says so instead of the profile reading as signed out.
func TestLockedSessionNamesTheKeyring(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Cookies")
	// v11 needs the keyring's password, and there is no keyring.
	locked := "763131" + strings.Repeat("00", 32)
	store(t, path,
		cookie{host: ".youtube.com", name: jar.Bellwether, encHex: locked}.row(),
		cookie{host: ".youtube.com", name: jar.APISID, encHex: locked}.row(),
	)
	_, err := Profile{Browser: Browser{Name: "Test"}, Name: "Default", Path: path}.
		Read("music.youtube.com", time.Now(), noKeys(t))
	if err == nil || !strings.Contains(err.Error(), "locked") || !errors.Is(err, ErrNoKeyring) {
		t.Errorf("err = %v, want one naming the locked keyring", err)
	}
}

// The keyring is only asked when a YouTube cookie needs it.
func TestTheKeyringIsAskedOnlyWhenNeeded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Cookies")
	store(t, path,
		row(".youtube.com", "PLAIN", "visible", "", 0),
		row(".example.com", "SEALED", "", sealed(t, "hidden", ".example.com", false), 0),
	)
	asked := false
	_, err := Profile{Path: path}.Read("music.youtube.com", time.Now(), func() (keySet, error) {
		asked = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if asked {
		t.Error("the keyring was asked for a profile with nothing encrypted for the host")
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
		if got := chromiumTime(stored(at)); !got.Equal(at) {
			t.Errorf("round trip of %s gave %s", at, got)
		}
	}
	if got := chromiumTime(int64(0)); !got.IsZero() {
		t.Errorf("a session cookie got an expiry: %s", got)
	}
	year := time.Now().Add(365 * 24 * time.Hour)
	if chromiumTime(stored(year)).Before(time.Now()) {
		t.Error("a cookie expiring next year reads as already expired")
	}
}

// Every profile of every browser is read, so the choice between them can be
// made on what they hold rather than on which was found first.
func TestJarsReadsEveryProfile(t *testing.T) {
	noKeyring(t)
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("HOME", base)
	store(t, filepath.Join(base, "BraveSoftware", "Brave-Browser", "Default", "Cookies"),
		row(".youtube.com", "YSC", "anonymous", "", 0))
	store(t, filepath.Join(base, "chromium", "Profile 1", "Network", "Cookies"),
		row(".youtube.com", jar.Bellwether, "the-session", "", 0))
	store(t, filepath.Join(base, "chromium", "Default", "Cookies"),
		row(".example.com", "OTHER", "x", "", 0))

	jars, err := Jars("music.youtube.com", time.Now())
	if err != nil {
		t.Fatalf("Jars: %v", err)
	}
	got := map[string]bool{}
	for _, j := range jars {
		got[j.Name()] = true
	}
	// A profile with nothing for the host is left out.
	if len(got) != 2 || !got["Brave/Default"] || !got["Chromium/Profile 1"] {
		t.Errorf("jars = %v", got)
	}
}

// A browser installed as a Flatpak or Snap keeps its own config root; both
// have to be searched, not just ~/.config.
func TestFindsSandboxedInstalls(t *testing.T) {
	noKeyring(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	flatpakDir := filepath.Join(home, ".var", "app", "org.chromium.Chromium", "config", "chromium")
	store(t, filepath.Join(flatpakDir, "Default", "Cookies"),
		row(".youtube.com", jar.Bellwether, "flatpak", "", 0))
	snapDir := filepath.Join(home, "snap", "brave", "common", "BraveSoftware", "Brave-Browser")
	store(t, filepath.Join(snapDir, "Profile 1", "Cookies"),
		row(".youtube.com", "SID", "snap", "", 0))

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
}
