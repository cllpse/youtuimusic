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
	// An artist, not an album: an album's table is deliberately narrower,
	// and this is about the two sharing one renderer.
	m.detour = detour{
		active: true,
		tab:    Playlist{ID: "UCd", Title: "DAPHNI", kind: tabArtist},
		tracks: tracks,
		cursor: 1,
	}

	// The main list, against the table built by hand.
	width, height := 60, len(tracks)+headerRows
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
	fits := trackTable{tracks: tracks, width: 40, height: len(tracks) + headerRows}
	if fits.hasScrollbar() {
		t.Error("a list that exactly fits has a bar")
	}
	for _, row := range fits.rows() {
		if strings.ContainsAny(plain(row), "█│") {
			t.Errorf("a bar is drawn anyway: %q", plain(row))
		}
	}

	tight := trackTable{tracks: tracks, width: 40, height: len(tracks) - 1 + headerRows}
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
	table := trackTable{tracks: []Track{release}, width: 60, height: 3, showRating: true}
	line := plain(table.trackLine(release, table.layout(60), false))
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
	songs := trackTable{tracks: []Track{song}, width: 60, height: 3, showRating: true}
	if !strings.Contains(plain(songs.trackLine(song, songs.layout(60), false)), "1:00") {
		t.Error("a song lost its length")
	}
}

// A listing with more to fetch offers it as the last row, centred, and that
// row is not a track.
func TestTheTableOffersTheNextPage(t *testing.T) {
	tracks := tableTracks()
	table := trackTable{tracks: tracks, width: 40, height: len(tracks) + 1 + headerRows, more: true}

	if table.rowCount() != len(tracks)+1 {
		t.Fatalf("row count = %d, want one more than the tracks", table.rowCount())
	}
	rows := table.rows()
	last := plain(rows[len(tracks)+headerRows])
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
	table.loadingMore, table.loader = true, "▒ Loading…"
	waiting := plain(table.rows()[len(tracks)+headerRows])
	if !strings.Contains(waiting, loaderLabel) || !strings.Contains(waiting, "▒") {
		t.Errorf("the row does not say it is waiting: %q", waiting)
	}
	if strings.Contains(waiting, "load more") {
		t.Errorf("it still offers what it is already fetching: %q", waiting)
	}
}

// Without more to fetch there is no extra row.
func TestACompleteListingOffersNothing(t *testing.T) {
	tracks := tableTracks()
	table := trackTable{tracks: tracks, width: 40, height: len(tracks) + 1 + headerRows}
	if table.rowCount() != len(tracks) {
		t.Fatalf("row count = %d", table.rowCount())
	}
	for _, row := range table.rows() {
		if strings.Contains(plain(row), "load more") {
			t.Errorf("a complete listing offers more: %q", plain(row))
		}
	}
}

// The header names the columns and marks the one in use.
func TestTheHeaderNamesTheColumns(t *testing.T) {
	table := trackTable{tracks: tableTracks(), width: 70, height: 6, showRating: true}
	header := plain(table.rows()[0])

	for _, want := range []string{"Title", "Artist", "Length"} {
		if !strings.Contains(header, want) {
			t.Errorf("header is missing %q: %q", want, header)
		}
	}
	if strings.Contains(header, "Added") {
		t.Errorf("an undated list has an Added column: %q", header)
	}
	if lipgloss.Width(header) != 70 {
		t.Errorf("header is %d cells, want 70", lipgloss.Width(header))
	}
	if strings.ContainsAny(header, "↑↓") {
		t.Errorf("an unsorted table marks a column: %q", header)
	}

	table.sort = sortSpec{by: sortArtist}
	if got := plain(table.rows()[0]); !strings.Contains(got, "Artist↑") {
		t.Errorf("the sorted column is not marked: %q", got)
	}
	table.sort = sortSpec{by: sortArtist, desc: true}
	if got := plain(table.rows()[0]); !strings.Contains(got, "Artist↓") {
		t.Errorf("the direction is not shown: %q", got)
	}
}

// A dated listing gets the extra column; an undated one does not, because a
// column of blanks is worse than no column.
func TestTheAddedColumnAppearsOnlyWhenThereAreDates(t *testing.T) {
	tracks := tableTracks()
	plainTable := trackTable{tracks: tracks, width: 80, height: 6}
	if plainTable.showsAdded() {
		t.Error("an undated list shows the column")
	}

	tracks[1].Added = time.Date(2024, 8, 2, 0, 0, 0, 0, time.UTC)
	dated := trackTable{tracks: tracks, width: 80, height: 6,
		now: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)}
	if !dated.showsAdded() {
		t.Fatal("a dated list does not show the column")
	}
	header := plain(dated.rows()[0])
	if !strings.Contains(header, "Added") {
		t.Errorf("header is missing the column: %q", header)
	}
	if got := plain(dated.rows()[1+1]); !strings.Contains(got, "2 Aug 2024") {
		t.Errorf("the date is not on its row: %q", got)
	}
	// Every row is still exactly the width.
	for i, row := range dated.rows() {
		if w := lipgloss.Width(plain(row)); w != 80 {
			t.Errorf("row %d is %d cells", i, w)
		}
	}
}

