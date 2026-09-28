package ui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cllpse/youtuimusic/internal/ytm"
)

// The app draws no icons at all. The last pair was a thumbs-up and a
// thumbs-down on a rated row — md-thumb_up at U+F0513 and md-thumb_down at
// U+F0511, from Nerd Fonts — and a rating is a colour now, which asks nothing
// of the reader's font.

// The transport is labelled rather than pictured. An icon has to be learned;
// a word does not, and there is room for words here.
// Each carries the key that works it, so the row doubles as the help for
// itself. Space is spelled out; a single blank in brackets would read as a
// typo.
// Prev is the dictionary's abbreviation of previous; the rest are already
// the shortest words for what they do.
//
// Rating is not down here any more. A thumb pair sat on the end of this row
// and rated whatever was playing, which made it the one control that acted on
// something other than what you were pointing at — and it could only ever
// rate that one track. Rating belongs to a row, so it lives on the row's menu
// now, beside the other things you can do to a track.
const (
	labelPrevious  = "Prev (p)"
	labelPlay      = "Play (space)"
	labelPause     = "Pause (space)"
	labelNext      = "Next (n)"
	labelRepeatOff = "Repeat off (r)"
	labelRepeatOn  = "Repeat on (r)"
	labelRepeatOne = "Repeat one (r)"
	labelMix       = "Start mix (M)"
	// labelMixTab names the tab it opens. Short, because the tab row is the
	// one place in the app that is always short of room.
	labelMixTab = "Mix"
)

// A button whose label changes with the state is drawn at the width of its
// longest label, so that it does not change size underneath the pointer.
var (
	repeatLabels  = []string{labelRepeatOff, labelRepeatOn, labelRepeatOne}
	playingLabels = []string{labelPlay, labelPause}
)

// playPauseLabel says what pressing it will do, which is the convention every
// other player follows: Pause while it is playing.
func (m Model) playPauseLabel() string {
	if m.playing.VideoID != "" && !m.Paused {
		return steady(labelPause, playingLabels)
	}
	return steady(labelPlay, playingLabels)
}

// steady renders one of a set of labels at the width of the widest.
func steady(label string, set []string) string {
	widest := 0
	for _, l := range set {
		widest = max(widest, lipgloss.Width(l))
	}
	return lipgloss.PlaceHorizontal(widest, lipgloss.Center, label)
}

var widestRepeatLabel = lipgloss.Width(steady(labelRepeatOff, repeatLabels))

// Repeat is what happens when a track ends.
type Repeat int

const (
	RepeatOff Repeat = iota
	RepeatAll
	RepeatOne
)

func (r Repeat) next() Repeat { return (r + 1) % 3 }

// label is what the repeat button says, centred in the width of the longest
// of the three.
func (r Repeat) label() string {
	label := labelRepeatOff
	switch r {
	case RepeatAll:
		label = labelRepeatOn
	case RepeatOne:
		label = labelRepeatOne
	}
	return steady(label, repeatLabels)
}

// control identifies a button on the controls row.
type control int

const (
	controlNone control = iota
	controlPrevious
	controlPlayPause
	controlNext
	controlRepeat
	controlMix
)

// button is one control as laid out on the row.
type button struct {
	control control
	label   string
	state   buttonState
	// hue is a colour this one has of its own, and nil for the ones that have
	// none.
	hue color.Color
	// start and end are half-open columns.
	start, end int
}

// groupWidth is what a run of buttons occupies, gaps between them included.
func groupWidth(group []button) int {
	if len(group) == 0 {
		return 0
	}
	total := (len(group) - 1) * buttonGap
	for _, b := range group {
		total += buttonWidth(b.label)
	}
	return total
}

// groupGap is the space between one kind of control and another: the transport
// moves about the list, repeat says what happens when a track ends, a mix is a
// page that is not this one. Three errands, three groups. Twice the gap
// between two buttons, so that it reads as a gap of its own rather than as one
// that happens to be wider.
const groupGap = buttonGap * 2

// rowWidth is what a row of groups occupies, both kinds of gap included.
func rowWidth(groups [][]button) int {
	total := 0
	for i, group := range groups {
		if i > 0 {
			total += groupGap
		}
		total += groupWidth(group)
	}
	return total
}

