package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// A button is its label: one row, nothing wrapped round it and no air of its
// own. It went through a bordered box three rows tall, a pair of rounded caps
// and a space either side on the way here, and every one of them was bigger
// than the thing it was wrapping. A word is a button.
//
// Dropping the padding also puts the row of them where everything else on the
// screen starts. A button was the only thing indented by a cell, so the
// transport sat a step right of the list above it and the bar below it.
//
// The gap is two, which is now the only separation there is, and it is a gap
// between buttons rather than part of one: every cell a button draws answers
// to a click, and no cell it does not draw does.
const (
	buttonPadding = 0
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
