package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cllpse/youtuimusic/internal/ytm"
)

// titles is what a list reads as, for comparing orders.
func titles(ts []Track) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Title
	}
	return out
}

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
		showRating: true, playing: "c", highlight: m.highlightColor(),
	}.render()
	if got := m.table(width, height).render(); got != want {
		t.Errorf("the main view's rows differ from the table's:\n got %q\nwant %q", got, want)
	}

	// The popover, at its own width, against the same table at that width.
	inner, rows := m.modalContentWidth(), m.modalListHeight()
	wantModal := trackTable{
		tracks: tracks, cursor: 1, width: inner, height: rows,
		showRating: true, playing: "c", highlight: m.highlightColor(),
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
	table := trackTable{tracks: tableTracks(), width: 70, height: 6, showRating: true,
		sortable: true}
	header := plain(table.rows()[0])

	for _, want := range []string{"Title", "Artist", "Length"} {
		if !strings.Contains(header, want) {
			t.Errorf("header is missing %q: %q", want, header)
		}
	}
	if strings.Contains(header, "Added") {
		t.Errorf("the Added column is back: %q", header)
	}
	if lipgloss.Width(header) != 70 {
		t.Errorf("header is %d cells, want 70", lipgloss.Width(header))
	}
	if strings.ContainsAny(header, "↑↓") {
		t.Errorf("an unsorted table marks a column: %q", header)
	}

	table.sort = sortSpec{by: sortArtist}
	if got := plain(table.rows()[0]); !strings.Contains(got, "Artist ↑") {
		t.Errorf("the sorted column is not marked: %q", got)
	}
	table.sort = sortSpec{by: sortArtist, desc: true}
	if got := plain(table.rows()[0]); !strings.Contains(got, "Artist ↓") {
		t.Errorf("the direction is not shown: %q", got)
	}
}

