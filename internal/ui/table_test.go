package ui

import (
	"fmt"
	"image/color"
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
	// Inactive, because the popover this test opens is in front of it. That
	// is the main view's state here, not a property of the component.
	want := trackTable{
		tracks: tracks, cursor: 1, width: width, height: height,
		showRating: true, playing: "c", highlight: m.highlightColor(),
		inactive: true, quiet: m.quietColor(),
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
	rendered := table.trackLine(release, table.layout(60), false)
	line := plain(rendered)
	// An album is the artist's own work rather than a song of theirs, and
	// weight is what says so now that nothing carries an icon.
	if !sgrCodes(rendered)["1"] {
		t.Errorf("a release is not bold: %v", sgrCodes(rendered))
	}
	if !strings.HasPrefix(line, release.Title) {
		t.Errorf("the title does not start the row: %q", line)
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
	if !strings.Contains(last, labelLoadMore) {
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
	if strings.Contains(waiting, labelLoadMore) {
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
		if strings.Contains(plain(row), labelLoadMore) {
			t.Errorf("a complete listing offers more: %q", plain(row))
		}
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
		now: time.Now()}
	album := full
	album.titleOnly = true

	// One column takes the whole width, where the full table shares it out.
	if got, want := album.layout(70).title, 70; got != want {
		t.Errorf("the title column is %d wide, want all %d of it", got, want)
	}
	if full.layout(70).artist == 0 {
		t.Fatal("the full table has no artist column, so this proves nothing")
	}

	// The rows carry only the title, and still fill the width.
	for i, row := range album.rows() {
		if lipgloss.Width(plain(row)) != 70 {
			t.Errorf("row %d is %d cells", i, lipgloss.Width(plain(row)))
		}
	}
	// Exactly the title and nothing after it. Checked whole rather than by
	// substring: the artist here is "A", which is inside "Alpha".
	first := plain(album.rows()[0])
	if got, want := strings.TrimSpace(first), "Alpha"; got != want {
		t.Errorf("row reads %q, want %q", got, want)
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

	bar := table.scrollbar()
	if len(bar) != height {
		t.Fatalf("the scrollbar is %d cells", len(bar))
	}

	// The mark is a block like the thumb, so it is the colour that finds it.
	at := -1
	for i, cell := range bar {
		if sgrCodes(cell)[liveFG] {
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
		bar := table.scrollbar()
		// Found by colour: the mark is a block wherever it lands.
		found := -1
		for i, cell := range bar {
			if sgrCodes(cell)[liveFG] {
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
		bar := table.scrollbar()
		for i, cell := range bar {
			if sgrCodes(cell)[liveFG] {
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
	bar := table.scrollbar()

	cell := bar[0]
	if !strings.Contains(plain(cell), "█") {
		t.Errorf("the thumb lost its shape: %q", plain(cell))
	}
	if codes := sgrCodes(cell); !codes[liveFG] {
		t.Errorf("the shared cell does not say the track is there: %v", codes)
	}
	// Below the thumb the trough is its ordinary self.
	if got := plain(bar[height-1]); !strings.Contains(got, "│") {
		t.Errorf("the bottom of the trough is %q", got)
	}
}

// The columns are shares of what is left once the mark and the separators
// are taken: most of it to the title, a third of the rest to the artist, the
// remainder to the length.
func TestTheColumnsAreSharedSixtyThirtyTen(t *testing.T) {
	for _, width := range []int{40, 60, 80, 100, 120} {
		table := trackTable{tracks: tableTracks(), width: width, height: 6}
		cols := table.layout(width)
		spare := width - 2

		if want := spare * titleShare / 100; cols.title != want {
			t.Errorf("%d: title is %d, want %d", width, cols.title, want)
		}
		if want := spare * lengthShare / 100; cols.length != want && cols.length != minLength {
			t.Errorf("%d: length is %d, want %d or the %d floor",
				width, cols.length, want, minLength)
		}
		if cols.length < minLength {
			t.Errorf("%d: length is %d, too narrow for MM:SS", width, cols.length)
		}
		// Nothing is lost or invented between them.
		if got := cols.title + cols.artist + cols.length; got != spare {
			t.Errorf("%d: the columns come to %d, want %d", width, got, spare)
		}
		// And the row that comes out is exactly the width asked for.
		for i, row := range table.rows() {
			if got := lipgloss.Width(plain(row)); got != width {
				t.Errorf("%d: row %d is %d cells", width, i, got)
			}
		}
	}
}

// One column takes all of it, because there is nothing to share with.
func TestASingleColumnTakesTheWholeWidth(t *testing.T) {
	table := trackTable{tracks: tableTracks(), width: 80, height: 6, titleOnly: true}
	cols := table.layout(80)
	if want := 80; cols.title != want {
		t.Errorf("title is %d, want %d", cols.title, want)
	}
	if cols.artist != 0 || cols.length != 0 {
		t.Errorf("the other columns are %d and %d", cols.artist, cols.length)
	}
}

// The columns are not named. A table of songs is legible without being told
// that the titles are titles, and the row it cost was the row it cost.
func TestTheTableHasNoHeader(t *testing.T) {
	tracks := tableTracks()
	table := trackTable{tracks: tracks, width: 80, height: len(tracks), showRating: true}

	rows := table.rows()
	if len(rows) != len(tracks) {
		t.Fatalf("%d rows for %d tracks", len(rows), len(tracks))
	}
	// The first row is a track, not a word about one.
	if got := plain(rows[0]); !strings.Contains(got, tracks[0].Title) {
		t.Errorf("the first row is %q, want the first track", got)
	}
	for _, word := range []string{"Title", "Artist", "Length"} {
		if strings.Contains(plain(strings.Join(rows, "\n")), word) {
			t.Errorf("the table still names %q", word)
		}
	}
	if headerRows != 0 {
		t.Errorf("headerRows is %d", headerRows)
	}
}

// The last column is read against the right edge, so it is set there. Only
// where there is more than one: a single column takes the width and its text
// starts at the left like any other title.
func TestTheLastColumnIsRightAligned(t *testing.T) {
	tracks := tableTracks()
	for _, width := range []int{50, 80, 120} {
		table := trackTable{tracks: tracks, width: width, height: len(tracks),
			showRating: true}
		cols := table.layout(width)
		row := plain(table.trackLine(tracks[0], cols, false))

		if got := lipgloss.Width(row); got != width {
			t.Fatalf("%d: the row is %d cells", width, got)
		}
		// Nothing after the duration but the edge.
		if strings.HasSuffix(row, " ") {
			t.Errorf("%d: the last column is not against the edge: %q", width, row)
		}
		want := formatDuration(tracks[0].Duration)
		if !strings.HasSuffix(row, strings.TrimSpace(want)) {
			t.Errorf("%d: the row ends %q, want it to end in %q", width, row, want)
		}
	}

	// One column, and the title starts where every title does.
	one := trackTable{tracks: tracks, width: 80, height: len(tracks), titleOnly: true}
	row := plain(one.trackLine(tracks[1], one.layout(80), false))
	if !strings.HasPrefix(row, tracks[1].Title) {
		t.Errorf("a single column does not start at the edge: %q", row)
	}
}

// A rating is the colour of the row and nothing in the row: no mark in front
// of the title, no room held for one, every title at the edge.
func TestARatingIsAColourAndNotAMark(t *testing.T) {
	up := Track{VideoID: "a", Title: "Alpha", Artist: "A", Rating: RatingUp}
	down := Track{VideoID: "b", Title: "Beta", Artist: "B", Rating: RatingDown}
	plainTrack := Track{VideoID: "c", Title: "Gamma", Artist: "C"}
	tracks := []Track{up, down, plainTrack}
	table := trackTable{tracks: tracks, width: 80, height: 3, showRating: true}
	cols := table.layout(80)

	for _, track := range tracks {
		row := plain(table.trackLine(track, cols, false))
		if !strings.HasPrefix(row, track.Title) {
			t.Errorf("%q starts %q, want the title at the edge", track.Title, row)
		}
	}

	// The colour comes from the row's style instead, and only where there is
	// something to say.
	for _, tc := range []struct {
		track Track
		hue   color.Color
		rated bool
	}{
		{up, liked, true},
		{down, disliked, true},
		{plainTrack, nil, false},
	} {
		hue, rated := table.ratingHue(tc.track)
		if rated != tc.rated || hue != tc.hue {
			t.Errorf("%q is drawn in %v (rated %v), want %v (%v)",
				tc.track.Title, hue, rated, tc.hue, tc.rated)
		}
	}

	// And the liked playlist says nothing at all about a rating.
	quiet := table
	quiet.showRating = false
	if _, rated := quiet.ratingHue(up); rated {
		t.Error("a block that shows no ratings still coloured one")
	}
}

// An inactive block is one colour run per row, not a row with dim parts in
// it. That matters beyond looks: a nested style ends in a reset, and a reset
// inside the row would drop the colour from there to the end of the line.
func TestAnInactiveRowIsOneColourAllTheWayAcross(t *testing.T) {
	tracks := tableTracks()
	live := trackTable{tracks: tracks, width: 50, height: len(tracks),
		showRating: true, cursor: 0, playing: tracks[0].VideoID,
		highlight: surface}
	off := live
	off.inactive, off.quiet = true, color.RGBA{0xBF, 0xBF, 0xBF, 0xFF}
	want := "\x1b[38;2;191;191;191m"

	// Live, the title is plain and the columns after it are faint.
	liveRow := live.rows()[1]
	if strings.HasPrefix(liveRow, "\x1b[2m") {
		t.Errorf("a live row starts faint: %q", liveRow)
	}
	if !strings.Contains(liveRow, "\x1b[2m") {
		t.Fatalf("a live row has no faint columns, so this proves nothing: %q", liveRow)
	}

	// Inactive, the whole row is one run of the quiet colour.
	offRow := off.rows()[1]
	if !strings.HasPrefix(offRow, want) {
		t.Errorf("an inactive row does not start in the quiet colour: %q", offRow)
	}
	if strings.Contains(offRow, "\x1b[2m") {
		t.Errorf("an inactive row is faint as well as coloured: %q", offRow)
	}
	// One reset, at the end of the row's own text — anything earlier would
	// drop the colour for the rest of the line.
	if n := strings.Count(offRow, "\x1b[m"); n != 1 {
		t.Errorf("an inactive row has %d resets in it: %q", n, offRow)
	}

	// And nothing in the block is rated, selected or playing any more.
	whole := strings.Join(off.rows(), "\n")
	for _, hue := range []string{liveFG, likedFG, dislikedFG} {
		if sgrCodes(whole)[hue] {
			t.Errorf("an inactive block still carries SGR %s: %q", hue, whole)
		}
	}
	if sgrCodes(whole)[highlightSGR] {
		t.Errorf("an inactive block still fills the cursor row: %q", whole)
	}
	// The live one does, so the comparison means something. The first track
	// here is both playing and liked, which is the precedence rowStyle sets:
	// the hue is the rating and the weight is the player, so a liked track
	// does not stop looking liked the moment it starts playing.
	liveWhole := strings.Join(live.rows(), "\n")
	if !sgrCodes(liveWhole)[likedFG] || !sgrCodes(liveWhole)[highlightSGR] {
		t.Fatalf("the live block marks neither: %q", liveWhole)
	}
	if sgrCodes(liveWhole)[liveFG] {
		t.Errorf("the rating did not displace the player's blue: %q", liveWhole)
	}
	if !sgrCodes(liveWhole)["1"] {
		t.Errorf("the playing row lost its weight: %q", liveWhole)
	}
}

// trough is a list long enough to need a scrollbar, with four rows to each
// cell of it: enough for several things to fall in one cell.
func trough(n int) []Track {
	out := make([]Track, n)
	for i := range out {
		out[i] = Track{VideoID: fmt.Sprintf("v%d", i), Title: "T"}
	}
	return out
}

// The trough marks what you think of the tracks in it, in the colours the rows
// themselves take, beside the mark for what is playing.
func TestTheScrollbarMarksRatedTracks(t *testing.T) {
	const height, total = 10, 40
	tracks := trough(total)
	tracks[0].Rating = RatingUp    // cell 0, under the thumb
	tracks[20].Rating = RatingDown // cell 5, on the bare trough
	table := trackTable{tracks: tracks, width: 60, height: height + headerRows,
		showRating: true}

	bar := table.scrollbar()
	if len(bar) != height {
		t.Fatalf("the scrollbar is %d cells, want %d", len(bar), height)
	}
	want := map[int]string{0: likedFG, 5: dislikedFG}
	for i, cell := range bar {
		code, marked := want[i]
		if marked {
			if !sgrCodes(cell)[code] {
				t.Errorf("cell %d is not SGR %s: %q", i, code, cell)
			}
			// A mark is a block wherever it lands, thumb or trough, so that
			// it reads the same in both.
			if got := plain(cell); !strings.Contains(got, "█") {
				t.Errorf("the mark in cell %d is not a block: %q", i, got)
			}
			continue
		}
		if sgrCodes(cell)[likedFG] || sgrCodes(cell)[dislikedFG] {
			t.Errorf("cell %d is marked and holds nothing rated: %q", i, cell)
		}
	}
}

// A cell can only be one colour, so where several things fall in one it says
// the most notable: a dislike over a like, and what is playing over both.
func TestAScrollbarCellSaysTheMostNotableThingInIt(t *testing.T) {
	const height, total = 10, 40
	base := trackTable{width: 60, height: height + headerRows, showRating: true}

	liked := trough(total)
	liked[0].Rating = RatingUp
	liked[1].Rating = RatingUp
	both := trough(total)
	both[0].Rating = RatingUp
	both[1].Rating = RatingDown
	playing := trough(total)
	playing[0].Rating = RatingUp
	playing[1].Rating = RatingDown

	for _, tc := range []struct {
		name    string
		table   trackTable
		want    string
		notWant []string
	}{
		{"likes alone", func() trackTable { at := base; at.tracks = liked; return at }(),
			likedFG, []string{dislikedFG, liveFG}},
		{"a dislike among them", func() trackTable { at := base; at.tracks = both; return at }(),
			dislikedFG, []string{likedFG, liveFG}},
		{"and the playing track over that", func() trackTable {
			at := base
			at.tracks, at.playing = playing, "v2"
			return at
		}(), liveFG, []string{likedFG, dislikedFG}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cell := tc.table.scrollbar()[0]
			if !sgrCodes(cell)[tc.want] {
				t.Errorf("cell 0 is not SGR %s: %q", tc.want, cell)
			}
			for _, code := range tc.notWant {
				if sgrCodes(cell)[code] {
					t.Errorf("cell 0 also carries SGR %s: %q", code, cell)
				}
			}
		})
	}
}

// The liked playlist marks no ratings, for the same reason its rows carry no
// colour: a trough of magenta says nothing a plain one does not. What is
// playing is still marked.
func TestTheScrollbarDropsRatingMarksWithTheRatings(t *testing.T) {
	const height, total = 10, 40
	tracks := trough(total)
	tracks[0].Rating, tracks[20].Rating = RatingUp, RatingDown
	table := trackTable{tracks: tracks, width: 60, height: height + headerRows,
		showRating: false, playing: "v36"}

	bar := strings.Join(table.scrollbar(), "")
	if sgrCodes(bar)[likedFG] || sgrCodes(bar)[dislikedFG] {
		t.Errorf("a block that shows no ratings marked one: %q", bar)
	}
	if !sgrCodes(bar)[liveFG] {
		t.Errorf("the playing track lost its mark with them: %q", bar)
	}
}

// Behind a popover the whole bar goes quiet, marks included: they keep their
// place and stop being the lit thing.
func TestAnInactiveScrollbarHasNoMarks(t *testing.T) {
	const height, total = 10, 40
	tracks := trough(total)
	tracks[0].Rating, tracks[20].Rating = RatingUp, RatingDown
	table := trackTable{tracks: tracks, width: 60, height: height + headerRows,
		showRating: true, playing: "v36", inactive: true,
		quiet: color.RGBA{0xBF, 0xBF, 0xBF, 0xFF}}

	bar := table.scrollbar()
	joined := strings.Join(bar, "")
	for _, code := range []string{likedFG, dislikedFG, liveFG} {
		if sgrCodes(joined)[code] {
			t.Errorf("an inactive scrollbar still carries SGR %s: %q", code, joined)
		}
	}
	// The shapes are where they were: three blocks for the marks and the
	// thumb, whatever colour they are in.
	if n := strings.Count(plain(joined), "█"); n < 3 {
		t.Errorf("the marks lost their shape as well: %q", plain(joined))
	}
	if !strings.Contains(joined, "38;2;191;191;191m") {
		t.Errorf("the bar is not in the quiet colour: %q", joined)
	}
}
