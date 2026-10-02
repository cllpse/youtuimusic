package ui

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The status block is drawn in colours made from the terminal's own: the
// state's hue lightened for the fill, and the same hue darkened for the word
// on it.
// A tinted fill with dark text of the same hue reads as one colour saying one
// thing, where a saturated fill with black text on it shouts.
//
// The hues are the ANSI entries the block already names — red, green, yellow
// and blue — asked of the terminal with OSC 4, so the result follows whatever
// theme is loaded. Until it answers, and in a terminal that never does, the
// block is drawn as it was: the entry itself, with the background colour on
// it.
//
// How far to lighten and darken is worked out per colour rather than fixed.
// Themes do not agree on what a yellow is: one is a deep mustard and another
// a pastel already, and an amount that suits one leaves the other's word
// unreadable or its fill saturated. So the fill is lightened until it is
// light enough to carry dark text, and the word darkened until it reads on
// it.

// blockColours is the block in one state: its fill and the word on it. A nil
// fill means the terminal has not said what the hue is.
type blockColours struct {
	fill, ink color.Color
}

const (
	// blockLightest is the least the fill is lightened, so every state reads
	// as a tint and not as the raw entry, however light the theme's hue.
	blockLightest = 0.25
	// blockFillLuminance is how light the fill has to be to carry dark text.
	blockFillLuminance = 0.45
	// blockDarkest is the least the word is darkened.
	blockDarkest = 0.4
	// blockContrast is what the word has to reach against its fill: WCAG's
	// 4.5:1 for text, and a little over for a word set in bold at one size.
	blockContrast = 5.0
	// blockStep is how finely the amounts are searched.
	blockStep = 0.05
)

// deriveBlock makes a state's colours from the terminal's own hue.
func deriveBlock(hue color.Color) blockColours {
	lift := blockLightest
	for luminance(towardWhite(hue, lift)) < blockFillLuminance && lift < 1 {
		lift += blockStep
	}
	fill := towardWhite(hue, lift)

	shade := blockDarkest
	for contrast(lipgloss.Darken(hue, shade), fill) < blockContrast && shade < 1 {
		shade += blockStep
	}
	return blockColours{fill: fill, ink: lipgloss.Darken(hue, shade)}
}

// blockColoursFor is the block's colours for a state's hue: the ones made
// from the terminal's own when it has said what that hue is, and the hue
// itself under the background colour when it has not.
func (m Model) blockColoursFor(hue color.Color) blockColours {
	if entry, ok := hue.(ansi.BasicColor); ok && int(entry) < len(m.derived) {
		if derived := m.derived[entry]; derived.fill != nil {
			return derived
		}
	}
	return blockColours{fill: hue, ink: background}
}

// blockEntries are the palette entries the status block is drawn from: red,
// green, yellow and blue, for an error, nothing to report, a wait and the
// player.
var blockEntries = []ansi.BasicColor{alert, good, busy, live}

// askColours asks the terminal for the colours the app derives its own from:
// the background, for the row highlight, and the block's four hues. The
// palette has no request of its own in the runtime, so those are OSC 4
// queries written raw, one per entry; each answer comes back as an OSC the
// input decoder does not know, and reaches Update as one.
func askColours() tea.Cmd {
	var queries strings.Builder
	for _, entry := range blockEntries {
		fmt.Fprintf(&queries, "\x1b]4;%d;?\x07", entry)
	}
	return batch(tea.RequestBackgroundColor, tea.Raw(queries.String()))
}

// paletteReply reads a terminal's answer to an OSC 4 query — ESC ] 4 ; n ;
// rgb:RRRR/GGGG/BBBB, ended by BEL or by ST — and returns which entry it
// answers for and its colour.
func paletteReply(seq string) (ansi.BasicColor, color.Color, bool) {
	body, ok := strings.CutPrefix(seq, "\x1b]4;")
	if !ok {
		return 0, nil, false
	}
	body = strings.TrimSuffix(strings.TrimSuffix(body, "\x07"), "\x1b\\")
	index, spec, ok := strings.Cut(body, ";")
	if !ok {
		return 0, nil, false
	}
	n, err := strconv.Atoi(index)
	if err != nil || n < 0 || n > 15 {
		return 0, nil, false
	}
	c := ansi.XParseColor(spec)
	return ansi.BasicColor(n), c, c != nil
}

// towardWhite mixes a colour a fraction of the way to white, channel by
// channel. Unlike lipgloss.Lighten, which adds the same amount to each
// channel and so flattens a yellow into cream, this keeps the channels in
// proportion, and with them the hue.
func towardWhite(c color.Color, t float64) color.Color {
	r, g, b, _ := c.RGBA()
	mix := func(v uint32) uint8 {
		f := float64(v >> 8)
		return uint8(math.Round(f + (255-f)*min(t, 1)))
	}
	return color.RGBA{R: mix(r), G: mix(g), B: mix(b), A: 255}
}

// luminance is WCAG's relative luminance: how light a colour looks, from 0
// for black to 1 for white.
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	linear := func(v uint32) float64 {
		s := float64(v>>8) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

// contrast is WCAG's contrast ratio between two colours, from 1 to 21.
func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}