func TestSortTracks(t *testing.T) {

	tracks := []Track{
		{Title: "beta", Artist: "Zappa", Duration: 3 * time.Minute},
		{Title: "Alpha", Artist: "abba", Duration: time.Minute},
		{Title: "Gamma", Artist: "Móna", Duration: 2 * time.Minute},
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
		spec = spec.next()
		if spec.by != want {
			t.Fatalf("cycled to %v, want %v", spec.by, want)
		}
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
	for _, by := range []sortColumn{sortTitle, sortArtist, sortLength} {
		for _, desc := range []bool{false, true} {
			table := trackTable{
				tracks: tracks, width: 80, height: 6, sortable: true,
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

	full := trackTable{tracks: tracks, width: 70, height: 6, showRating: true,
		sortable: true, now: time.Now()}
	album := full
	album.titleOnly = true

	header := plain(album.rows()[0])
	if !strings.Contains(header, "Title") {
		t.Errorf("no title column: %q", header)
	}
	for _, gone := range []string{"Artist", "Length"} {
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

// The flag is the whole point of it: the same table, sortable or not.
func TestSortingCanBeTurnedOff(t *testing.T) {
	tracks := tableTracks()
	base := trackTable{tracks: tracks, width: 70, height: 6, showRating: true,
		sort: sortSpec{by: sortArtist}, now: time.Now()}

	on := base
	on.sortable = true
	if got := plain(on.rows()[0]); !strings.Contains(got, "Artist ↑") {
		t.Errorf("a sortable table does not mark its column: %q", got)
	}
	if len(on.headerSpans()) == 0 {
		t.Error("a sortable table offers no columns to click")
	}

	off := base
	if got := plain(off.rows()[0]); strings.ContainsAny(got, "↑↓") {
		t.Errorf("an unsortable table still marks a column: %q", got)
	}
	// Still a header, just not a control.
	if got := plain(off.rows()[0]); !strings.Contains(got, "Artist") {
		t.Errorf("an unsortable table lost its labels: %q", got)
	}
	if spans := off.headerSpans(); spans != nil {
		t.Errorf("an unsortable table answers clicks: %+v", spans)
	}
	// Both are the same width, so turning sorting off shifts nothing.
	if a, b := lipgloss.Width(plain(on.rows()[0])), lipgloss.Width(plain(off.rows()[0])); a != b {
		t.Errorf("the header is %d cells sortable and %d not", a, b)
	}
}

// The arrow is a mark beside the name, not another letter of it.
func TestTheArrowIsSpacedFromTheLabel(t *testing.T) {
	for _, by := range []sortColumn{sortTitle, sortArtist, sortLength} {
		table := trackTable{tracks: tableTracks(), width: 80, height: 6,
			sortable: true, sort: sortSpec{by: by}, now: time.Now()}
		header := plain(table.rows()[0])
		at := strings.IndexAny(header, "↑↓")
		if at <= 0 {
			t.Fatalf("no arrow for %v: %q", by, header)
		}
		if header[at-1] != ' ' {
			t.Errorf("the arrow is against the label: %q", header)
		}
	}
}

// An order is asked for about one listing. It does not follow the reader to
// the next playlist, which has an order of its own.
func TestSwitchingPlaylistsClearsTheSort(t *testing.T) {
	lib := library()
	lib.tracks["PL1"] = []ytm.Track{
		{VideoID: "z", Title: "Zeta", Artist: "Z", Duration: time.Minute},
		{VideoID: "a", Title: "Alpha", Artist: "A", Duration: time.Minute},
	}
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m = drain(t, m, m.Init())

	next, cmd := m.Update(keyPress("s"))
	m = drain(t, next.(Model), cmd)
	if m.sort.by != sortTitle {
		t.Fatalf("the first list did not sort: %+v", m.sort)
	}

	next, cmd = m.Update(keyPress("l"))
	m = drain(t, next.(Model), cmd)

	if m.sort.by != sortNone {
		t.Errorf("the order followed to the next playlist: %+v", m.sort)
	}
	// And the new listing really is in the order it arrived in.
	if len(m.Tracks) != 2 {
		t.Fatalf("the next playlist has %d tracks", len(m.Tracks))
	}
	if m.Tracks[0].Title != "Zeta" {
		t.Errorf("the listing is not in its own order: %v", titles(m.Tracks))
	}
	// The header stops saying it is sorted, too.
	header := plain(m.table(m.width, m.bodyHeight()).rows()[0])
	if strings.ContainsAny(header, "↑↓") {
		t.Errorf("the header still marks a column: %q", header)
	}
}

// Coming back to a playlist shows it the way it arrives, not the way it was
// last left.
func TestReturningToAPlaylistDoesNotKeepItsOldSort(t *testing.T) {
	lib := library()
	lib.tracks["PL1"] = []ytm.Track{{VideoID: "z", Title: "Zeta", Duration: time.Minute}}
	m := wired(t, lib, &fakeStreams{}, newFakeAudio())
	m = drain(t, m, m.Init())

	next, cmd := m.Update(keyPress("s"))
	m = drain(t, next.(Model), cmd)
	next, cmd = m.Update(keyPress("l"))
	m = drain(t, next.(Model), cmd)
	next, cmd = m.Update(keyPress("h"))
	m = drain(t, next.(Model), cmd)

	if m.sort.by != sortNone {
		t.Errorf("the old order came back with the playlist: %+v", m.sort)
	}
	if m.Tracks[0].Title != "Alpha" {
		t.Errorf("the listing is not in its own order: %v", titles(m.Tracks))
	}
}

// A long list can only colour the playing row while it is on screen. The
// scrollbar says where it is the rest of the time.
func TestTheScrollbarMarksThePlayingTrack(t *testing.T) {
	var tracks []Track
	for i := range 100 {
		tracks = append(tracks, Track{
			VideoID: fmt.Sprintf("v%d", i), Title: "Track", Duration: time.Minute,
		})
	}
	const height = 10
	table := trackTable{tracks: tracks, width: 60, height: height + headerRows,
		playing: "v50"}

	bar := scrollbarFor(table.rowCount(), table.offset, height, table.playingRow())
	if len(bar) != height {
		t.Fatalf("the scrollbar is %d cells", len(bar))
	}

	// The mark is a block like the thumb, so it is the colour that finds it.
	at := -1
	for i, cell := range bar {
		if sgrCodes(cell)["34"] {
			if at >= 0 {
				t.Errorf("the mark is on rows %d and %d", at, i)
			}
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no mark in the scrollbar: %q", plainAll(bar))
	}
	// Halfway down the list, so halfway down the trough.
	if want := 5; at != want {
		t.Errorf("the mark is at %d, want %d", at, want)
	}
	if got := plain(bar[at]); !strings.Contains(got, "█") {
		t.Errorf("the mark is not a block: %q", got)
	}
}

// The ends of the list are reachable: a mark must never fall off the trough.
func TestThePlayingMarkStaysOnTheTrough(t *testing.T) {
	var tracks []Track
	for i := range 37 {
		tracks = append(tracks, Track{VideoID: fmt.Sprintf("v%d", i), Title: "T"})
	}
	const height = 8
	for _, at := range []int{0, 1, 18, 35, 36} {
		table := trackTable{tracks: tracks, width: 60, height: height + headerRows,
			playing: fmt.Sprintf("v%d", at)}
		bar := scrollbarFor(table.rowCount(), table.offset, height, table.playingRow())
		// Found by colour: the mark is a block wherever it lands.
		found := -1
		for i, cell := range bar {
			if sgrCodes(cell)["34"] {
				found = i
			}
		}
		if found < 0 || found >= height {
			t.Errorf("track %d marked at %d, outside 0..%d", at, found, height-1)
		}
	}
}

// Nothing playing, or playing something from another tab, means no mark.
func TestNoMarkForATrackThatIsNotInTheList(t *testing.T) {
	var tracks []Track
	for i := range 40 {
		tracks = append(tracks, Track{VideoID: fmt.Sprintf("v%d", i), Title: "T"})
	}
	for _, playing := range []string{"", "somewhere-else"} {
		table := trackTable{tracks: tracks, width: 60, height: 10 + headerRows,
			playing: playing}
		if got := table.playingRow(); got != -1 {
			t.Errorf("playing %q gave row %d", playing, got)
		}
		bar := scrollbarFor(table.rowCount(), table.offset, 10, table.playingRow())
		for i, cell := range bar {
			if sgrCodes(cell)["34"] {
				t.Errorf("playing %q still marked row %d", playing, i)
			}
		}
	}
}

func plainAll(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = plain(c)
	}
	return out
}

// The mark and the thumb land on the same cell whenever the playing track
// is on screen. Neither may swallow the other.
func TestTheMarkAndTheThumbShareACell(t *testing.T) {
	var tracks []Track
	for i := range 300 {
		tracks = append(tracks, Track{VideoID: fmt.Sprintf("v%d", i), Title: "T"})
	}
	const height = 10
	// A long list moves its thumb by well under a cell per row, so at the
	// top both the thumb and a mark for an early track are on row 0.
	table := trackTable{tracks: tracks, width: 60, height: height + headerRows,
		playing: "v0"}
	bar := scrollbarFor(table.rowCount(), table.offset, height, table.playingRow())

	cell := bar[0]
	if !strings.Contains(plain(cell), "█") {
		t.Errorf("the thumb lost its shape: %q", plain(cell))
	}
	if codes := sgrCodes(cell); !codes["34"] {
		t.Errorf("the shared cell does not say the track is there: %v", codes)
	}
	// Below the thumb the trough is its ordinary self.
	if got := plain(bar[height-1]); !strings.Contains(got, "│") {
		t.Errorf("the bottom of the trough is %q", got)
	}
}
