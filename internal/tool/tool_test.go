package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeTool(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

// The flat layout: the helper sits next to the main binary.
func TestBundledFlatLayout(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, filepath.Join(dir, "mpv"), 0o755)

	if got := bundled("mpv", dir); got != filepath.Join(dir, "mpv") {
		t.Fatalf("bundled(mpv) = %q, want the flat copy", got)
	}
}

// A bin/ + libexec layout wins over a plain on-PATH copy.
func TestBundledLibexecLayout(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, filepath.Join(dir, "libexec", "youtuimusic", "yt-dlp"), 0o755)

	got := bundled("yt-dlp", dir)
	want := filepath.Join(dir, "libexec", "youtuimusic", "yt-dlp")
	if got != want {
		t.Fatalf("bundled(yt-dlp) = %q, want %q", got, want)
	}
}

// The Homebrew-style layout: bin/ at the top, libexec one level up.
func TestBundledHomebrewLayout(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTool(t, filepath.Join(root, "libexec", "youtuimusic", "mpv"), 0o755)

	got := bundled("mpv", dir)
	want := filepath.Join(root, "libexec", "youtuimusic", "mpv")
	if got != want {
		t.Fatalf("bundled(mpv) = %q, want %q", got, want)
	}
}

// A file that is not executable is not a usable bundle.
func TestBundledSkipsNonExecutable(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, filepath.Join(dir, "mpv"), 0o644)

	if got := bundled("mpv", dir); got != "" {
		t.Fatalf("bundled(mpv) = %q, want empty for a non-executable file", got)
	}
}

// System prefers a copy on PATH over the bare name (and over a bundle).
func TestSystemPrefersPATH(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, filepath.Join(dir, "yt-dlp"), 0o755)
	t.Setenv("PATH", dir)

	if got := System("yt-dlp"); got != filepath.Join(dir, "yt-dlp") {
		t.Fatalf("System(yt-dlp) = %q, want the PATH copy", got)
	}
}

// With nothing on PATH, System still returns a usable name to exec so the
// caller fails with the program's name, not a resolution error.
func TestSystemFallsBackToName(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if got := System("definitely-not-a-real-binary-xyz"); got != "definitely-not-a-real-binary-xyz" {
		t.Fatalf("System(missing) = %q, want the bare name", got)
	}
}

// With no bundle, Path falls back to PATH — and to the bare name when the
// program is nowhere to be found.
func TestPathFallsBackToPATHThenName(t *testing.T) {
	dir := t.TempDir()
	if got := bundled("sh", dir); got != "" {
		t.Fatalf("bundled('sh', tempdir) = %q, want empty", got)
	}

	if got, err := exec.LookPath("sh"); err == nil {
		if p := Path("sh"); p != got {
			t.Fatalf("Path(sh) = %q, want PATH lookup %q", p, got)
		}
	}

	if p := Path("definitely-not-a-real-binary-xyz"); p != "definitely-not-a-real-binary-xyz" {
		t.Fatalf("Path(missing) = %q, want the bare name", p)
	}
}