func TestHumanDate(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   time.Time
		want string
	}{
		{time.Time{}, ""},
		{now, "today"},
		{now.Add(-25 * time.Hour), "yesterday"},
		{now.Add(-3 * 24 * time.Hour), "3 days ago"},
		{now.Add(-9 * 24 * time.Hour), "last week"},
		{now.Add(-21 * 24 * time.Hour), "3 weeks ago"},
		{now.Add(-200 * 24 * time.Hour), "8 Mar 2026"},
		{time.Date(2024, 8, 2, 0, 0, 0, 0, time.UTC), "2 Aug 2024"},
	} {
		if got := humanDate(tc.in, now); got != tc.want {
			t.Errorf("humanDate(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSortTracks(t *testing.T) {
	jan := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	feb := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	tracks := []Track{
		{Title: "beta", Artist: "Zappa", Duration: 3 * time.Minute, Added: feb},
		{Title: "Alpha", Artist: "abba", Duration: time.Minute, Added: jan},
		{Title: "Gamma", Artist: "Móna", Duration: 2 * time.Minute},
	}
	titles := func(ts []Track) []string {
		out := make([]string, len(ts))
		for i, t := range ts {
			out[i] = t.Title
		}
		return out
	}

	for _, tc := range []struct {
		name string
		spec sortSpec
		want []string
	}{
		{"unsorted", sortSpec{}, []string{"beta", "Alpha", "Gamma"}},
		{"title", sortSpec{by: sortTitle}, []string{"Alpha", "beta", "Gamma"}},
		{"title reversed", sortSpec{by: sortTitle, desc: true}, []string{"Gamma", "beta", "Alpha"}},
		{"artist ignores case", sortSpec{by: sortArtist}, []string{"Alpha", "Gamma", "beta"}},
		{"length", sortSpec{by: sortLength}, []string{"Alpha", "Gamma", "beta"}},
		{"added, undated first", sortSpec{by: sortAdded}, []string{"Gamma", "Alpha", "beta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := append([]Track(nil), tracks...)
			sortTracks(got, tc.spec)
			if strings.Join(titles(got), ",") != strings.Join(tc.want, ",") {
				t.Errorf("got %v, want %v", titles(got), tc.want)
			}
		})
	}
}

// Cycling stops at the last column there is, and comes back to unsorted so
// a list can be put back the way it arrived.
func TestSortCycles(t *testing.T) {
	var spec sortSpec
	for _, want := range []sortColumn{sortTitle, sortArtist, sortLength, sortNone} {
		spec = spec.next(false)
		if spec.by != want {
			t.Fatalf("cycled to %v, want %v", spec.by, want)
		}
	}
	// With dates there is one more stop.
	spec = sortSpec{by: sortLength}
	if spec = spec.next(true); spec.by != sortAdded {
		t.Errorf("a dated list skips Added: %v", spec.by)
	}
}

// Clicking a column orders by it, and clicking it again reverses.
func TestSortOnAColumn(t *testing.T) {
	spec := sortSpec{}.on(sortArtist)
	if spec.by != sortArtist || spec.desc {
		t.Fatalf("first click gave %+v", spec)
	}
	if spec = spec.on(sortArtist); !spec.desc {
		t.Errorf("the second click did not reverse: %+v", spec)
	}
	if spec = spec.on(sortTitle); spec.by != sortTitle || spec.desc {
		t.Errorf("moving to another column kept the direction: %+v", spec)
	}
}

// A column's label has to fit with its arrow, or the header says which
// column is sorted but not which way.
func TestEveryHeaderLabelFitsWithItsArrow(t *testing.T) {
	tracks := tableTracks()
	tracks[0].Added = time.Now()
	for _, by := range []sortColumn{sortTitle, sortArtist, sortLength, sortAdded} {
		for _, desc := range []bool{false, true} {
			table := trackTable{
				tracks: tracks, width: 80, height: 6,
				sort: sortSpec{by: by, desc: desc}, now: time.Now(),
			}
			header := plain(table.rows()[0])
			if strings.Contains(header, "…") {
				t.Errorf("sorting by %v cut a label short: %q", by, header)
			}
			arrow := "↑"
			if desc {
				arrow = "↓"
			}
			if !strings.Contains(header, arrow) {
				t.Errorf("sorting by %v lost its arrow: %q", by, header)
			}
		}
	}
}

// truncate counts screen cells, which is not the same as counting runes: a
// CJK character or an emoji takes two cells, a combining mark none.
//
// Cutting by rune index against a width measured in cells panicked on the
// first title that was not Latin — "slice bounds out of range [:41] with
// capacity 32", from a thirty-two character title seventy cells wide.
func TestTruncateCountsCellsNotRunes(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		w    int
		want string
	}{
		{"fits", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"latin", "hello there", 5, "hell…"},
		{"no room", "hello", 1, "…"},
		{"zero", "hello", 0, ""},
		{"negative", "hello", -3, ""},
		// Each of these is two cells wide, so five fit in ten.
		{"cjk fits", "宇多田ヒカル", 12, "宇多田ヒカル"},
		{"cjk cut", "宇多田ヒカル", 7, "宇多田…"},
		{"cjk odd width", "宇多田ヒカル", 6, "宇多…"},
		{"emoji", "party 🎉🎉🎉🎉", 9, "party 🎉…"},
		{"combining marks are free", "ééé", 3, "ééé"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.w)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
			}
			if w := lipgloss.Width(got); w > max(tc.w, 0) {
				t.Errorf("result is %d cells, past the %d asked for", w, tc.w)
			}
		})
	}
}

