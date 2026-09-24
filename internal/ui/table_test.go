package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func tableTracks() []Track {
	return []Track{
		{VideoID: "a", Title: "Alpha", Artist: "A", Duration: time.Minute, Rating: RatingUp},
		{VideoID: "b", Title: "Beta", Artist: "B", Duration: 2 * time.Minute},
		{VideoID: "c", Title: "Gamma", Artist: "C", Duration: 3 * time.Minute},
		{VideoID: "d", Title: "Delta", Artist: "D", Duration: 4 * time.Minute},
	}
}

// The main view and the popover are one table at two sizes. Rendering the
// same tracks the same way has to give the same lines, or one of them has
// grown a renderer of its own again.
func TestTheMainViewAndThePopoverShareOneTable(t *testing.T) {
	tracks := tableTracks()

	m := New(Services{})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = sized.(Model)
	m.Tracks, m.trackCursor, m.playing = tracks, 1, tracks[2]
	m.detour = detour{
		active: true,
		tab:    Playlist{ID: "MPREb", Title: "Cherry", kind: tabAlbum},
		tracks: tracks,
		cursor: 1,
	}

	// The main list, against the table built by hand.
	width, height := 60, len(tracks)
	want := trackTable{
		tracks: tracks, cursor: 1, width: width, height: height,
		showRating: true, playing: "c",
	}.render()
	if got := m.table(width, height).render(); got != want {
		t.Errorf("the main view's rows differ from the table's:\n got %q\nwant %q", got, want)
	}

	// The popover, at its own width, against the same table at that width.
	inner, rows := m.modalContentWidth(), m.modalListHeight()
	wantModal := trackTable{
		tracks: tracks, cursor: 1, width: inner, height: rows,
		showRating: true, playing: "c",
	}.rows()
	lines := strings.Split(m.renderModal(), "\n")
	// Past the box's own border, its header and the blank line under it.
	for i, wantRow := range wantModal {
		got := lines[1+modalHeader+i]
		if !strings.Contains(got, wantRow) {
			t.Fatalf("popover row %d is not the table's:\n got %q\nwant %q", i, got, wantRow)
		}
	}
}

// A table exactly as tall as its list has nothing to scroll.
func TestTheTableOnlyDrawsABarWhenItMustScroll(t *testing.T) {
	tracks := tableTracks()
	fits := trackTable{tracks: tracks, width: 40, height: len(tracks)}
	if fits.hasScrollbar() {
		t.Error("a list that exactly fits has a bar")
	}
	for _, row := range fits.rows() {
		if strings.ContainsAny(plain(row), "█│") {
			t.Errorf("a bar is drawn anyway: %q", plain(row))
		}
	}

	tight := trackTable{tracks: tracks, width: 40, height: len(tracks) - 1}
	if !tight.hasScrollbar() {
		t.Fatal("an overflowing list has no bar")
	}
	for i, row := range tight.rows() {
		if lipgloss.Width(plain(row)) != 40 {
			t.Errorf("row %d is %d cells, want 40", i, lipgloss.Width(plain(row)))
		}
	}
}

// Every row is exactly the width it was given, whatever is in it.
func TestTableRowsAreAlwaysTheFullWidth(t *testing.T) {
	tracks := append(tableTracks(), Track{
		VideoID: "long",
		Title:   strings.Repeat("a very long title ", 10),
		Artist:  strings.Repeat("and an artist ", 10),
	})
	for _, width := range []int{12, 20, 40, 80, 200} {
		for _, height := range []int{1, 3, 8} {
			table := trackTable{tracks: tracks, cursor: 0, width: width, height: height,
				showRating: true, playing: "a"}
			rows := table.rows()
			if len(rows) != height {
				t.Fatalf("%dx%d: got %d rows", width, height, len(rows))
			}
			for i, row := range rows {
				if got := lipgloss.Width(plain(row)); got != width {
					t.Errorf("%dx%d row %d is %d cells", width, height, i, got)
				}
			}
		}
	}
}

// The window arithmetic is shared too, so both lists scroll the same way.
func TestKeepVisibleMovesTheWindowTheLeastItCan(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		cursor, offset, height, total int
		want                          int
	}{
		{"already on screen", 3, 0, 5, 20, 0},
		{"just below", 5, 0, 5, 20, 1},
		{"far below", 19, 0, 5, 20, 15},
		{"above", 2, 7, 5, 20, 2},
		{"list shorter than the window", 0, 4, 10, 3, 0},
		{"offset past the end", 0, 18, 5, 20, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := keepVisible(tc.cursor, tc.offset, tc.height, tc.total); got != tc.want {
				t.Errorf("keepVisible(%d, %d, %d, %d) = %d, want %d",
					tc.cursor, tc.offset, tc.height, tc.total, got, tc.want)
			}
		})
	}
}

func TestClampOffsetHoldsTheWindowInsideTheList(t *testing.T) {
	for _, tc := range []struct{ offset, height, total, want int }{
		{-5, 5, 20, 0},
		{0, 5, 20, 0},
		{15, 5, 20, 15},
		{99, 5, 20, 15},
		{3, 10, 4, 0}, // a list that fits cannot scroll
	} {
		if got := clampOffset(tc.offset, tc.height, tc.total); got != tc.want {
			t.Errorf("clampOffset(%d, %d, %d) = %d, want %d",
				tc.offset, tc.height, tc.total, got, tc.want)
		}
	}
}