// controlButtons lays the row out: the transport against the left edge, repeat
// a little clear of it. Rendering and hit-testing share it, so a click lands on
// the button it looks like it should.
func (m Model) controlButtons() []button {
	playing := m.playing.VideoID != ""

	// Skipping needs something to skip from, so those two go quiet with
	// nothing playing. Play does not: with nothing playing it starts the row
	// under the cursor, which is something to do. Repeat is never disabled —
	// off is a state it is in, not a thing it cannot do.
	onward := buttonDefault
	if !playing {
		onward = buttonDisabled
	}
	repeat := buttonDefault
	if m.repeat != RepeatOff {
		repeat = buttonActive
	}
	transport := []button{
		{control: controlPrevious, label: labelPrevious, state: onward},
		{control: controlPlayPause, label: m.playPauseLabel(), state: buttonDefault},
		{control: controlNext, label: labelNext, state: onward},
	}
	// A mix is a page rather than a change to this one, which is why it is the
	// far end of the row, a group of its own, and in that page's colour. It
	// needs a track to build from, and is quiet without one.
	mix := buttonDefault
	if _, ok := m.mixSeed(); !ok {
		mix = buttonDisabled
	}
	groups := [][]button{
		transport,
		{{control: controlRepeat, label: m.repeat.label(), state: repeat}},
		{{control: controlMix, label: labelMix, state: mix, hue: m.palette().mix}},
	}

	// A row too narrow for all of it gives up a group at a time from the right:
	// those two say what the player will do next and what else there is, and the
	// transport says what it does now. Narrower than the transport itself, and
	// there is nothing worth drawing — the bar and the status line still say
	// where the track is and what it is.
	for len(groups) > 1 && m.contentWidth() < rowWidth(groups) {
		groups = groups[:len(groups)-1]
	}
	if m.contentWidth() < rowWidth(groups[:1]) {
		return nil
	}

	var out []button
	at := contentLeft
	for i, group := range groups {
		if i > 0 {
			at += groupGap
		}
		at = lay(group, at)
		out = append(out, group...)
	}
	return out
}

// lay assigns columns to a group and returns where the last button ends.
func lay(group []button, at int) int {
	end := at
	for i := range group {
		group[i].start, group[i].end = at, at+buttonWidth(group[i].label)
		end = group[i].end
		at = end + buttonGap
	}
	return end
}

// renderControls draws the row of buttons, which is controlsRows lines tall.
// Each button is assembled line by line rather than joined horizontally,
// because they do not sit shoulder to shoulder: each one starts at the column
// hit-testing says it does.
func (m Model) renderControls() string {
	width := m.contentWidth()
	blank := strings.Repeat(" ", max(width, 0))
	buttons := m.controlButtons()
	if len(buttons) == 0 {
		rows := make([]string, controlsRows)
		for i := range rows {
			rows[i] = blank
		}
		return strings.Join(rows, "\n")
	}

	rows := make([]strings.Builder, controlsRows)
	at := make([]int, controlsRows)
	for i := range at {
		at[i] = contentLeft
	}
	for _, btn := range buttons {
		lines := strings.Split(renderButton(btn.label, btn.state, btn.hue), "\n")
		for r := range rows {
			if r >= len(lines) {
				continue
			}
			if btn.start > at[r] {
				rows[r].WriteString(strings.Repeat(" ", btn.start-at[r]))
			}
			rows[r].WriteString(lines[r])
			at[r] = btn.end
		}
	}

	out := make([]string, controlsRows)
	for r := range rows {
		if end := contentLeft + width; at[r] < end {
			rows[r].WriteString(strings.Repeat(" ", end-at[r]))
		}
		out[r] = rows[r].String()
	}
	return strings.Join(out, "\n")
}

// press acts on a control.
func (m Model) press(c control) (tea.Model, tea.Cmd) {
	switch c {
	case controlPrevious:
		return m.skip(false)
	case controlNext:
		return m.skip(true)
	case controlPlayPause:
		if m.playing.VideoID == "" {
			// Nothing has played yet, so this is a play button.
			return m.skip(true)
		}
		return m, m.togglePause()
	case controlRepeat:
		m.repeat = m.repeat.next()
		return m, nil
	case controlMix:
		return m.startMix()
	}
	return m, nil
}