// Whatever it is given, it must not exceed the width and must not panic.
func TestTruncateNeverOverrunsOrPanics(t *testing.T) {
	inputs := []string{
		"", "a", "ascii title here",
		"宇多田ヒカル - First Love (オリジナル)",
		"🎉🎉🎉🎉🎉🎉🎉🎉",
		"mixed 日本語 and latin and 🎉",
		strings.Repeat("字", 40),
		// The shape that crashed: thirty-five characters, seventy cells.
		strings.Repeat("字", 35),
		"é" + strings.Repeat("字", 10),
	}
	for _, in := range inputs {
		for w := -2; w < 50; w++ {
			got := truncate(in, w)
			if cells := lipgloss.Width(got); cells > max(w, 0) {
				t.Errorf("truncate(%q, %d) is %d cells", in, w, cells)
			}
		}
	}
}

// And a table of such titles still lays out square.
func TestRowsAreSquareWithWideCharacters(t *testing.T) {
	tracks := []Track{
		{VideoID: "a", Title: "宇多田ヒカル - First Love", Artist: "宇多田ヒカル", Duration: time.Minute},
		{VideoID: "b", Title: strings.Repeat("字", 40), Artist: "🎉🎉🎉🎉", Duration: time.Minute},
		{VideoID: "c", Title: "plain", Artist: "plain", Duration: time.Minute},
	}
	for _, width := range []int{20, 42, 60, 110} {
		table := trackTable{tracks: tracks, width: width, height: len(tracks) + headerRows,
			showRating: true, now: time.Now()}
		for i, row := range table.rows() {
			if got := lipgloss.Width(plain(row)); got != width {
				t.Errorf("width %d: row %d is %d cells", width, i, got)
			}
		}
	}
}

// An album is one artist's record, so naming them down the page says the
// same thing on every row.
func TestAnAlbumsTableIsTitlesOnly(t *testing.T) {
	tracks := tableTracks()
	tracks[0].Added = time.Now()

	full := trackTable{tracks: tracks, width: 70, height: 6, showRating: true, now: time.Now()}
	album := full
	album.titleOnly = true

	header := plain(album.rows()[0])
	if !strings.Contains(header, "Title") {
		t.Errorf("no title column: %q", header)
	}
	for _, gone := range []string{"Artist", "Length", "Added"} {
		if strings.Contains(header, gone) {
			t.Errorf("%s is still a column: %q", gone, header)
		}
	}
	// The full table still has them, so the comparison means something.
	if got := plain(full.rows()[0]); !strings.Contains(got, "Artist") {
		t.Fatalf("the full table lost its columns too: %q", got)
	}

	// The rows carry only the title, and still fill the width.
	for i, row := range album.rows() {
		if lipgloss.Width(plain(row)) != 70 {
			t.Errorf("row %d is %d cells", i, lipgloss.Width(plain(row)))
		}
	}
	// Exactly the mark and the title, and nothing after them. Checked whole
	// rather than by substring: the artist here is "A", which is inside
	// "Alpha".
	first := plain(album.rows()[1])
	if got, want := strings.TrimSpace(first), iconThumbUp+" Alpha"; got != want {
		t.Errorf("row reads %q, want %q", got, want)
	}

	// And only that column answers to a click.
	spans := album.headerSpans()
	if len(spans) != 1 || spans[0].by != sortTitle {
		t.Errorf("header spans = %+v", spans)
	}
}
