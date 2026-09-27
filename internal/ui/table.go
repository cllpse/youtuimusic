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
	// showRating is false where every row would be drawn the same, as in the
	// liked playlist: a page of magenta says nothing a page of plain rows does
	// not.
	showRating bool
	// titleOnly drops every column but the first. An album is one artist's
	// record, so naming them down the page says the same thing each time.
	titleOnly bool
	// playing is the video id to colour, and is empty when nothing is.
	playing string
	// highlight is the selected row's fill, derived from the terminal's own
	// background so that it follows the theme.
	highlight color.Color
	// inactive draws the whole block in one quiet colour: something is in
	// front of it and none of what it usually says — this row is selected,
	// this one is playing — is being said to anyone.
	inactive bool
	// quiet is the colour it says nothing in.
	quiet color.Color
	sort  sortSpec
	now   time.Time

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
	// scrollbarWidth is the bar itself with a blank column either side, so it
	// sits against neither the last column nor the edge. It needed one on
	// the left as soon as the last column was set against the right: a
	// duration ending where the bar begins reads as one thing.
	scrollbarWidth = 3

	// The columns are shares of what is left once the two separators are
	// taken: most of it to the title, a third of what remains to the artist,
	// the rest to the length.
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
		return layout{title: max(width, 0)}
	}
	spare := max(width-2, 0)
	title := spare * titleShare / 100
	length := min(max(spare*lengthShare/100, minLength), max(spare-title, 0))
	return layout{title: title, artist: spare - title - length, length: length}
}

