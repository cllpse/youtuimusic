package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// press feeds a keystroke through Update, the way the runtime would.
func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var key tea.Key
		if len(k) == 1 {
			key = tea.Key{Code: rune(k[0]), Text: k}
		} else {
			switch k {
			case "tab":
				key = tea.Key{Code: tea.KeyTab}
			case "up":
				key = tea.Key{Code: tea.KeyUp}
			case "down":
				key = tea.Key{Code: tea.KeyDown}
			case "esc":
				key = tea.Key{Code: tea.KeyEscape}
			case "enter":
				key = tea.Key{Code: tea.KeyEnter}
			case "backspace":
				key = tea.Key{Code: tea.KeyBackspace}
			}
		}
		next, _ := m.Update(tea.KeyPressMsg(key))
		m = next.(Model)
	}
	return m
}

func sample() Model {
	m := New()
	m.Playlists = []Playlist{{Title: "One"}, {Title: "Two"}, {Title: "Three"}}
	m.Tracks = []Track{
		{VideoID: "a", Title: "Alpha", Artist: "A", Duration: time.Minute},
		{VideoID: "b", Title: "Beta", Artist: "B", Duration: 2 * time.Minute},
	}
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return sized.(Model)
}

func TestTabSwitchesFocus(t *testing.T) {
	m := sample()
	if m.Focus() != PaneSidebar {
		t.Fatal("expected sidebar focus initially")
	}
	if m = press(m, "tab"); m.Focus() != PaneTracks {
		t.Fatal("tab did not move focus to tracks")
	}
	if m = press(m, "tab"); m.Focus() != PaneSidebar {
		t.Fatal("tab did not move focus back")
	}
}

// The cursor must belong to the focused pane; moving in one must not move
// the other.
func TestCursorFollowsFocus(t *testing.T) {
	m := press(sample(), "down")
	if p, _ := m.SelectedPlaylist(); p.Title != "Two" {
		t.Fatalf("sidebar cursor did not move, got %q", p.Title)
	}
	if m.TrackCursor() != 0 {
		t.Fatal("track cursor moved while the sidebar had focus")
	}

	m = press(m, "tab", "down")
	if tr, _ := m.SelectedTrack(); tr.Title != "Beta" {
		t.Fatalf("track cursor did not move, got %q", tr.Title)
	}
	if p, _ := m.SelectedPlaylist(); p.Title != "Two" {
		t.Fatal("sidebar cursor moved while tracks had focus")
	}
}

func TestCursorClampsAtBothEnds(t *testing.T) {
	m := press(sample(), "up", "up", "up")
	if p, _ := m.SelectedPlaylist(); p.Title != "One" {
		t.Fatalf("cursor went above the first row: %q", p.Title)
	}
	m = press(m, "down", "down", "down", "down", "down")
	if p, _ := m.SelectedPlaylist(); p.Title != "Three" {
		t.Fatalf("cursor went past the last row: %q", p.Title)
	}
}

func TestEmptyListsDoNotPanic(t *testing.T) {
	m := New()
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(sized.(Model), "down", "up", "tab", "down", "+")
	if _, ok := m.SelectedTrack(); ok {
		t.Fatal("empty model reported a selected track")
	}
	_ = m.View() // must render without panicking
}

// Rating the same way twice clears it, matching the API's behaviour.
func TestRatingTogglesOff(t *testing.T) {
	m := press(sample(), "tab", "+")
	if tr, _ := m.SelectedTrack(); tr.Rating != RatingUp {
		t.Fatalf("rating = %v, want up", tr.Rating)
	}
	m = press(m, "+")
	if tr, _ := m.SelectedTrack(); tr.Rating != RatingNone {
		t.Fatal("rating the same way twice did not clear it")
	}
	m = press(m, "-")
	if tr, _ := m.SelectedTrack(); tr.Rating != RatingDown {
		t.Fatal("thumbs down did not apply")
	}
}

func TestRatingIgnoredWhenSidebarFocused(t *testing.T) {
	m := press(sample(), "+")
	if tr, _ := m.SelectedTrack(); tr.Rating != RatingNone {
		t.Fatal("rated a track while the sidebar had focus")
	}
}

// Search must capture ordinary keys, including ones that are commands
// otherwise — typing "j" should not move the cursor.
func TestSearchCapturesKeys(t *testing.T) {
	m := press(sample(), "/", "j", "a", "z", "z")
	if !m.Searching {
		t.Fatal("expected to be searching")
	}
	if m.Query != "jazz" {
		t.Fatalf("query = %q, want %q", m.Query, "jazz")
	}
	if p, _ := m.SelectedPlaylist(); p.Title != "One" {
		t.Fatal("typing in search moved the sidebar cursor")
	}
	m = press(m, "backspace")
	if m.Query != "jaz" {
		t.Fatalf("backspace gave %q", m.Query)
	}
	if m = press(m, "esc"); m.Searching || m.Query != "" {
		t.Fatal("esc did not cancel the search")
	}
}

func TestViewRendersAllThreeRegions(t *testing.T) {
	out := sample().View().Content
	for _, want := range []string{"One", "Alpha", "Nothing playing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("view is missing %q:\n%s", want, out)
		}
	}
	if lines := strings.Count(out, "\n") + 1; lines != 20 {
		t.Fatalf("view is %d lines, want the full 20-row height", lines)
	}
}

func TestViewIsStableBeforeFirstResize(t *testing.T) {
	// Rendering before a WindowSizeMsg must not divide by a zero width.
	if got := New().View().Content; got != "" {
		t.Fatalf("unsized view = %q, want empty", got)
	}
}

// tmux's capture-pane renders runs of spaces as tabs, which looks like a
// layout bug in a screenshot. Assert on the real output instead.
func TestNoTabsAndWidthIsExact(t *testing.T) {
	m := New()
	m.Playlists = []Playlist{{Title: "Liked Music"}}
	m.Tracks = []Track{{Title: "Poly", Artist: "DAPHNI", Duration: 6*time.Minute + 14*time.Second, Rating: RatingUp}}
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	out := sized.(Model).View().Content

	if strings.Contains(out, "\t") {
		t.Errorf("render contains tab characters")
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Errorf("line %d is %d cells wide, exceeds terminal width 100: %q", i, w, line)
		}
	}
}
