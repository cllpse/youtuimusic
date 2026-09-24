// Package auth loads and stores the session youtuimusic signs in with.
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
// Fixing that means rotating the cookie ourselves and writing the result
// back, which is what Save is for. The rotation call itself is not written
// yet: accounts.google.com/RotateCookies answers the widely published
// request shape with HTTP 401 for this account, so the shape it does want
// has still to be found.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cllpse/youtuimusic/internal/ytm"
)

// Own is where youtuimusic keeps its own session. Reading the Python
// player's file is a courtesy; writing to it is not ours to do, and a
// refreshed session has to land somewhere we control.
func Own() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "youtuimusic", "session.json"), nil
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

// sources are tried in order. The second is the Python player's, so an
// existing setup keeps working without being migrated by hand.
func sources() []string {
	var out []string
	if p := os.Getenv(EnvPath); p != "" {
		out = append(out, p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	return append(out,
		filepath.Join(home, ".config", "youtuimusic", "session.json"),
		filepath.Join(home, ".config", "ytm-player", "auth.json"),
	)
}

// Load reads the first session file that exists.
func Load() (ytm.Session, error) {
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
