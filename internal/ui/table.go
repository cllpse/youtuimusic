package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// trackTable draws a list of tracks: one row each, the cursor highlighted,
// the playing track coloured, and a scrollbar down the side when there is
// more than fits.
//
// The main view and the popover are the same table at different sizes.
// Having one of these is what keeps them from drifting apart — a column
// added here shows up in both, and so does a bug.
type trackTable struct {
	tracks []Track
	cursor int
	offset int
	width  int
	height int
	// showRating is false where every row would carry the same mark, as in
	// the liked playlist.
	showRating bool
	// playing is the video id to colour, and is empty when nothing is.
	playing string
}

// scrollbarWidth is the bar itself plus a blank column to its right, so it
// does not sit against whatever is beside it.
const scrollbarWidth = 2

// needsScrollbar reports whether a list is longer than its window. The
// column only exists when it has something to say, so a list that fits is
// not made narrower for nothing.
func needsScrollbar(total, height int) bool { return height > 0 && total > height }

func (t trackTable) hasScrollbar() bool { return needsScrollbar(len(t.tracks), t.height) }

// rows renders the table one line at a time, so a caller can put something
// above it without splitting a string apart again.
func (t trackTable) rows() []string {
	width := t.width
	bar := scrollbarFor(len(t.tracks), t.offset, t.height)
	if bar != nil {
		width -= scrollbarWidth
	}

	out := make([]string, 0, t.height)
	for i := range t.height {
		index := i + t.offset
		line := ""
		if index >= 0 && index < len(t.tracks) {
			track := t.tracks[index]
			playing := t.playing != "" && track.VideoID == t.playing
			style, styled := rowStyle(playing, index == t.cursor)
			line = trackLine(track, width, t.showRating, styled)
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

// A row carries two independent things: whether it is the track playing, and
// whether it is the one under the cursor. Colour says the first and a filled
// background says the second, so a row can say both at once — which it has
// to, since the cursor is usually on the track that is playing.
var (
	rowPlaying  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	rowSelected = lipgloss.NewStyle().Background(muted)
	rowBoth     = lipgloss.NewStyle().Bold(true).Foreground(accent).Background(muted)
)

// rowStyle picks how a row is drawn, and reports whether it is styled at
// all. An unstyled row mutes its own columns; a styled one must not, since
// grey on a filled background is nothing.
func rowStyle(playing, selected bool) (lipgloss.Style, bool) {
	switch {
	case playing && selected:
		return rowBoth, true
	case playing:
		return rowPlaying, true
	case selected:
		return rowSelected, true
	}
	return lipgloss.Style{}, false
}

// trackLine draws one row. The leading column is the same two cells whether
// or not it holds a rating, so the titles line up across tabs.
//
// Everything but the title is muted, which leaves the eye one thing to read
// down. A highlighted row is drawn plain and coloured whole by the caller —
// dimming part of it would fight the highlight.
func trackLine(t Track, width int, showRating, highlighted bool) string {
	const durCol, rateCol = 6, 2
	prefix := "  "
	if showRating {
		prefix = t.Rating.glyph() + " "
	}
	rest := width - durCol - rateCol - 2
	if rest < 4 {
		// No room for columns; the title is the only thing worth keeping.
		return pad(truncate(prefix+t.Title, width), width)
	}
	titleW := rest / 2
	artistW := rest - titleW

	artist := pad(truncate(t.Artist, artistW), artistW)
	duration := pad(formatDuration(t.Duration), durCol)
	if !highlighted {
		artist, duration = dim.Render(artist), dim.Render(duration)
	}
	return prefix + pad(truncate(t.Title, titleW), titleW) + " " + artist + " " + duration
}

// scrollbarFor draws a trough and thumb for a list. The thumb is the same
// grey as the trough: the glyphs carry the difference, as they do on the
// progress bar.
func scrollbarFor(total, offset, height int) []string {
	if !needsScrollbar(total, height) {
		return nil
	}
	thumb := max(1, height*height/total)
	start := 0
	if span, furthest := height-thumb, total-height; furthest > 0 {
		start = min(offset*span/furthest, span)
	}

	out := make([]string, height)
	for i := range out {
		if i >= start && i < start+thumb {
			out[i] = dim.Render("█") + " "
			continue
		}
		out[i] = dim.Render("│") + " "
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

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return " 0:00"
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%2d:%02d", total/60, total%60)
}
