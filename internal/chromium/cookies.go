package chromium

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/cllpse/youtuimusic/internal/sqlitescan"
)

// Cookie is one stored cookie.
type Cookie struct {
	Name    string
	Value   string
	Host    string    // the host_key it is filed under
	Expires time.Time // zero for a session cookie
}

// ErrNoBrowser means no browser profile held a usable session.
var ErrNoBrowser = errors.New("chromium: no signed-in browser profile found")

// bellwether is the cookie a YouTube session actually turns on. Measured:
// remove it and a library request comes back signed out, while removing
// __Secure-3PSIDTS or the SIDCC family changes nothing. Picking the profile
// that has it beats guessing from how many cookies a profile holds, because
// a signed-out profile can easily hold more.
const bellwether = "__Secure-1PSIDTS"

// iterations is what Chromium stretches a storage password with. The number
// differs per platform in Chromium itself, not by our choice.
func iterations() int {
	if runtime.GOOS == "darwin" {
		return 1003
	}
	return 1
}

// keys builds the decryption key for each value prefix a browser may have
// written. On Linux v10 means the hardcoded fallback password and v11 means
// the one in the keyring, so a missing keyring costs the v11 cookies and
// leaves the rest readable.
func (b Browser) keys() (map[string][]byte, error) {
	out := map[string][]byte{}
	fallback, err := deriveKey([]byte(fallbackPassword), iterations())
	if err != nil {
		return nil, err
	}
	out["v10"] = fallback

	password, err := keyringPassword(b.Keyring)
	if err != nil {
		// Not fatal: say so, and let the v10 cookies through.
		return out, fmt.Errorf("%s: %w", b.Name, err)
	}
	stored, err := deriveKey(password, iterations())
	if err != nil {
		return out, err
	}
	out["v11"] = stored
	if runtime.GOOS == "darwin" {
		// macOS has no fallback password; v10 is the stored one.
		out["v10"] = stored
	}
	return out, nil
}

// sends reports whether a cookie filed under hostKey would be sent to host.
// A leading dot means the cookie covers subdomains, which is how the
// account cookies reach music.youtube.com from .youtube.com.
func sends(hostKey, host string) bool {
	hostKey, host = strings.ToLower(hostKey), strings.ToLower(host)
	if strings.HasPrefix(hostKey, ".") {
		return host == hostKey[1:] || strings.HasSuffix(host, hostKey)
	}
	return host == hostKey
}

// Chromium counts microseconds from 1601-01-01 UTC. The gap to the Unix
// epoch is applied in seconds rather than as a time.Duration: a Duration is
// nanoseconds in an int64 and tops out around 292 years, so anything built
// from 1601 overflows and comes back looking long expired.
const epochGapSeconds = 11644473600

func expiry(raw any) time.Time {
	micros, ok := raw.(int64)
	if !ok || micros <= 0 {
		return time.Time{} // a session cookie
	}
	return time.Unix(micros/1e6-epochGapSeconds, micros%1e6*1e3).UTC()
}

// Read returns the cookies in this profile that a request to host would
// send. Cookies that cannot be decrypted are skipped rather than failing
// the read: one unreadable cookie should not cost the whole session.
func (p Profile) Read(host string, now time.Time) ([]Cookie, error) {
	keys, keyErr := p.Browser.keys()

	db, err := sqlitescan.Open(p.Path)
	if err != nil {
		return nil, fmt.Errorf("chromium: %s: %w", p.Path, err)
	}
	tbl, err := db.Table("cookies")
	if err != nil {
		return nil, fmt.Errorf("chromium: %s: %w", p.Path, err)
	}

	var out []Cookie
	var undecrypted int
	for _, row := range tbl.Rows {
		text := func(name string) string {
			v, _ := tbl.Column(row, name)
			s, _ := v.(string)
			return s
		}
		hostKey, name := text("host_key"), text("name")
		if name == "" || !sends(hostKey, host) {
			continue
		}
		if at := expiry(func() any { v, _ := tbl.Column(row, "expires_utc"); return v }()); !at.IsZero() &&
			at.Before(now) {
			continue
		}

		value := text("value")
		if value == "" {
			raw, _ := tbl.Column(row, "encrypted_value")
			blob, _ := raw.([]byte)
			if len(blob) == 0 {
				continue
			}
			plain, err := decrypt(blob, hostKey, keys)
			if err != nil {
				undecrypted++
				continue
			}
			value = plain
		}
		out = append(out, Cookie{Name: name, Value: value, Host: hostKey})
	}

	// A profile where nothing decrypted is a keyring problem wearing the
	// costume of an empty profile, so it is worth saying which it was.
	if len(out) == 0 && undecrypted > 0 {
		if keyErr != nil {
			return nil, fmt.Errorf("chromium: %s: %d cookies are locked: %w",
				p.Browser.Name, undecrypted, keyErr)
		}
		return nil, fmt.Errorf("chromium: %s: none of the %d cookies could be decrypted",
			p.Browser.Name, undecrypted)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Cookies finds the browser profile signed in to host and returns its
// cookies. Every installed browser is tried, most recently used profile
// first, and the first one holding a real session wins.
func Cookies(host string, now time.Time) ([]Cookie, Profile, error) {
	installed := Installed()
	if len(installed) == 0 {
		return nil, Profile{}, fmt.Errorf("%w: no Chromium-based browser on this machine",
			ErrNoBrowser)
	}

	var reasons []string
	var best []Cookie
	var from Profile
	for _, b := range installed {
		for _, p := range b.Profiles() {
			got, err := p.Read(host, now)
			if err != nil {
				reasons = append(reasons, err.Error())
				continue
			}
			for _, c := range got {
				if c.Name == bellwether {
					return got, p, nil
				}
			}
			if len(got) > len(best) {
				best, from = got, p
			}
		}
	}

	// Something, but nothing signed in: report it as such rather than
	// handing back cookies that will look like an empty library.
	if len(best) > 0 {
		return best, from, fmt.Errorf("%w: %s/%s has cookies for %s but no %s",
			ErrNoBrowser, from.Browser.Name, from.Name, host, bellwether)
	}
	if len(reasons) > 0 {
		return nil, Profile{}, fmt.Errorf("%w: %s", ErrNoBrowser, strings.Join(reasons, "; "))
	}
	return nil, Profile{}, fmt.Errorf("%w: no profile had cookies for %s", ErrNoBrowser, host)
}

// Header renders cookies as a Cookie request header.
func Header(cookies []Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}
