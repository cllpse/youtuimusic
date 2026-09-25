package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Material Design icons from Nerd Fonts, by the names glyphnames.json gives
// them. The codepoints were checked against that file rather than typed from
// memory, and against the installed fonts with fontconfig.
const (
	iconPrevious     = "\U000f04ae" // md-skip_previous
	iconPlay         = "\U000f040a" // md-play
	iconPause        = "\U000f03e4" // md-pause
	iconNext         = "\U000f04ad" // md-skip_next
	iconThumbUp      = "\U000f0513" // md-thumb_up
	iconThumbUpOff   = "\U000f0514" // md-thumb_up_outline
	iconThumbDown    = "\U000f0511" // md-thumb_down
	iconThumbDownOff = "\U000f0512" // md-thumb_down_outline
	iconRepeatOff    = "\U000f0457" // md-repeat_off
	iconRepeatAll    = "\U000f0456" // md-repeat
	iconRepeatOne    = "\U000f0458" // md-repeat_once
)

// Repeat is what happens when a track ends.
type Repeat int

const (
	RepeatOff Repeat = iota
	RepeatAll
	RepeatOne
)

func (r Repeat) next() Repeat { return (r + 1) % 3 }

func (r Repeat) icon() string {
	switch r {
	case RepeatAll:
		return iconRepeatAll
	case RepeatOne:
		return iconRepeatOne
	default:
		return iconRepeatOff
	}
}

// control identifies a button on the controls row.
type control int

const (
	controlNone control = iota
	controlPrevious
	controlPlayPause
	controlNext
	controlThumbUp
	controlThumbDown
	controlRepeat
)

// button is one control as laid out on the row.
type button struct {
	control control
	icon    string
	// lit means filled rather than dimmed: there is something for
	// it to act on, or the thing it toggles is on.
	lit        bool
	start, end int // half-open columns
}

// A button is three cells — the icon with one either side — all of which
// answer to a click. The gap is two so a row of them does not run together.
const (
	buttonWidth = 3
	buttonGap   = 2
)

var (
	// Idle, a button is only its icon, dimmed: there is nothing to press.
	buttonStyle = lipgloss.NewStyle().Foreground(muted)
	// Live, it is turned inside out — the foreground as a fill, the
	// background as its text — which is what makes it look pressable rather
	// than printed, without reaching for a second hue.
	buttonLitStyle = lipgloss.NewStyle().
			Background(emphasis).
			Foreground(background).
			Bold(true)
)

// renderButton draws one button: its icon, filled when it is live.
func renderButton(icon string, lit bool) string {
	style := buttonStyle
	if lit {
		style = buttonLitStyle
	}
	return style.Render(" " + icon + " ")
}

// groupWidth is what a run of buttons occupies, gaps between them included.
func groupWidth(n int) int {
	if n == 0 {
		return 0
	}
	return n*buttonWidth + (n-1)*buttonGap
}

// controlButtons lays the row out: transport against the left edge, the
// thumbs centred on the row, repeat against the right. Rendering and
// hit-testing share it, so a click lands on the button it looks like it
// should.
func (m Model) controlButtons() []button {
	width := m.contentWidth()
	if width < groupWidth(3) {
		return nil
	}
	// Columns are counted across the whole row, so a span can be compared
	// against a click without anyone remembering the border is there.
	right := contentLeft + width
	playing := m.playing.VideoID != ""

	leftGroup := []button{
		{control: controlPrevious, icon: iconPrevious, lit: playing},
		{control: controlPlayPause, icon: m.playPauseIcon(), lit: playing},
		{control: controlNext, icon: iconNext, lit: playing},
	}
	up, down := iconThumbUpOff, iconThumbDownOff
	if m.playing.Rating == RatingUp {
		up = iconThumbUp
	}
	if m.playing.Rating == RatingDown {
		down = iconThumbDown
	}
	centre := []button{
		{control: controlThumbUp, icon: up, lit: playing && m.playing.Rating == RatingUp},
		{control: controlThumbDown, icon: down, lit: playing && m.playing.Rating == RatingDown},
	}
	repeatGroup := []button{
		{control: controlRepeat, icon: m.repeat.icon(), lit: m.repeat != RepeatOff},
	}

	at := lay(leftGroup, contentLeft)

	// Centred on the row, but never on top of the transport.
	centreStart := max(contentLeft+(width-groupWidth(len(centre)))/2, at+buttonGap)
	if centreStart+groupWidth(len(centre)) > right {
		centre = nil
	} else {
		at = lay(centre, centreStart)
	}

	repeatStart := right - groupWidth(len(repeatGroup))
	if repeatStart < at+buttonGap {
		repeatGroup = nil
	} else {
		lay(repeatGroup, repeatStart)
	}

	return append(append(leftGroup, centre...), repeatGroup...)
}

// lay assigns columns to a group and returns where the last button ends.
func lay(group []button, at int) int {
	end := at
	for i := range group {
		group[i].start, group[i].end = at, at+buttonWidth
		end = group[i].end
		at += buttonWidth + buttonGap
	}
	return end
}

func (m Model) playPauseIcon() string {
	// The icon is what pressing it does, which is the convention every other
	// player follows: a pause bar while it plays.
	if m.playing.VideoID != "" && !m.Paused {
		return iconPause
	}
	return iconPlay
}

func (m Model) renderControls() string {
	width := m.contentWidth()
	buttons := m.controlButtons()
	if len(buttons) == 0 {
		return strings.Repeat(" ", width)
	}
	var b strings.Builder
	at := contentLeft
	for _, btn := range buttons {
		if btn.start > at {
			b.WriteString(strings.Repeat(" ", btn.start-at))
		}
		b.WriteString(renderButton(btn.icon, btn.lit))
		at = btn.end
	}
	if end := contentLeft + width; at < end {
		b.WriteString(strings.Repeat(" ", end-at))
	}
	return b.String()
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
	case controlThumbUp:
		return m.ratePlaying(RatingUp)
	case controlThumbDown:
		return m.ratePlaying(RatingDown)
	case controlRepeat:
		m.repeat = m.repeat.next()
		return m, nil
	}
	return m, nil
}

// ratePlaying rates the track that is playing. The thumbs sit beside the
// transport, so they act on what is playing rather than on what happens to
// be highlighted — which is what the + and - keys are for.
func (m Model) ratePlaying(r Rating) (tea.Model, tea.Cmd) {
	if m.playing.VideoID == "" {
		return m, nil
	}
	previous := m.playing.Rating
	if previous == r {
		r = RatingNone
	}
	m.setRating(m.playing.VideoID, r)
	return m, m.rate(m.playing.VideoID, r, previous)
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
func (m Model) start(t Track) (tea.Model, tea.Cmd) {
	m.playing, m.Position, m.Length = t, 0, t.Duration
	return m, m.play(t)
}

// indexOfPlaying finds the playing track in the list on screen, which it is
// not in once another tab is opened.
func (m Model) indexOfPlaying() (int, bool) {
	if m.playing.VideoID == "" {
		return 0, false
	}
	for i, t := range m.Tracks {
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
	switch m.repeat {
	case RepeatOne:
		return m.Tracks[i], true
	case RepeatAll:
		return m.Tracks[(i+1)%len(m.Tracks)], true
	default:
		if i+1 < len(m.Tracks) {
			return m.Tracks[i+1], true
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
	if i > 0 {
		return m.Tracks[i-1], true
	}
	if m.repeat == RepeatAll {
		return m.Tracks[len(m.Tracks)-1], true
	}
	return Track{}, false
}
