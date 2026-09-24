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

// An artist's page lists albums beside songs. A release has nothing to play,
// so it carries the album mark and no length.
func TestAReleaseRowIsMarkedAndHasNoLength(t *testing.T) {
	release := Track{Title: "Cherry", Artist: "DAPHNI", AlbumID: "MPREbCherry"}
	if !release.isRelease() {
		t.Fatal("not recognised as a release")
	}
	line := plain(trackLine(release, 60, true, false))
	if !strings.HasPrefix(line, iconAlbum) {
		t.Errorf("no album mark: %q", line)
	}
	if strings.Contains(line, "0:00") {
		t.Errorf("a release shows a length: %q", line)
	}

	// A song in the same table still shows its own.
	song := Track{VideoID: "v", Title: "Poly", Duration: time.Minute}
	if song.isRelease() {
		t.Fatal("a song with a video id is not a release")
	}
	if !strings.Contains(plain(trackLine(song, 60, true, false)), "1:00") {
		t.Error("a song lost its length")
	}
}

// A listing with more to fetch offers it as the last row, centred, and that
// row is not a track.
func TestTheTableOffersTheNextPage(t *testing.T) {
	tracks := tableTracks()
	table := trackTable{tracks: tracks, width: 40, height: len(tracks) + 1, more: true}

	if table.rowCount() != len(tracks)+1 {
		t.Fatalf("row count = %d, want one more than the tracks", table.rowCount())
	}
	rows := table.rows()
	last := plain(rows[len(tracks)])
	if !strings.Contains(last, "load more") {
		t.Fatalf("the last row is %q", last)
	}
	if lipgloss.Width(last) != 40 {
		t.Errorf("the last row is %d cells", lipgloss.Width(last))
	}
	// Centred, which means space either side of it.
	if strings.HasPrefix(last, "load") || strings.HasSuffix(last, "more") {
		t.Errorf("the offer is not centred: %q", last)
	}

	// Waiting for it, the row becomes the spinner.
	table.loadingMore, table.spinner = true, "▒"
	waiting := plain(table.rows()[len(tracks)])
	if !strings.Contains(waiting, "loading…") || !strings.Contains(waiting, "▒") {
		t.Errorf("the row does not say it is waiting: %q", waiting)
	}
	if strings.Contains(waiting, "load more") {
		t.Errorf("it still offers what it is already fetching: %q", waiting)
	}
}

// Without more to fetch there is no extra row.
func TestACompleteListingOffersNothing(t *testing.T) {
	tracks := tableTracks()
	table := trackTable{tracks: tracks, width: 40, height: len(tracks) + 1}
	if table.rowCount() != len(tracks) {
		t.Fatalf("row count = %d", table.rowCount())
	}
	for _, row := range table.rows() {
		if strings.Contains(plain(row), "load more") {
			t.Errorf("a complete listing offers more: %q", plain(row))
		}
	}
}
