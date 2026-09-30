// Package auth loads and stores the session youtuimusic signs in with.
//
// The session is read out of a browser you are already signed in to — a
// Chromium-family cookie store or Firefox's — rather than a captured file,
// because the browser is the client Google keeps rotating the session for.
// A saved file remains the fallback for a locked keyring or no browser.
//
// A captured session goes stale on its own, which is why signing in keeps
// having to be repeated. Measured against a live account: of the two dozen
// cookies in a capture, __Secure-1PSIDTS is the one Google authenticates
// with — remove it and a library request comes back signed out, while
// removing __Secure-3PSIDTS or any of the SIDCC family changes nothing. It
// is also the one Google rotates, roughly every ten minutes (the interval
// it names in its own reply to /RotateCookies), and neither the InnerTube
// endpoints nor music.youtube.com ever hand back a replacement: they
// refresh the SIDCCs we do not need and never the one we do. So a file on
// disk only ages, while the browser sharing the account keeps rolling the
// session forward, and one day the copy is behind and stops working.
//
// So the session is read from the browser at launch instead. The browser
// is the one client Google keeps rotating for, which makes its copy current
// by definition, and reading it again on every run costs one file read. A
// saved session remains as the fallback for when the browser cannot be
// read at all — a locked keyring, a machine without one — and Load keeps it
// up to date so the fallback is never the stale thing it used to be.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cllpse/youtuimusic/internal/chromium"
	"github.com/cllpse/youtuimusic/internal/gecko"
	"github.com/cllpse/youtuimusic/internal/ytm"
)

// Own is where youtuimusic keeps its own session. Only this file is read
// back as a fallback, so a refreshed session lands somewhere we control and
// a stale file from another tool can never masquerade as a live one.
func Own() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "youtuimusic", "session.json"), nil
}

// Clear removes the saved session, so the next load reads the browser again.
// It is what --auth-clear runs.
func Clear() error {
	path, err := Own()
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("auth: clear %s: %w", path, err)
	}
	return nil
}

// Save writes a session to Own(). It writes to a temporary file and renames
// it, so a session is never half-written: the rename is atomic, and a crash
// mid-write leaves the previous session intact rather than a truncated one
// that reads as signed out.
//
// The file keeps the header-map shape the other sources use, so a session
// saved here can be handed to either app.
func Save(s ytm.Session) error {
	path, err := Own()
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	headers := map[string]string{"cookie": s.Cookie}
	for name, value := range map[string]string{
		"user-agent":        s.UserAgent,
		"x-goog-authuser":   s.AuthUser,
		"x-goog-visitor-id": s.VisitorID,
	} {
		if value != "" {
			headers[name] = value
		}
	}
	blob, err := json.MarshalIndent(headers, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	// 0600 throughout: this is the whole account.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("auth: %w", err)
	}
	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("auth: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return nil
}

// ErrNoSession means no session file was found.
var ErrNoSession = errors.New("auth: no session found")

// EnvPath overrides where the session is read from.
const EnvPath = "YOUTUIMUSIC_SESSION"

// sources are tried in order. Only youtuimusic's own session is read: a file
// captured by another tool goes stale and, worse, gets handed back as if it
// were live, so the app would start into the library and fail every request.
// Point EnvPath at a file to use that one deliberately.
func sources() []string {
	var out []string
	if p := os.Getenv(EnvPath); p != "" {
		out = append(out, p)
	}
	path, err := Own()
	if err != nil {
		return out
	}
	return append(out, path)
}

// UserAgent is what requests identify as when the source did not say. The
// cookie store does not record a user agent, so one is supplied.
const UserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

// Host is the site whose cookies a session is built from.
const Host = "music.youtube.com"

// LoginURL is where the default browser is sent when there is no session.
const LoginURL = "https://accounts.google.com/ServiceLogin?service=youtube" +
	"&continue=https%3A%2F%2Fmusic.youtube.com%2F"

// OpenLogin opens LoginURL in the browser you already use, so a sign-in gets
// your extensions and saved passwords instead of a bare profile.
//
// $BROWSER comes first because a desktop that sets it — Omarchy sets it to
// its own launcher, which goes through the session's app runner — knows how a
// browser should be started here better than a guess does. The rest are the
// ordinary fallbacks.
func OpenLogin() error {
	var candidates [][]string
	if runtime.GOOS == "darwin" {
		candidates = [][]string{{"open", LoginURL}}
	} else {
		if b := strings.TrimSpace(os.Getenv("BROWSER")); b != "" {
			candidates = append(candidates, append(strings.Fields(b), LoginURL))
		}
		candidates = append(candidates,
			[]string{"xdg-open", LoginURL},
			[]string{"gio", "open", LoginURL},
			[]string{"sensible-browser", LoginURL},
		)
	}

	var first error
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if err := exec.Command(c[0], c[1:]...).Start(); err == nil {
			return nil
		} else if first == nil {
			first = err
		}
	}
	if first == nil {
		first = errors.New("no browser launcher found")
	}
	return first
}

