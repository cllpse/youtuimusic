// Package auth loads the session youtuimusic signs in with.
//
// This is the interim arrangement. The intent is for the app to read the
// browser's cookies itself — internal/chromium is most of the way there —
// so that signing in needs no steps at all. Until that lands, the session
// comes from a file on disk.
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
