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

// layout is one browser's directory, relative to the platform's application
// data root, and the name it uses in the keyring.
type layout struct{ name, linux, mac, keyring string }

// The Chromium-based browsers worth looking for. Helium is here because the
// Python player already supports it, so a setup that works there keeps
// working here.
var layouts = []layout{
	{"Chromium", "chromium", "Chromium", "chromium"},
	{"Google Chrome", "google-chrome", "Google/Chrome", "chrome"},
	{"Brave", "BraveSoftware/Brave-Browser", "BraveSoftware/Brave-Browser", "brave"},
	{"Microsoft Edge", "microsoft-edge", "Microsoft Edge", "microsoft-edge"},
	{"Vivaldi", "vivaldi", "Vivaldi", "vivaldi"},
	{"Opera", "opera", "com.operasoftware.Opera", "opera"},
	{"Helium", "net.imput.helium", "net.imput.helium", "chromium"},
}

// root is where this platform keeps application data.
//
// Windows is absent on purpose rather than by oversight: it encrypts cookies
// with DPAPI and AES-GCM, which shares nothing with the scheme in decrypt.go,
// so pretending to look there would only produce a confusing failure.
func root() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return xdg, true
		}
		return filepath.Join(home, ".config"), true
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), true
	default:
		return "", false
	}
}

// Installed returns the browsers that are actually on this machine.
func Installed() []Browser {
	base, ok := root()
	if !ok {
		return nil
	}
	var out []Browser
	for _, l := range layouts {
		dir := filepath.Join(base, filepath.FromSlash(l.linux))
		if runtime.GOOS == "darwin" {
			dir = filepath.Join(base, filepath.FromSlash(l.mac))
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			out = append(out, Browser{Name: l.name, Dir: dir, Keyring: l.keyring})
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
