package ui

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// trackTable draws a list of tracks: a header naming the columns, one row
// each, the cursor highlighted, the playing track coloured, and a scrollbar
// down the side when there is more than fits.
//
// The main view and the popover are the same table at different sizes.
// Having one of these is what keeps them from drifting apart — a column
// added here shows up in both, and so does a bug.
type trackTable struct {
	tracks []Track
	cursor int
	offset int
	width  int
	// height is the whole block, the header included.
	height int
	// showRating is false where every row would carry the same mark, as in
	// the liked playlist.
	showRating bool
	// titleOnly drops every column but the first. An album is one artist's
	// record, so naming them down the page says the same thing each time.
	titleOnly bool
	// playing is the video id to colour, and is empty when nothing is.
	playing string
	// highlight is the selected row's fill, derived from the terminal's own
	// background so that it follows the theme.
	highlight color.Color
	sort      sortSpec
	now       time.Time

	// more draws one extra row at the end, offering the next page. While
	// that page is on its way it becomes the same loader the rest of the
	// interface waits with.
	more        bool
	loadingMore bool
	loader      string
}

const (
	// headerRows is zero: the columns are not named. A table of songs is
	// legible without being told that the titles are titles, and the row it
	// cost was the row it cost.
	headerRows = 0
	// scrollbarWidth is the bar itself plus a blank column to its right, so
	// it does not sit against whatever is beside it.
	scrollbarWidth = 2

	markWidth = 2
	// The columns are shares of what is left once the mark and the two
	// separators are taken: most of it to the title, a third of what remains
	// to the artist, the rest to the length.
	titleShare  = 60
	lengthShare = 10
	// minLength still fits "MM:SS", because a duration cut short is a
	// different duration rather than a shorter one.
	minLength = 5
)

// rowsHeight is how many tracks the block has room for.
func (t trackTable) rowsHeight() int { return max(t.height-headerRows, 0) }

// rowCount is the tracks plus the row that offers the next page.
func (t trackTable) rowCount() int {
	if t.more {
		return len(t.tracks) + 1
	}
	return len(t.tracks)
}

// needsScrollbar reports whether a list is longer than its window. The
// column only exists when it has something to say, so a list that fits is
// not made narrower for nothing.
func needsScrollbar(total, height int) bool { return height > 0 && total > height }

// playingRow is where the playing track is in this listing, or -1 when it
// is not in this one at all — which is the ordinary case for every tab but
// the one it was started from.
func (t trackTable) playingRow() int {
	if t.playing == "" {
		return -1
	}
	for i, track := range t.tracks {
		if track.VideoID == t.playing {
			return i
		}
	}
	return -1
}

func (t trackTable) hasScrollbar() bool {
	return needsScrollbar(t.rowCount(), t.rowsHeight())
}

// layout is the width of each column, given what the table has to work with.
type layout struct {
	title, artist, length int
}

func (t trackTable) layout(width int) layout {
	if t.titleOnly {
		// One column takes all of it. There is nothing to share with.
		return layout{title: max(width-markWidth, 0)}
	}
	spare := max(width-markWidth-2, 0)
	title := spare * titleShare / 100
	length := min(max(spare*lengthShare/100, minLength), max(spare-title, 0))
	return layout{title: title, artist: spare - title - length, length: length}
}

// rows renders the block one line at a time, so a caller can put something
// above it without splitting a string apart again.
func (t trackTable) rows() []string {
	width := t.width
	bar := scrollbarFor(t.rowCount(), t.offset, t.rowsHeight(), t.playingRow(), t.highlight)
	if bar != nil {
		width -= scrollbarWidth
	}
	cols := t.layout(width)

	out := make([]string, 0, t.height)
	for i := range t.rowsHeight() {
		index := i + t.offset
		var line string
		switch {
		case t.more && index == len(t.tracks):
			line = t.moreRow(width)
		case index >= 0 && index < len(t.tracks):
			track := t.tracks[index]
			playing := t.playing != "" && track.VideoID == t.playing
			style, styled := rowStyle(playing, index == t.cursor, t.highlight)
			line = t.trackLine(track, cols, styled)
			if styled {
				line = style.Render(line)
			}
		}
		line = pad(line, width)
		if bar != nil {
			line += bar[i]
		}
		out = append(out, line)
	}
	return out
}

func (t trackTable) render() string { return strings.Join(t.rows(), "\n") }

// labelLoadMore ends in an ellipsis, which is what says the row is a door
// rather than a statement.
const labelLoadMore = "Load more…"

// moreRow is the last line of a listing that has more to fetch.
func (t trackTable) moreRow(width int) string {
	if t.loadingMore {
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, t.loader)
	}
	centred := lipgloss.PlaceHorizontal(width, lipgloss.Center, labelLoadMore)
	if t.cursor == len(t.tracks) {
		return rowSelected(t.highlight).Render(centred)
	}
	return dim.Render(centred)
}

// A row carries two independent things: whether it is the track playing, and
// whether it is the one under the cursor. Colour says the first and a filled
// background says the second, so a row can say both at once — which it has
// to, since the cursor is usually on the track that is playing.
var rowPlaying = lipgloss.NewStyle().Bold(true).Foreground(emphasis)

// rowSelected fills a row with the highlight. No foreground is set with it:
// the highlight is a tint of the terminal's own background, so the
// terminal's own text colour still reads on it whatever the theme is.
func rowSelected(highlight color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Background(highlight)
}

