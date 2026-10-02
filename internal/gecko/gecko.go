// Package gecko reads Firefox's cookie store.
//
// Firefox is not Chromium, so none of internal/chromium applies: its cookies
// live in cookies.sqlite, they are not encrypted, and profiles are listed in
// profiles.ini rather than discovered as directories. What the two share is
// the shape of the answer, which is internal/jar.
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

	"github.com/cllpse/youtuimusic/internal/jar"
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

// ErrNoBrowser means no Firefox-family browser is installed.
var ErrNoBrowser = errors.New("gecko: no Firefox-family browser found")

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
			filepath.Join(home, "Library", "Application Support", "zen"),
			filepath.Join(home, "Library", "Application Support", "Floorp"),
		}
	}
	return []string{
		filepath.Join(home, ".mozilla", "firefox"),
		filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
		filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox"),
		filepath.Join(home, ".librewolf"),
		filepath.Join(home, ".var", "app", "io.gitlab.librewolf-community", ".librewolf"),
		filepath.Join(home, ".waterfox"),
		filepath.Join(home, ".zen"),
		filepath.Join(home, ".var", "app", "app.zen_browser.zen", ".zen"),
		filepath.Join(home, ".floorp"),
	}
}

// name maps a data directory to the product that keeps it.
func name(dir string) string {
	switch {
	case strings.Contains(dir, "LibreWolf") || strings.HasSuffix(dir, ".librewolf"):
		return "LibreWolf"
	case strings.Contains(dir, "Waterfox") || strings.HasSuffix(dir, ".waterfox"):
		return "Waterfox"
	case strings.HasSuffix(dir, filepath.Join("Application Support", "zen")) || strings.HasSuffix(dir, ".zen"):
		return "Zen"
	case strings.Contains(dir, "Floorp") || strings.HasSuffix(dir, ".floorp"):
		return "Floorp"
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

// Read returns the cookies in this profile that a request to host would send.
//
// Only cookies outside any container are taken. Firefox keeps a container's
// cookies — and partitioned and private-browsing ones — in the same table,
// told apart by originAttributes, and a request from the app is none of
// those. Mixing them would mean two sessions' cookies in one header.
func (p Profile) Read(host string, now time.Time) (jar.Jar, error) {
	tbl, err := sqlitescan.ReadTable(p.Path, "moz_cookies")
	if err != nil {
		return jar.Jar{}, fmt.Errorf("gecko: %s: %w", p.Path, err)
	}

	out := jar.Jar{Browser: p.Browser.Name, Profile: p.Name, Modified: jar.StoreModified(p.Path)}
	// Looked up by name once per table rather than once per row: a profile
	// holds thousands of cookies, and most are for other sites.
	var (
		hostCol       = tbl.Index("host")
		nameCol       = tbl.Index("name")
		valueCol      = tbl.Index("value")
		pathCol       = tbl.Index("path")
		originCol     = tbl.Index("originAttributes")
		expiryCol     = tbl.Index("expiry")
		lastAccessCol = tbl.Index("lastAccessed")
	)
	for _, row := range tbl.Rows {
		hostKey := text(row, hostCol)
		if !jar.Sends(hostKey, host) {
			continue
		}
		name, path := text(row, nameCol), text(row, pathCol)
		if name == "" || !jar.PathMatches(path, jar.RequestPath) {
			continue
		}
		if text(row, originCol) != "" {
			continue
		}
		value := text(row, valueCol)
		if value == "" || !jar.Valid(value) {
			continue
		}
		var expires time.Time
		if n, ok := sqlitescan.Cell(row, expiryCol).(int64); ok && n > 0 {
			expires = time.Unix(n, 0).UTC()
			if expires.Before(now) {
				continue
			}
		}
		var lastAccess time.Time
		if micros, ok := sqlitescan.Cell(row, lastAccessCol).(int64); ok && micros > 0 {
			lastAccess = time.UnixMicro(micros).UTC()
		}
		out.Cookies = append(out.Cookies, jar.Cookie{
			Name: name, Value: value, Host: hostKey, Path: path,
			Expires: expires, LastAccess: lastAccess,
		})
	}
	return out, nil
}

// text is a cell read as a string, empty when it is not one.
func text(row []any, i int) string {
	s, _ := sqlitescan.Cell(row, i).(string)
	return s
}

// Jars reads every Firefox-family profile on this machine and returns what
// each would send to host. Choosing between them is jar.Pick's job.
func Jars(host string, now time.Time) ([]jar.Jar, error) {
	return jarsIn(Installed(), host, now)
}

// jarsIn is Jars over an explicit set of browsers, so it can be exercised
// without a real home directory.
func jarsIn(installed []Browser, host string, now time.Time) ([]jar.Jar, error) {
	if len(installed) == 0 {
		return nil, ErrNoBrowser
	}
	var jars []jar.Jar
	var errs []error
	for _, b := range installed {
		for _, p := range b.Profiles() {
			got, err := p.Read(host, now)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if len(got.Cookies) > 0 {
				jars = append(jars, got)
			}
		}
	}
	return jars, errors.Join(errs...)
}
