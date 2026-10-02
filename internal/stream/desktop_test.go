package stream

import (
	"slices"
	"strings"
	"testing"
)

func desktopOf(env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "XDG_CURRENT_DESKTOP="); ok {
			return v
		}
	}
	return "<unset>"
}

func TestUnknownDesktopGetsAFallbackAppended(t *testing.T) {
	// The case that actually bit: Hyprland is not in yt-dlp's table, so
	// cookies silently fail to decrypt.
	got := desktopOf(CookieEnv([]string{"XDG_CURRENT_DESKTOP=Hyprland", "HOME=/home/x"}))
	if got != "Hyprland:GNOME" {
		t.Fatalf("desktop = %q, want Hyprland:GNOME", got)
	}
}

func TestKnownDesktopIsLeftAlone(t *testing.T) {
	for _, desktop := range []string{"GNOME", "KDE", "X-Cinnamon", "gnome", "ubuntu:GNOME"} {
		in := []string{"XDG_CURRENT_DESKTOP=" + desktop}
		if got := desktopOf(CookieEnv(in)); got != desktop {
			t.Errorf("desktop %q was rewritten to %q", desktop, got)
		}
	}
}

func TestTheRealDesktopStaysFirst(t *testing.T) {
	// Everything else reading this variable — portals especially — must still
	// see the real compositor.
	got := desktopOf(CookieEnv([]string{"XDG_CURRENT_DESKTOP=Hyprland"}))
	if !strings.HasPrefix(got, "Hyprland") {
		t.Fatalf("real desktop no longer leads: %q", got)
	}
}

func TestUnsetDesktopGetsOne(t *testing.T) {
	got := desktopOf(CookieEnv([]string{"HOME=/home/x"}))
	if got != "GNOME" {
		t.Fatalf("desktop = %q, want GNOME", got)
	}
}

func TestEmptyDesktopIsLeftAlone(t *testing.T) {
	// An explicitly empty value is a deliberate choice; don't second-guess it.
	got := desktopOf(CookieEnv([]string{"XDG_CURRENT_DESKTOP="}))
	if got != "" {
		t.Fatalf("desktop = %q, want empty", got)
	}
}

func TestOtherVariablesAreUntouched(t *testing.T) {
	in := []string{"HOME=/home/x", "XDG_CURRENT_DESKTOP=Hyprland", "PATH=/usr/bin"}
	out := CookieEnv(in)
	for _, want := range []string{"HOME=/home/x", "PATH=/usr/bin"} {
		if !slices.Contains(out, want) {
			t.Errorf("%q was lost", want)
		}
	}
	if len(out) != len(in) {
		t.Fatalf("env grew from %d to %d entries", len(in), len(out))
	}
}

func TestInputIsNotMutated(t *testing.T) {
	in := []string{"XDG_CURRENT_DESKTOP=Hyprland"}
	_ = CookieEnv(in)
	if in[0] != "XDG_CURRENT_DESKTOP=Hyprland" {
		t.Fatalf("caller's slice was modified: %q", in[0])
	}
}