// FromBrowser reads a live session out of a signed-in browser profile.
func FromBrowser() (ytm.Session, error) {
	header, err := browserHeader(Host, time.Now())
	if err != nil {
		return ytm.Session{}, err
	}
	return ytm.Session{Cookie: header, UserAgent: UserAgent}, nil
}

// browserHeader asks each supported engine for the cookies a request to host
// would send. Chromium is tried first: the rest of the app was built around
// it, and on a machine with both a Chromium browser and Firefox signed in,
// the Chromium session is the one the user was already using. Firefox is the
// fallback, which is what most Linux desktops need.
func browserHeader(host string, now time.Time) (string, error) {
	chromiumCookies, _, chromiumErr := chromium.Cookies(host, now)
	if chromiumErr == nil {
		return chromium.Header(chromiumCookies), nil
	}
	geckoCookies, _, geckoErr := gecko.Cookies(host, now)
	if geckoErr == nil {
		return gecko.Header(geckoCookies), nil
	}
	// Report both, so a locked keyring and "no Firefox" are not confused
	// with each other.
	return "", errors.Join(chromiumErr, geckoErr)
}

// Load returns the session to sign in with.
//
// The browser comes first and a saved file second, because a file only ages
// while the browser's copy is current. What the browser cannot supply — the
// user agent, the account index — is carried over from the saved session,
// and the result is saved back, so the fallback stays fresh even on the
// runs that never need it.
//
// Setting EnvPath is taken as meaning that file and no other, so a session
// captured by hand can still be used deliberately.
func Load() (ytm.Session, error) {
	saved, savedErr := loadFile()
	if os.Getenv(EnvPath) != "" {
		return saved, savedErr
	}

	live, liveErr := FromBrowser()
	if liveErr == nil {
		session := saved // zero if there was none
		session.Cookie = live.Cookie
		if session.UserAgent == "" {
			session.UserAgent = live.UserAgent
		}
		// Best effort: a session that works is worth having even if it
		// cannot be cached.
		_ = Save(session)
		return session, nil
	}
	if savedErr == nil {
		return saved, nil
	}
	return ytm.Session{}, fmt.Errorf("%w; and no saved session: %w", liveErr, savedErr)
}

// Cached returns the saved session file, if there is one, without reading
// the browser. The app uses it to decide whether it can start straight into
// the library or has to show the sign-in screen.
func Cached() (ytm.Session, bool) {
	s, err := loadFile()
	if err != nil {
		return ytm.Session{}, false
	}
	return s, true
}

// loadFile reads the first session file that exists.
func loadFile() (ytm.Session, error) {
	tried := sources()
	for _, path := range tried {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return ytm.Session{}, fmt.Errorf("auth: %s: %w", path, err)
		}
		s, err := parse(raw)
		if err != nil {
			return ytm.Session{}, fmt.Errorf("auth: %s: %w", path, err)
		}
		return s, nil
	}
	return ytm.Session{}, fmt.Errorf("%w; looked in:\n  %s",
		ErrNoSession, strings.Join(tried, "\n  "))
}

// parse reads a map of request headers. Both files are that shape, and
// matching header names case-insensitively means it does not matter which
// spelling wrote them.
func parse(raw []byte) (ytm.Session, error) {
	var headers map[string]string
	if err := json.Unmarshal(raw, &headers); err != nil {
		return ytm.Session{}, fmt.Errorf("not a map of headers: %w", err)
	}

	var s ytm.Session
	for name, value := range headers {
		switch strings.ToLower(strings.ReplaceAll(name, "_", "-")) {
		case "cookie":
			s.Cookie = value
		case "user-agent":
			s.UserAgent = value
		case "x-goog-authuser":
			s.AuthUser = value
		case "x-goog-visitor-id":
			s.VisitorID = value
		}
	}
	if s.Cookie == "" {
		return ytm.Session{}, errors.New("no cookie in the file")
	}
	return s, nil
}