// rows renders the block one line at a time, so a caller can put something
// above it without splitting a string apart again.
func (t trackTable) rows() []string {
	width := t.width
	bar := t.scrollbar()
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
			style, styled := t.rowStyle(track, index == t.cursor)
			line = t.trackLine(track, cols, styled)
			if styled {
				line = style.Render(line)
			}
			if t.inactive {
				// One colour over the whole line, which is why trackLine
				// leaves its own parts unstyled when the block is inactive:
				// a nested style ends in a reset, and the reset would drop
				// the colour for the rest of the line.
				line = lipgloss.NewStyle().Foreground(t.quiet).Render(line)
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
	if t.cursor == len(t.tracks) && !t.inactive {
		return rowSelected(t.highlight).Render(centred)
	}
	return dim.Render(centred)
}

// rowSelected fills a row with the highlight. No foreground is set with it:
// the highlight is a tint of the terminal's own background, so the
// terminal's own text colour still reads on it whatever the theme is.
func rowSelected(highlight color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Background(highlight)
}

// rowStyle picks how a row is drawn, and reports whether it is styled at all.
// An unstyled row mutes its own columns; a styled one must not, since dimmed
// text on a filled background is nothing.
//
// A row carries three independent things: what you think of the track, whether
// it is the one playing, and whether it is the one under the cursor. There are
// three ways to say something here and one goes to each — the hue is the
// rating, the weight is the player, the fill is the cursor — so a row can say
// all three at once, which it has to: the cursor is usually on the track
// playing, and that track is as likely to be rated as any other.
//
// Where there is no rating the hue says the player instead, in its own blue.
// A rating displaces that because the player has three other places to say
// where it is — the state block, the bar, the mark in the scrollbar — and a
// rating has only this one.
func (t trackTable) rowStyle(track Track, selected bool) (lipgloss.Style, bool) {
	if t.inactive {
		// Nothing is rated, selected or playing as far as this block is
		// concerned: something is in front of it.
		return lipgloss.Style{}, false
	}
	style, styled := lipgloss.NewStyle(), false
	playing := t.playing != "" && track.VideoID == t.playing

	switch hue, rated := t.ratingHue(track); {
	case rated:
		style, styled = style.Foreground(hue), true
	case playing:
		style, styled = style.Foreground(live), true
	}
	if playing {
		style, styled = style.Bold(true), true
	}
	if selected {
		style, styled = style.Background(t.highlight), true
	}
	return style, styled
}

// ratingHue is the colour a row is drawn in for what you think of it, where
// the block shows that at all.
func (t trackTable) ratingHue(track Track) (color.Color, bool) {
	if !t.showRating {
		return nil, false
	}
	return track.Rating.hue()
}

// trackLine draws one row. Every title starts at the edge: nothing goes in
// front of one, now that how a track is rated is the colour of the row rather
// than a mark on it.
//
// Everything but the title is muted, which leaves the eye one thing to read
// down. A styled row is drawn plain and coloured whole by the caller — dimming
// part of it would fight the colour.
func (t trackTable) trackLine(track Track, cols layout, highlighted bool) string {
	title := track.Title
	// A release is the artist's own work rather than a song of theirs, and
	// weight is what says so now that it has no icon. Not while the block is
	// inactive: weight is what inactive takes away, and a style here would
	// end in a reset that cancels the faint the caller puts over the row.
	if track.isRelease() && !t.inactive {
		title = releaseStyle.Render(title)
	}

	if cols.title+cols.artist < 4 {
		// No room for columns; the title is the only thing worth keeping.
		return truncate(title, t.width)
	}
	if t.titleOnly {
		return pad(truncate(title, cols.title), cols.title)
	}

	artist := pad(truncate(track.Artist, cols.artist), cols.artist)
	// The last column is read against the right edge, so it is set there.
	length := strings.Repeat(" ", cols.length)
	if !track.isRelease() {
		length = padLeft(truncate(formatDuration(track.Duration), cols.length), cols.length)
	}
	if !highlighted && !t.inactive {
		artist, length = dim.Render(artist), dim.Render(length)
	}
	return pad(truncate(title, cols.title), cols.title) + " " + artist + " " + length
}

// releaseStyle is how an album reads in an artist's listing: its own work,
// not one of its songs.
var releaseStyle = lipgloss.NewStyle().Bold(true)

// The trough is drawn out of blocks: a whole one for the window, halves for the
// marks in it, and a line where there is neither. All three are block elements,
// which is the one part of Unicode a terminal font can be relied on for — see
// the note on emptyCell, which went through the same search.
const (
	blockFull  = "█"
	blockUpper = "▀"
	blockLower = "▄"
	troughLine = "│"
)

// markHalves is how many marks one cell of the trough can hold.
//
// Two, because a cell is one character and a character can be half one colour
// and half another: an upper half block drawn in one colour over a background
// of the other. The progress bar already gets sub-cell resolution this way.
//
// It is worth the trouble because a cell is not one row. Seventy tracks in a
// twenty row window puts three or four of them in every cell, so two tracks
// liked one after the other landed in the same cell and the second had nowhere
// to go — which read, correctly, as only one of them being marked.
const markHalves = 2

// markAt is which half of the trough a row falls on, the trough standing for
// the whole list. The thumb is placed by the same proportion, in whole cells.
func markAt(row, total, halves int) int {
	return min(row*halves/total, halves-1)
}

// scrollbarMarks is what each half of each cell has to say about the rows that
// fall in it: half 2i is the top of cell i and 2i+1 the bottom.
//
// Two rows in the same half still have to share one colour, so the more notable
// of them takes it. A dislike outranks a like because there are fewer of them
// and they are the ones worth finding. The playing track outranks both — not
// because it matters more, but because its mark is the one that moves, and a
// mark you are following cannot go missing every time the track it stands for
// is liked.
func (t trackTable) scrollbarMarks() map[int]color.Color {
	total, halves := t.rowCount(), t.rowsHeight()*markHalves
	marks := map[int]color.Color{}
	if total <= 0 || halves <= 0 {
		return marks
	}

	for i, track := range t.tracks {
		hue, rated := t.ratingHue(track)
		if !rated {
			continue
		}
		at := markAt(i, total, halves)
		if marks[at] == disliked {
			continue
		}
		marks[at] = hue
	}
	if at := t.playingRow(); at >= 0 && at < total {
		marks[markAt(at, total, halves)] = live
	}
	return marks
}

// scrollbar draws a trough and thumb for the list, with what is playing and
// what you think of it marked in the colours those things take everywhere else.
//
// The bar itself is drawn in the row highlight, the same surface the selected
// row, the status bar and the progress groove use. Shape says where the window
// is — a block there, a line where it is not — and colour says what is in the
// list, so a mark and the thumb can share a cell, which they do the whole time
// the playing track is on screen, without either hiding the other.
func (t trackTable) scrollbar() []string {
	total, height := t.rowCount(), t.rowsHeight()
	if !needsScrollbar(total, height) {
		return nil
	}
	thumb := max(1, height*height/total)
	start := 0
	if span, furthest := height-thumb, total-height; furthest > 0 {
		start = min(t.offset*span/furthest, span)
	}

	furniture := lipgloss.NewStyle().Foreground(highlightOr(t.highlight, t.inactive, t.quiet))
	marks := t.scrollbarMarks()

	out := make([]string, height)
	for i := range out {
		out[i] = " " + t.troughCell(
			marks[i*markHalves], marks[i*markHalves+1],
			i >= start && i < start+thumb, furniture) + " "
	}
	return out
}

// troughCell draws one cell of the trough: what falls in its two halves, and
// whether the window covers it.
func (t trackTable) troughCell(top, bottom color.Color, inThumb bool, furniture lipgloss.Style) string {
	if t.inactive {
		// Behind a popover nothing is lit. The marks keep their place — the
		// cell is still a block — and stop drawing the eye.
		if top != nil || bottom != nil || inThumb {
			return furniture.Render(blockFull)
		}
		return furniture.Render(troughLine)
	}
	switch {
	case top != nil && bottom != nil:
		// Two marks in one cell, one above the other: the upper is drawn and
		// the lower is what it is drawn on.
		return lipgloss.NewStyle().Foreground(top).Background(bottom).Render(blockUpper)
	case top != nil:
		return halfMark(blockUpper, top, inThumb, t.highlight)
	case bottom != nil:
		return halfMark(blockLower, bottom, inThumb, t.highlight)
	case inThumb:
		return furniture.Render(blockFull)
	}
	return furniture.Render(troughLine)
}

// halfMark is a mark in one half of a cell. Inside the window the other half
// is the thumb, drawn as this one's background so that neither hides the other.
func halfMark(glyph string, hue color.Color, inThumb bool, highlight color.Color) string {
	style := lipgloss.NewStyle().Foreground(hue)
	if inThumb {
		style = style.Background(highlight)
	}
	return style.Render(glyph)
}

// highlightOr is the bar's own colour, which sinks with the block it belongs
// to.
func highlightOr(highlight color.Color, inactive bool, quiet color.Color) color.Color {
	if inactive {
		return quiet
	}
	return highlight
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
