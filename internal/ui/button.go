package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// A button is its label with a space either side: one row, nothing wrapped
// round it. It went through a bordered box three rows tall and a pair of
// rounded caps on the way here, and both were bigger than what they were
// wrapping. A word is a button.
//
// A cell is the narrowest space a terminal has. The thin spaces — U+2009 and
// its neighbours — are a cell wide here too, and are in neither of the fonts
// this is read in, so they would come from whatever fallback the terminal
// picked and at whatever width it liked.
//
// The gap is two, which is the only separation there is, and every cell of a
// button answers to a click, padding included.
const (
	buttonPadding = 1
	buttonGap     = 2
	// controlsRows is how tall a row of them is.
	controlsRows = 1
)

// buttonState is what a button has to say about itself.
//
// Default and active look the same for now, deliberately: one place decides
// how a button reads, and until that place says otherwise the only thing
// worth drawing differently is one that cannot be pressed.
type buttonState int

const (
	// buttonDefault is a button with something to do and nothing on.
	buttonDefault buttonState = iota
	// buttonActive is one whose thing is already on — repeat, or a rating
	// the playing track already carries.
	buttonActive
	// buttonDisabled is one with nothing to act on: the transport with
	// nothing playing, a rating with nothing to rate.
	buttonDisabled
)

// disabledButton is how a button that cannot be pressed reads. Faint rather
// than a grey: colour 8 is the only grey the scheme has for this and against
// a light page it is about 1.88:1, which is not a label. Faint asks the
// terminal to take its own foreground down, which lands on any theme.
var disabledButton = lipgloss.NewStyle().Faint(true)

// buttonWidth is what one button occupies: its label and the space either
// side of it. What is drawn has to be this wide or a click lands on the
// wrong one.
func buttonWidth(label string) int {
	return lipgloss.Width(label) + 2*buttonPadding
}

// padded is a label with its air.
func padded(label string) string {
	pad := strings.Repeat(" ", buttonPadding)
	return pad + label + pad
}

// renderButton draws one button in a state.
func renderButton(label string, state buttonState) string {
	if state == buttonDisabled {
		return disabledButton.Render(padded(label))
	}
	return padded(label)
}
