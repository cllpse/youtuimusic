package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// --------------------------------------------------------------- view ----

// Every colour in the interface comes from these, so recolouring it is one
// edit rather than a search.
//
// They are named palette entries, not indices into the 256-colour cube:
// 0-15 are the terminal's own scheme, and anything above that is a fixed
// table that ignores it.
//
// The names below say what a colour is for and not what it looks like, and
// that distinction is the whole of getting this right. A sixteen colour
// scheme is not sixteen fixed colours: 0 is the end of the range the
// background sits at and 7 the end the text sits at, so a light theme swaps
// what those two literally are. One in use while this was written sets 0 to
// #FFFFFF and 7 to #272727 — "black" is white and "white" is nearly black.
// Pick a colour for its name and it inverts with the theme; pick it for its
// role and it follows.
//
// Monochrome is where the app opens, and there everything that has to stand
// out does it by weight — faint, ordinary, bright — or by being turned inside
// out, a fill of the foreground with the background as its text, and shape
// does the rest. The hues below are what colour adds to that, and the
// palette further down is which of them each theme spends.
var (
	// alert is the one exception, and it earns it: an error announcing
	// itself by colour is the point of colouring it.
	alert = lipgloss.Red
	// good, busy and live join it on the state block. Green is nothing to
	// report, yellow is waiting on the network, and blue is the player: it
	// also goes everywhere else the track playing is pointed at — the played
	// part of the bar, the row in the list, its mark in the scrollbar. One
	// fact, one colour, wherever it is being said.
	//
	// Yellow earns its own step because a wait is neither of the other two: it
	// is not trouble, and saying it is fine while the screen has not filled in
	// yet is the state that reads as a hang.
	//
	// All four are ANSI colours and not hex, for the same reason everything
	// else here is: they are the terminal's own red, green, yellow and blue,
	// so they come out of whatever scheme is loaded rather than fighting it.
	good = lipgloss.Green
	busy = lipgloss.Yellow
	live = lipgloss.Blue

	// mixHue is a mix the server built, which is a page of somebody else's
	// choosing rather than one of yours — the one other kind of page that is
	// worth telling apart at a glance.
	mixHue = lipgloss.Cyan

	// liked and disliked are what you think of a track, which the list said
	// with a pair of thumbs until a hue could say it without spending a cell
	// of the title on it.
	//
	// Magenta because it was the one hue in the scheme nothing else here had
	// taken. The dislike shares red with trouble, deliberately: it is the one
	// mark on a row you would not want more of, and a scheme of sixteen has
	// only so many ways to say that.
	liked    = lipgloss.Magenta
	disliked = lipgloss.Red

	// background and foreground are the terminal's own two ends, whichever
	// way round the theme has them.
	background = lipgloss.Black
	foreground = lipgloss.White
	// muted is only ever a background. As a foreground it does not clear any
	// contrast worth having: a light theme has to spend colour 8 on being a
	// shade of its own page, and #BDBDBD on #FFFFFF is about 1.8:1, which is
	// not text and is not a border either. Dim text is the terminal's own
	// faint instead, and anything that has to hold a line takes the
	// foreground.
	muted = lipgloss.BrightBlack
	// emphasis is the far end of the foreground. It is what the accent used
	// to be — the strongest thing available — and doubles as the fill under
	// text drawn in the background colour.
	emphasis = lipgloss.BrightWhite

	// surface is a raised background — the status bar's band. It is the dim
	// foreground used the other way round, which puts it one step off the
	// terminal's background in whichever direction that is.
	surface = muted
	// played is the paused bar's filled part: a step below the lit state, so
	// nothing about it reads as playing, but well clear of the groove behind
	// it, so the playhead is still there to see.
	played = foreground
)

var (
	// dim is faint rather than a colour, and that is deliberate. Colour 8 is
	// the only grey a sixteen colour scheme has for dim text, and a light
	// theme has to spend it on being a shade of the background: the one in
	// use while this was written sets it to #BDBDBD, which against a #FFFFFF
	// page is around 1.8:1 and cannot be read. Faint asks the terminal to
	// take its own foreground down instead, which lands right on any theme,
	// and where it is not supported the text comes back at full strength —
	// a flatter hierarchy rather than an invisible one.
	dim    = lipgloss.NewStyle().Faint(true)
	active = lipgloss.NewStyle().Foreground(emphasis).Bold(true)
)

// palette is every accent the interface is allowed, in one value. The colour
// theme spends a hue on each; the monochrome one spends none of them anywhere
// but the status block, whose state hues survive — it is the one thing on
// screen that says how the app is going, and a green READY is a fact, not a
// decoration. Everything else reads in the terminal's own foreground, weight
// and shape doing what the hues were doing: nothing is muted or fainted to
// stand in for a colour it has dropped.
type palette struct {
	liked    color.Color
	disliked color.Color
	mix      color.Color
	live     color.Color
	good     color.Color
	busy     color.Color
	alert    color.Color
}

var (
	colourPalette = palette{
		liked:    liked,
		disliked: disliked,
		mix:      mixHue,
		live:     live,
		good:     good,
		busy:     busy,
		alert:    alert,
	}
	monoPalette = palette{
		// No accent anywhere. A nil means the row is drawn plain rather than
		// drawn quiet: fainting or greying a dislike is how a row that can be
		// chosen comes to look like one that cannot.
		liked:    nil,
		disliked: nil,
		mix:      nil,
		// The status block's hues, and nothing else's.
		live:  live,
		good:  good,
		busy:  busy,
		alert: alert,
	}
)

// palette is the accents in force for this model: the hues, or none of them
// in monochrome.
func (m Model) palette() palette { return paletteFor(m.mono) }

// paletteFor is the same for anything that carries the theme as a flag of its
// own, as the track table does. Which accents monochrome drops is said once,
// in monoPalette, and everything asks it rather than checking the flag.
func paletteFor(mono bool) palette {
	if mono {
		return monoPalette
	}
	return colourPalette
}

// playerColour is the colour of everything that points at the track playing:
// the played part of the bar, the playing row, its mark in the scrollbar and
// the tab that holds it. The player's blue while it plays, and the paused
// colour while it holds — the bar said the difference that way before any of
// the marks existed, so they say it the same way rather than inventing a
// second language for the same fact.
//
// Monochrome has no hue to spend on it, so playing takes the bright end of the
// foreground, which is what the bar used before there were any colours, and
// paused steps down to the foreground itself.
func playerColour(mono, paused bool) color.Color {
	switch {
	case paused && mono:
		return foreground
	case paused:
		return played
	case mono:
		return emphasis
	}
	return live
}
