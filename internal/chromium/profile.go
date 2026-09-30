package chromium

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Browser is a Chromium-based browser's user data directory.
//
// Keyring is the name the browser files its storage password under, which is
// not always its own name: Chrome files under "chrome", Helium under
// "chromium" because it does not rename the key.
type Browser struct {
	Name    string
	Dir     string
	Keyring string
}

// layout is one browser: where it keeps its profile directory relative to a
// base, the name it uses in the keyring, and the sandbox names it ships under
// on Linux. Flatpak and Snap installs each keep their own copy of the config
// root, so a browser installed that way is invisible under ~/.config unless
// its sandbox path is tried too.
type layout struct {
	name, linux, mac, keyring string
	flatpak, snap             string
}

// The Chromium-based browsers worth looking for. Helium is here because the
// Python player already supports it, so a setup that works there keeps
// working here.
var layouts = []layout{
	{"Chromium", "chromium", "Chromium", "chromium", "org.chromium.Chromium", "chromium"},
	{"Google Chrome", "google-chrome", "Google/Chrome", "chrome", "com.google.Chrome", ""},
	{"Brave", "BraveSoftware/Brave-Browser", "BraveSoftware/Brave-Browser", "brave", "com.brave.Browser", "brave"},
	{"Microsoft Edge", "microsoft-edge", "Microsoft Edge", "microsoft-edge", "com.microsoft.Edge", ""},
	{"Vivaldi", "vivaldi", "Vivaldi", "vivaldi", "com.vivaldi.Vivaldi", ""},
	{"Opera", "opera", "com.operasoftware.Opera", "opera", "com.opera.Opera", ""},
	{"Helium", "net.imput.helium", "net.imput.helium", "chromium", "", ""},
}

// nativeBases is where this platform keeps application data itself.
//
// Windows is absent on purpose rather than by oversight: it encrypts cookies
// with DPAPI and AES-GCM, which shares nothing with the scheme in decrypt.go,
// so pretending to look there would only produce a confusing failure.
func nativeBases(home string) []string {
	if runtime.GOOS == "darwin" {
		return []string{filepath.Join(home, "Library", "Application Support")}
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return []string{xdg}
	}
	return []string{filepath.Join(home, ".config")}
}

// dirs returns every directory this browser's profiles may live in: the
// native root first, then the Flatpak and Snap sandboxes on Linux.
func (l layout) dirs(home string) []string {
	rel := l.linux
	if runtime.GOOS == "darwin" {
		rel = l.mac
	}
	var out []string
	for _, base := range nativeBases(home) {
		out = append(out, filepath.Join(base, filepath.FromSlash(rel)))
	}
	if runtime.GOOS == "darwin" {
		return out
	}
	if l.flatpak != "" {
		out = append(out, filepath.Join(home, ".var", "app", l.flatpak, "config", filepath.FromSlash(l.linux)))
	}
	if l.snap != "" {
		out = append(out, filepath.Join(home, "snap", l.snap, "common", filepath.FromSlash(l.linux)))
	}
	return out
}

// Installed returns the browsers that are actually on this machine.
func Installed() []Browser {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []Browser
	seen := map[string]bool{}
	for _, l := range layouts {
		for _, dir := range l.dirs(home) {
			if seen[dir] {
				continue
			}
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				seen[dir] = true
				out = append(out, Browser{Name: l.name, Dir: dir, Keyring: l.keyring})
			}
		}
	}
	return out
}

// Profile is one profile's cookie store.
type Profile struct {
	Browser Browser
	Name    string // "Default", "Profile 1", and so on
	Path    string // the Cookies file itself
}

// Profiles returns the profiles of a browser that have a cookie store,
// most recently written first — a browser signed in to something was
// almost certainly the one used last.
func (b Browser) Profiles() []Profile {
	entries, err := os.ReadDir(b.Dir)
	if err != nil {
		return nil
	}
	type dated struct {
		p Profile
		t int64
	}
	var found []dated
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Newer Chromium keeps the store one level down, under Network.
		for _, rel := range []string{
			filepath.Join(e.Name(), "Network", "Cookies"),
			filepath.Join(e.Name(), "Cookies"),
		} {
			path := filepath.Join(b.Dir, rel)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			found = append(found, dated{
				p: Profile{Browser: b, Name: e.Name(), Path: path},
				t: info.ModTime().UnixNano(),
			})
			break
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].t > found[j].t })

	out := make([]Profile, len(found))
	for i, f := range found {
		out[i] = f.p
	}
	return out
}