// mixSeed is the track a mix would be built from: the one playing, or the one
// under the cursor when nothing is. The same rule the play button follows, for
// the same reason — with nothing playing, what you are pointing at is what you
// mean.
func (m Model) mixSeed() (Track, bool) {
	if m.playing.VideoID != "" {
		return m.playing, true
	}
	if t, ok := m.SelectedTrack(); ok && t.VideoID != "" {
		return t, true
	}
	return Track{}, false
}

// startMix opens a mix built from the track the transport would act on.
func (m Model) startMix() (tea.Model, tea.Cmd) {
	seed, ok := m.mixSeed()
	if !ok {
		return m, nil
	}
	return m.mixFrom(seed)
}

// mixFrom opens a mix built from a track: a tab of its own in front of the
// library's, and the seed playing if it is not already.
//
// The old mix goes, cache and all. Two of them would be two tabs with the same
// name and no way to tell which was which, and the one you started last is the
// one you meant.
func (m Model) mixFrom(seed Track) (tea.Model, tea.Cmd) {
	if seed.VideoID == "" {
		return m, nil
	}
	if m.mix.ID != "" {
		delete(m.cache, m.mix.ID)
	}
	m.mix = Playlist{ID: ytm.RadioID(seed.VideoID), Title: labelMixTab, kind: tabMix}
	m.detour, m.history, m.menu = detour{}, nil, trackMenu{}

	m.tabCursor = 0
	next, cmd := m.showTab()
	if next.playing.VideoID == seed.VideoID {
		return next, cmd
	}
	playing, play := next.start(seed)
	return playing, batch(cmd, play)
}

// skip starts the next or previous track. With nothing playing yet there is
// no "next", so it starts the highlighted row instead.
func (m Model) skip(forward bool) (tea.Model, tea.Cmd) {
	if m.playing.VideoID == "" {
		if t, ok := m.SelectedTrack(); ok {
			return m.start(t)
		}
		return m, nil
	}
	pick := m.preceding
	if forward {
		pick = m.following
	}
	if t, ok := pick(); ok {
		return m.start(t)
	}
	return m, nil
}

// open does whatever a row is for: a release has nothing to play, so it
// opens instead.
func (m Model) open(t Track) (tea.Model, tea.Cmd) {
	if t.isRelease() {
		return m.goTo(Playlist{ID: t.AlbumID, Title: t.Title, kind: tabAlbum})
	}
	return m.start(t)
}

// start plays a track, showing it at once because resolving takes a moment.
// The listing it came from is remembered so that advancing does not depend on
// which tab happens to be in front when the track runs out.
func (m Model) start(t Track) (tea.Model, tea.Cmd) {
	m.playing, m.Position, m.Length = t, 0, t.Duration
	m.playingFrom = m.currentTab().ID
	return m, m.play(t)
}

// playingTracks is the listing the playing track belongs to: the visible one
// when that is where it is, otherwise whatever is cached for the tab it was
// started from. Without the fallback, switching tabs stops playback at the end
// of the current track.
func (m Model) playingTracks() []Track {
	if m.playingFrom == m.showingID {
		return m.Tracks
	}
	if entry, ok := m.cache[m.playingFrom]; ok {
		return entry.tracks
	}
	return m.Tracks
}

// indexOfPlaying finds the playing track in the listing it belongs to.
func (m Model) indexOfPlaying() (int, bool) {
	if m.playing.VideoID == "" {
		return 0, false
	}
	rows := m.playingTracks()
	for i, t := range rows {
		if t.VideoID == m.playing.VideoID {
			return i, true
		}
	}
	return 0, false
}

// following is the track that plays when this one ends, which is what the
// repeat setting decides.
func (m Model) following() (Track, bool) {
	i, ok := m.indexOfPlaying()
	if !ok {
		return Track{}, false
	}
	rows := m.playingTracks()
	switch m.repeat {
	case RepeatOne:
		return rows[i], true
	case RepeatAll:
		return rows[(i+1)%len(rows)], true
	default:
		if i+1 < len(rows) {
			return rows[i+1], true
		}
		return Track{}, false
	}
}

// preceding is where the previous button goes. Repeating one track is about
// what happens at the end of it, not about refusing to go back.
func (m Model) preceding() (Track, bool) {
	i, ok := m.indexOfPlaying()
	if !ok {
		return Track{}, false
	}
	rows := m.playingTracks()
	if i > 0 {
		return rows[i-1], true
	}
	if m.repeat == RepeatAll {
		return rows[len(rows)-1], true
	}
	return Track{}, false
}
