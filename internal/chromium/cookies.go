package chromium

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cllpse/youtuimusic/internal/jar"
	"github.com/cllpse/youtuimusic/internal/sqlitescan"
)

// ErrNoBrowser means no Chromium-based browser is installed.
var ErrNoBrowser = errors.New("chromium: no Chromium-based browser found")

// iterations is what Chromium stretches a storage password with. The number
// differs per platform in Chromium itself, not by our choice.
func iterations() int {
	if runtime.GOOS == "darwin" {
		return 1003
	}
	return 1
}

// keySet is the keys worth trying for each value prefix, most likely first.
type keySet map[string][][]byte

// keys builds the decryption keys for each value prefix a browser may have
// written.
//
// On Linux v10 means the hardcoded fallback password and v11 the one in the
// keyring, so a missing keyring costs the v11 cookies and leaves the rest
// readable. Both also get the key stretched from an empty password, which
// Chromium uses when the keyring answered but had nothing to give; yt-dlp
// found that one in the wild. On macOS there is no fallback: v10 is the
// Keychain's password.
//
// More than one key per prefix is safe because decrypt only accepts a
// result that unpads and reads as a cookie value.
func (b Browser) keys() (keySet, error) {
	passwords, keyErr := storagePasswords(b)
	var stored [][]byte
	for _, pw := range passwords {
		k, err := deriveKey(pw, iterations())
		if err != nil {
			return nil, err
		}
		stored = append(stored, k)
	}
	if runtime.GOOS == "darwin" {
		return keySet{"v10": stored}, keyErr
	}

	fallback, err := deriveKey([]byte(fallbackPassword), iterations())
	if err != nil {
		return nil, err
	}
	empty, err := deriveKey(nil, iterations())
	if err != nil {
		return nil, err
	}
	return keySet{
		"v10": {fallback, empty},
		"v11": append(stored, empty),
	}, keyErr
}

// Chromium counts microseconds from 1601-01-01 UTC. The gap to the Unix
// epoch is applied in seconds rather than as a time.Duration: a Duration is
// nanoseconds in an int64 and tops out around 292 years, so anything built
// from 1601 overflows and comes back looking long expired.
const epochGapSeconds = 11644473600

func chromiumTime(raw any) time.Time {
	micros, ok := raw.(int64)
	if !ok || micros <= 0 {
		return time.Time{} // a session cookie, or a column the store lacks
	}
	return time.Unix(micros/1e6-epochGapSeconds, micros%1e6*1e3).UTC()
}

// Read returns the cookies in this profile that a request to host would
// send. keys is asked only when a cookie for host is encrypted, so a profile
// with no YouTube cookies never reaches the keyring. Cookies that cannot be
// decrypted are skipped rather than failing the read, unless they cost the
// session, in which case the keyring is the thing to report.
func (p Profile) Read(host string, now time.Time, keys func() (keySet, error)) (jar.Jar, error) {
	tbl, err := sqlitescan.ReadTable(p.Path, "cookies")
	if err != nil {
		return jar.Jar{}, fmt.Errorf("chromium: %s: %w", p.Path, err)
	}

	out := jar.Jar{Browser: p.Browser.Name, Profile: p.Name, Modified: jar.StoreModified(p.Path)}
	col := chromiumColumns(tbl)
	var undecrypted []string
	var keyErr error
	for _, row := range tbl.Rows {
		hostKey := text(row, col.host)
		if !jar.Sends(hostKey, host) {
			continue // most rows: checked first, so they cost one lookup
		}
		name, path := text(row, col.name), text(row, col.path)
		if name == "" || !jar.PathMatches(path, jar.RequestPath) {
			continue
		}
		// A partitioned cookie belongs to the site it was embedded under,
		// not to a request made to the host itself.
		if text(row, col.partition) != "" {
			continue
		}
		expires := chromiumTime(sqlitescan.Cell(row, col.expires))
		if !expires.IsZero() && expires.Before(now) {
			continue
		}

		value := text(row, col.value)
		if value == "" {
			blob, _ := sqlitescan.Cell(row, col.encrypted).([]byte)
			if len(blob) == 0 {
				continue
			}
			set, err := keys()
			if err != nil {
				keyErr = err
			}
			plain, err := decrypt(blob, hostKey, set)
			if err != nil {
				undecrypted = append(undecrypted, name)
				continue
			}
			value = plain
		}
		if !jar.Valid(value) {
			continue
		}
		out.Cookies = append(out.Cookies, jar.Cookie{
			Name: name, Value: value, Host: hostKey, Path: path,
			Expires: expires, LastAccess: chromiumTime(sqlitescan.Cell(row, col.lastAccess)),
		})
	}

	// A profile whose session is locked away is a keyring problem wearing
	// the costume of a signed-out profile, so it is worth saying which.
	if len(undecrypted) > 0 && !out.SignedIn() {
		reason := keyErr
		if reason == nil {
			reason = fmt.Errorf("%w: the keyring's password does not fit", ErrEncrypted)
		}
		return out, fmt.Errorf("chromium: %s/%s: %d cookies are locked (%s): %w",
			p.Browser.Name, p.Name, len(undecrypted), strings.Join(undecrypted, ", "), reason)
	}
	return out, nil
}

// columns is where Chromium's cookie table keeps what Read needs, looked up
// by name once per table: the table has gained columns over the years, so a
// fixed position would mean a different column on a different build. A
// column the store lacks is -1, and reads as empty.
type columns struct {
	host, name, value, encrypted, path, partition, expires, lastAccess int
}

func chromiumColumns(tbl *sqlitescan.Table) columns {
	return columns{
		host:       tbl.Index("host_key"),
		name:       tbl.Index("name"),
		value:      tbl.Index("value"),
		encrypted:  tbl.Index("encrypted_value"),
		path:       tbl.Index("path"),
		partition:  tbl.Index("top_frame_site_key"),
		expires:    tbl.Index("expires_utc"),
		lastAccess: tbl.Index("last_access_utc"),
	}
}

// text is a cell read as a string, empty when it is not one.
func text(row []any, i int) string {
	s, _ := sqlitescan.Cell(row, i).(string)
	return s
}

// Jars reads every profile of every installed Chromium-based browser and
// returns what each would send to host. Choosing between them is
// jar.Pick's job. The error collects the profiles that could not be read;
// it is not fatal while others could.
//
// The keyring is asked at most once per browser, and only if one of its
// profiles needs it: a locked keyring is a prompt, and a browser holding no
// YouTube cookies has no business raising one.
func Jars(host string, now time.Time) ([]jar.Jar, error) {
	installed := Installed()
	if len(installed) == 0 {
		return nil, ErrNoBrowser
	}
	var jars []jar.Jar
	var errs []error
	for _, b := range installed {
		keys := sync.OnceValues(b.keys)
		for _, p := range b.Profiles() {
			got, err := p.Read(host, now, keys)
			if err != nil {
				errs = append(errs, err)
			}
			if len(got.Cookies) > 0 {
				jars = append(jars, got)
			}
		}
	}
	return jars, errors.Join(errs...)
}
