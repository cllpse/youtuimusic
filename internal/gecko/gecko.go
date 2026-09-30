// Package gecko reads Firefox's cookie store.
//
// Firefox is not Chromium, so none of internal/chromium applies: its cookies
// live in cookies.sqlite, they are not encrypted, and profiles are listed in
// profiles.ini rather than discovered as directories. What the two share is
// the shape of the answer — the cookies a request to a host would send — and
// the account cookie that says a session is real.
package gecko

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/cllpse/youtuimusic/internal/sqlitescan"
)

// Browser is a Firefox installation's data directory — the one holding
// profiles.ini and the profile directories.
type Browser struct {
	Name string
	Dir  string
}

// Profile is one profile's cookie store.
type Profile struct {
	Browser Browser
	Name    string // the profile's Path, which is its directory name
	Path    string // the cookies.sqlite file itself
}

// Cookie is one stored cookie.
type Cookie struct {
	Name    string
	Value   string
	Host    string    // the host column, which may start with a dot
	Expires time.Time // zero for a session cookie
}

// ErrNoBrowser means no Firefox profile held a usable session.
var ErrNoBrowser = errors.New("gecko: no signed-in Firefox profile found")

// Bellwether is the cookie a YouTube session actually turns on, the same one
// internal/chromium looks for. A profile that has it is signed in; one that
// has cookies without it is signed out.
const Bellwether = "__Secure-1PSIDTS"

// roots are the directories Firefox keeps its data in, most likely first. The
// flatpak and snap entries matter because on Linux those installs keep their
// own copy of ~/.mozilla rather than sharing it.
func roots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	if runtime.GOOS == "darwin" {
		return []string{
			filepath.Join(home, "Library", "Application Support", "Firefox"),
			filepath.Join(home, "Library", "Application Support", "LibreWolf"),
			filepath.Join(home, "Library", "Application Support", "Waterfox"),
		}
	}
	return []string{
		filepath.Join(home, ".mozilla", "firefox"),
		filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
		filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox"),
		filepath.Join(home, ".librewolf"),
		filepath.Join(home, ".waterfox"),
	}
}

// names maps a data directory to the product that keeps it.
func name(dir string) string {
	switch {
	case strings.Contains(dir, "LibreWolf") || strings.HasSuffix(dir, ".librewolf"):
		return "LibreWolf"
	case strings.Contains(dir, "Waterfox") || strings.HasSuffix(dir, ".waterfox"):
		return "Waterfox"
	default:
		return "Firefox"
	}
}

// Installed returns the Firefox-family browsers on this machine.
func Installed() []Browser {
	var out []Browser
	seen := map[string]bool{}
	for _, dir := range roots() {
		if seen[dir] {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			seen[dir] = true
			out = append(out, Browser{Name: name(dir), Dir: dir})
		}
	}
	return out
}

// entry is one [ProfileN] section of profiles.ini.
type entry struct {
	name       string
	path       string
	isRelative bool
	isDefault  bool
}

// profilesINI parses the [ProfileN] sections of a Firefox profiles.ini.
// [General], [InstallN] and anything unknown are ignored.
func profilesINI(path string) []entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var out []entry
	var cur *entry
	inProfile := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if inProfile && cur != nil {
				out = append(out, *cur)
			}
			inProfile = strings.HasPrefix(line, "[Profile")
			cur = &entry{}
			continue
		}
		if !inProfile {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Path":
			cur.path = strings.TrimSpace(value)
		case "Name":
			cur.name = strings.TrimSpace(value)
		case "IsRelative":
			cur.isRelative = strings.TrimSpace(value) == "1"
		case "Default":
			cur.isDefault = strings.TrimSpace(value) == "1"
		}
	}
	if inProfile && cur != nil {
		out = append(out, *cur)
	}
	return out
}

// Profiles returns the profiles that have a cookie store, most recently
// written first. profiles.ini is authoritative when it exists; otherwise the
// profile directories are globbed, which covers a layout the file does not
// describe.
func (b Browser) Profiles() []Profile {
	type dated struct {
		p Profile
		t int64
	}
	var found []dated
	add := func(dir string) {
		path := filepath.Join(dir, "cookies.sqlite")
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return
		}
		found = append(found, dated{
			p: Profile{Browser: b, Name: filepath.Base(dir), Path: path},
			t: info.ModTime().UnixNano(),
		})
	}

	for _, e := range profilesINI(filepath.Join(b.Dir, "profiles.ini")) {
		if e.path == "" {
			continue
		}
		dir := e.path
		if e.isRelative || !filepath.IsAbs(dir) {
			dir = filepath.Join(b.Dir, e.path)
		}
		add(dir)
	}
	if len(found) == 0 {
		matches, _ := filepath.Glob(filepath.Join(b.Dir, "*", "cookies.sqlite"))
		for _, m := range matches {
			add(filepath.Dir(m))
		}
	}

	sort.Slice(found, func(i, j int) bool { return found[i].t > found[j].t })
	out := make([]Profile, len(found))
	for i, f := range found {
		out[i] = f.p
	}
	return out
}

// sends reports whether a cookie filed under hostKey would be sent to host.
// A leading dot means the cookie covers subdomains.
func sends(hostKey, host string) bool {
	hostKey, host = strings.ToLower(hostKey), strings.ToLower(host)
	if strings.HasPrefix(hostKey, ".") {
		return host == hostKey[1:] || strings.HasSuffix(host, hostKey)
	}
	return host == hostKey
}

// Read returns the cookies in this profile that a request to host would send.
func (p Profile) Read(host string, now time.Time) ([]Cookie, error) {
	db, err := sqlitescan.Open(p.Path)
	if err != nil {
		return nil, fmt.Errorf("gecko: %s: %w", p.Path, err)
	}
	tbl, err := db.Table("moz_cookies")
	if err != nil {
		return nil, fmt.Errorf("gecko: %s: %w", p.Path, err)
	}

	var out []Cookie
	for _, row := range tbl.Rows {
		text := func(name string) string {
			v, _ := tbl.Column(row, name)
			s, _ := v.(string)
			return s
		}
		hostKey, cookieName := text("host"), text("name")
		if cookieName == "" || !sends(hostKey, host) {
			continue
		}
		value := text("value")
		if value == "" {
			continue
		}
		var expires time.Time
		if secs, ok := tbl.Column(row, "expiry"); ok {
			if n, ok := secs.(int64); ok && n > 0 {
				expires = time.Unix(n, 0).UTC()
				if expires.Before(now) {
					continue
				}
			}
		}
		out = append(out, Cookie{Name: cookieName, Value: value, Host: hostKey, Expires: expires})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Cookies finds the Firefox profile signed in to host and returns its
// cookies on this machine.
func Cookies(host string, now time.Time) ([]Cookie, Profile, error) {
	return cookiesIn(Installed(), host, now)
}

// cookiesIn is Cookies over an explicit set of browsers, so the selection
// can be exercised without a real home directory.
func cookiesIn(installed []Browser, host string, now time.Time) ([]Cookie, Profile, error) {
	if len(installed) == 0 {
		return nil, Profile{}, fmt.Errorf("%w: no Firefox-family browser on this machine",
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
				if c.Name == Bellwether {
					return got, p, nil
				}
			}
			if len(got) > len(best) {
				best, from = got, p
			}
		}
	}

	if len(best) > 0 {
		return best, from, fmt.Errorf("%w: %s/%s has cookies for %s but no %s",
			ErrNoBrowser, from.Browser.Name, from.Name, host, Bellwether)
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
