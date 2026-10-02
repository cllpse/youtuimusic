package stream

import (
	"os"
	"strings"
)

// yt-dlp picks a Chromium cookie-decryption backend from XDG_CURRENT_DESKTOP.
// Its lookup table (cookies.py, _get_linux_desktop_environment) knows this
// list and nothing else. Anything unrecognised falls through to OTHER, which
// _choose_linux_keyring maps to BASICTEXT, which assumes cookies are v10 and
// hands back no key at all. Chromium launched with --password-store=
// gnome-libsecret writes v11 cookies, so every one fails to decrypt:
//
//	WARNING: cannot decrypt v11 cookies: no key found
//	Extracted 0 cookies from chromium (713 could not be decrypted)
//
// Measured on Hyprland: "Hyprland" -> OTHER -> BASICTEXT (0 cookies), while
// "Hyprland:GNOME" -> GNOME -> GNOMEKEYRING (628 cookies, 25 for youtube).
var ytdlpKnownDesktops = []string{
	"GNOME", "KDE", "XFCE", "LXQT", "UNITY", "DEEPIN", "PANTHEON",
	"UKUI", "X-CINNAMON", "CINNAMON", "LXDE", "MATE",
}

// desktopFallback is appended when the running desktop is not one yt-dlp
// recognises. GNOME is the right choice because the Chromium builds that
// encrypt cookies do it against gnome-libsecret.
const desktopFallback = "GNOME"

// CookieEnv returns the environment to run yt-dlp under.
//
// youtuimusic does not ask yt-dlp for cookies, but a user's own yt-dlp
// config may (--cookies-from-browser), and some tracks need a signed-in
// session. When it does, this is what lets it decrypt them.
//
// XDG_CURRENT_DESKTOP is a colon-separated priority list and yt-dlp scans
// every part, so appending a known desktop fixes the lookup while the real one
// stays first. This is scoped to the child process on purpose: exporting it
// session-wide would hand every portal and toolkit a GNOME identity to satisfy
// one lookup table.
//
// Compositors that yt-dlp has never heard of are the normal case on Wayland,
// not an exotic one, which is why this is built in rather than left to a
// wrapper script the user has to remember to run.
func CookieEnv(environ []string) []string {
	const key = "XDG_CURRENT_DESKTOP="
	for i, kv := range environ {
		if !strings.HasPrefix(kv, key) {
			continue
		}
		value := strings.TrimPrefix(kv, key)
		if value == "" || desktopIsKnown(value) {
			return environ
		}
		out := make([]string, len(environ))
		copy(out, environ)
		out[i] = key + value + ":" + desktopFallback
		return out
	}
	// Unset entirely: yt-dlp would fall through to OTHER just the same.
	return append(append([]string(nil), environ...), key+desktopFallback)
}

// desktopIsKnown reports whether any component of a colon-separated
// XDG_CURRENT_DESKTOP is one yt-dlp can map to a keyring.
func desktopIsKnown(value string) bool {
	for _, part := range strings.Split(value, ":") {
		part = strings.ToUpper(strings.TrimSpace(part))
		for _, known := range ytdlpKnownDesktops {
			if part == known {
				return true
			}
		}
	}
	return false
}

// environ is CookieEnv applied to this process's environment.
func environ() []string { return CookieEnv(os.Environ()) }