// rowStyle picks how a row is drawn, and reports whether it is styled at
// all. An unstyled row mutes its own columns; a styled one must not, since
// dimmed text on a filled background is nothing.
func rowStyle(playing, selected bool, highlight color.Color) (lipgloss.Style, bool) {
	switch {
	case playing && selected:
		return rowPlaying.Background(highlight), true
	case playing:
		return rowPlaying, true
	case selected:
		return rowSelected(highlight), true
	}
	return lipgloss.Style{}, false
}

// trackLine draws one row. The leading column is the same two cells whether
// or not it holds a mark, so the titles line up across tabs.
//
// Everything but the title is muted, which leaves the eye one thing to read
// down. A highlighted row is drawn plain and coloured whole by the caller —
// dimming part of it would fight the highlight.
func (t trackTable) trackLine(track Track, cols layout, highlighted bool) string {
	prefix := strings.Repeat(" ", markWidth)
	if t.showRating || track.isRelease() {
		prefix = track.glyph() + " "
	}
	if cols.title+cols.artist < 4 {
		// No room for columns; the title is the only thing worth keeping.
		return truncate(prefix+track.Title, t.width)
	}
	if t.titleOnly {
		return prefix + pad(truncate(track.Title, cols.title), cols.title)
	}

	artist := pad(truncate(track.Artist, cols.artist), cols.artist)
	length := strings.Repeat(" ", cols.length)
	if !track.isRelease() {
		length = pad(truncate(formatDuration(track.Duration), cols.length), cols.length)
	}
	if !highlighted {
		artist, length = dim.Render(artist), dim.Render(length)
	}
	return prefix + pad(truncate(track.Title, cols.title), cols.title) +
		" " + artist + " " + length
}

// scrollbarFor draws a trough and thumb for a list. The thumb is the same
// grey as the trough: the glyphs carry the difference, as they do on the
// progress bar.
// scrollbarFor draws a trough and thumb for a list, with the playing track
// marked in it. playingAt is that track's index, or -1 for none.
//
// The bar is drawn in the row highlight, the same surface the selected row,
// the status bar and the progress groove use. Shape carries the meaning here
// — a block where the window is, a line where it is not — so the colour is
// free to say only "furniture".
func scrollbarFor(total, offset, height, playingAt int, highlight color.Color) []string {
	if !needsScrollbar(total, height) {
		return nil
	}
	thumb := max(1, height*height/total)
	start := 0
	if span, furthest := height-thumb, total-height; furthest > 0 {
		start = min(offset*span/furthest, span)
	}

	// The trough stands for the whole list, so the mark is placed by the
	// same proportion the thumb is.
	mark := -1
	if playingAt >= 0 && playingAt < total {
		mark = min(playingAt*height/total, height-1)
	}

	// The mark is the trough's own block, highlighted. Shape says where the
	// window is and colour says where the playing track is, so the two can
	// share a cell — which they do the whole time the playing track is on
	// screen — without either hiding the other.
	furniture := lipgloss.NewStyle().Foreground(highlight)
	out := make([]string, height)
	for i := range out {
		switch {
		case i == mark:
			out[i] = active.Render("█") + " "
		case i >= start && i < start+thumb:
			out[i] = furniture.Render("█") + " "
		default:
			out[i] = furniture.Render("│") + " "
		}
	}
	return out
}

// keepVisible moves a window the least it can to keep an index on screen,
// and never past the end of the list.
func keepVisible(cursor, offset, height, total int) int {
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+height {
		offset = cursor - height + 1
	}
	return clampOffset(offset, height, total)
}

// clampOffset holds a window inside a list.
func clampOffset(offset, height, total int) int {
	return max(min(offset, total-height), 0)
}

// sortColumn names what a table is ordered by.
type sortColumn int

const (
	sortNone sortColumn = iota
	sortTitle
	sortArtist
	sortLength
)

// sortSpec is an order: a column and a direction.
type sortSpec struct {
	by   sortColumn
	desc bool
}

func (s sortSpec) arrow() string {
	if s.desc {
		return "↓"
	}
	return "↑"
}

// next moves to the following column, wrapping back through unsorted so
// that the list can always be put back the way it arrived.
func (s sortSpec) next() sortSpec {
	if s.by >= sortLength {
		return sortSpec{}
	}
	return sortSpec{by: s.by + 1}
}

// on is what clicking a column header means: order by it, or reverse it if
// it is already the one in use.
func (s sortSpec) on(by sortColumn) sortSpec {
	if s.by == by {
		return sortSpec{by: by, desc: !s.desc}
	}
	return sortSpec{by: by}
}

// sortTracks orders a list in place. It is stable, so the order a listing
// arrived in survives wherever the key is equal.
func sortTracks(tracks []Track, spec sortSpec) {
	if spec.by == sortNone {
		return
	}
	slices.SortStableFunc(tracks, func(a, b Track) int {
		n := compareBy(a, b, spec.by)
		if spec.desc {
			return -n
		}
		return n
	})
}

func compareBy(a, b Track, by sortColumn) int {
	switch by {
	case sortTitle:
		return compareText(a.Title, b.Title)
	case sortArtist:
		return compareText(a.Artist, b.Artist)
	case sortLength:
		return int(a.Duration - b.Duration)
	}
	return 0
}

// compareText orders the way a person reading a list would, which is not
// how bytes order: "abba" belongs beside "ABBA", not after "Zappa".
func compareText(a, b string) int {
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return " 0:00"
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%2d:%02d", total/60, total%60)
}
