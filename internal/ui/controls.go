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
	iconThumbUp      = "\U000f0513" // md-thumb_up
	iconThumbUpOff   = "\U000f0514" // md-thumb_up_outline
	iconThumbDown    = "\U000f0511" // md-thumb_down
	iconThumbDownOff = "\U000f0512" // md-thumb_down_outline
)

// The transport is labelled rather than pictured. An icon has to be learned;
// a word does not, and there is room for words here.
const (
	labelPrevious  = "Previous"
	labelPlayPause = "Play/Pause"
	labelNext      = "Next"
	labelLike      = "Like"
	labelDislike   = "Dislike"
	labelRepeatOff = "Repeat off"
	labelRepeatOn  = "Repeat on"
	labelRepeatOne = "Repeat one"
)

// repeatLabels are what the one repeat button says, and every one of them is
// drawn at the width of the longest so that the button does not change size
// underneath the pointer as it cycles.
var repeatLabels = []string{labelRepeatOff, labelRepeatOn, labelRepeatOne}

var widestRepeatLabel = func() int {
	widest := 0
	for _, l := range repeatLabels {
		widest = max(widest, lipgloss.Width(l))
	}
	return widest
}()

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
	return lipgloss.PlaceHorizontal(widestRepeatLabel, lipgloss.Center, label)
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
	label   string
	// lit means filled rather than dimmed: there is something for
	// it to act on, or the thing it toggles is on.
	lit        bool
	start, end int // half-open columns
}

// A button is a labelled box: its word, a space either side, and a rounded
// border around that. Every cell of it answers to a click, all three rows.
// The gap is two so a row of them does not run together.
const (
	buttonPadding = 1
	buttonBorder  = 1
	buttonGap     = 2
	// controlsRows is how tall the row of them is: a border, the labels,
	// a border.
	controlsRows = 3
)

// buttonWidth is what one label occupies once it is boxed.
func buttonWidth(label string) int {
	return lipgloss.Width(label) + 2*buttonPadding + 2*buttonBorder
}

var (
	// Idle, a button is an outline: there is nothing to press.
	buttonStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(muted).
			Foreground(muted).
			Padding(0, buttonPadding)
	// Live, it is turned inside out — the foreground as a fill, the
	// background as its text — which is what makes it look pressable rather
	// than printed, without reaching for a second hue. Its border takes the
	// fill as well, so the box reads as one solid rounded thing.
	buttonLitStyle = buttonStyle.
			BorderForeground(emphasis).
			BorderBackground(emphasis).
			Background(emphasis).
			Foreground(background).
			Bold(true)
)

// renderButton draws one button, three lines tall.
func renderButton(label string, lit bool) string {
	style := buttonStyle
	if lit {
		style = buttonLitStyle
	}
	return style.Render(label)
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

// controlButtons lays the row out: transport against the left edge, the
// thumbs centred on the row, repeat against the right. Rendering and
// hit-testing share it, so a click lands on the button it looks like it
// should.
func (m Model) controlButtons() []button {
	width := m.contentWidth()
	// Columns are counted across the whole row, so a span can be compared
	// against a click without anyone remembering the border is there.
	right := contentLeft + width
	playing := m.playing.VideoID != ""

	leftGroup := []button{
		{control: controlPrevious, label: labelPrevious, lit: playing},
		{control: controlPlayPause, label: labelPlayPause, lit: playing},
		{control: controlNext, label: labelNext, lit: playing},
	}
	if width < groupWidth(leftGroup) {
		return nil
	}
	centre := []button{
		{control: controlThumbUp, label: labelLike,
			lit: playing && m.playing.Rating == RatingUp},
		{control: controlThumbDown, label: labelDislike,
			lit: playing && m.playing.Rating == RatingDown},
	}
	repeatGroup := []button{
		{control: controlRepeat, label: m.repeat.label(), lit: m.repeat != RepeatOff},
	}

	at := lay(leftGroup, contentLeft)

	// Centred on the row, but never on top of the transport.
	centreStart := max(contentLeft+(width-groupWidth(centre))/2, at+buttonGap)
	if centreStart+groupWidth(centre) > right {
		centre = nil
	} else {
		at = lay(centre, centreStart)
	}

	repeatStart := right - groupWidth(repeatGroup)
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
		lines := strings.Split(renderButton(btn.label, btn.lit), "\n")
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
