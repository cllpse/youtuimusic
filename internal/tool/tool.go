// Package tool locates the external programs youtuimusic shells out to: mpv
// for playback and yt-dlp for stream URLs.
//
// A release may carry its own copies of those programs beside the youtuimusic
// executable, so an install that bundles them does not need the user to put
// them on PATH. A bundled copy wins over one on PATH: if the two disagree,
// the one that shipped with this build is the one that was tested with it.
package tool

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Path returns the path to the named helper program.
//
// Search order: a copy bundled with this build (beside the youtuimusic
// executable, or under libexec/youtuimusic), then one on PATH. If neither
// exists the bare name is returned, so the eventual exec failure names the
// program the user is missing instead of this package.
func Path(name string) string {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		if p := bundled(name, filepath.Dir(exe)); p != "" {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// System is like Path but prefers a copy on PATH.
//
// Use it for programs that go stale faster than this build: a newer yt-dlp
// on PATH should beat the snapshot shipped in the archive, which YouTube can
// outdate within weeks. When nothing is on PATH the bundled copy is the
// fallback, so the program still runs on a machine with no packages.
func System(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return Path(name)
}

// bundled returns the path to name inside dir's bundled locations, or "".
//
// Three layouts are recognised:
//
//	$dir/youtuimusic, $dir/mpv, $dir/yt-dlp            one flat dir
//	$dir/bin/youtuimusic, $dir/libexec/youtuimusic/mpv  bin/ + libexec
//	$dir/youtuimusic, $dir/../libexec/youtuimusic/mpv  Homebrew-style
func bundled(name, dir string) string {
	for _, sub := range []string{
		dir,
		filepath.Join(dir, "libexec", "youtuimusic"),
		filepath.Join(dir, "..", "libexec", "youtuimusic"),
	} {
		p := filepath.Join(sub, name)
		if executable(p) {
			return p
		}
	}
	return ""
}

// executable reports whether path exists, is a regular file, and has any
// execute bit set (matching what "which" would be willing to run).
func executable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}
