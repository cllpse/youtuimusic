package ui

import (
	"errors"
	"image/color"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// waiting is a sized model whose block says LOADING.
func waiting(t *testing.T) Model {
	t.Helper()
	m := New(Services{})
	m.loading = true
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	return next.(Model)
}

// spinnerTick finds the spinner's tick among what a command would run, so a
// test can deliver the real one — the spinner ignores a tick not its own.
func spinnerTick(cmd tea.Cmd) (spinner.TickMsg, bool) {
	if cmd == nil {
		return spinner.TickMsg{}, false
	}
	switch msg := cmd().(type) {
	case spinner.TickMsg:
		return msg, true
	case tea.BatchMsg:
		for _, c := range msg {
			if tick, ok := spinnerTick(c); ok {
				return tick, true
			}
		}
	}
	return spinner.TickMsg{}, false
}

// The block says LOADING with the list loader's own glyph beside it, and the
// glyph turns as the spinner ticks.
func TestTheLoadingBlockSpins(t *testing.T) {
	m := New(Services{})
	m.loading = true
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = next.(Model)
	if !m.spinning {
		t.Fatal("a wait began and the spinner did not start")
	}
	frame := func(m Model) string {
		// The glyph against the block's left edge and right against the
		// word, the padding only after it: "▓LOADING ".
		block := ansi.Strip(m.statusBlock())
		inner, closed := strings.CutSuffix(block, " ")
		glyph, word, _ := strings.Cut(inner, labelLoading)
		if !closed || word != "" || !strings.HasSuffix(inner, labelLoading) ||
			!slices.Contains(spinner.Pulse.Frames, glyph) {
			t.Fatalf("block reads %q, want a Pulse frame then %q", block, labelLoading+" ")
		}
		return glyph
	}
	first := frame(m)
	tick, ok := spinnerTick(cmd)
	if !ok {
		t.Fatal("no spinner tick was started")
	}
	next, _ = m.Update(tick)
	if frame(next.(Model)) == first {
		t.Error("the glyph did not move on a tick")
	}
}

// One loop, however many things begin to wait: a second wait while the
// spinner turns starts no second loop, which would turn it twice as fast.
func TestThereIsOnlyEverOneSpinner(t *testing.T) {
	m := waiting(t)
	m.loadingMore = true
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20}); func() bool {
		_, ok := spinnerTick(cmd)
		return ok
	}() {
		t.Error("a second wait started a second spinner loop")
	}
}

// A track that has not started sounding is a wait too, so the spinner turns
// for it — and stops on the first tick after it sounds.
func TestTheSpinnerTurnsWhileATrackLoads(t *testing.T) {
	m := New(Services{})
	m.requested = "a"
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = next.(Model)
	tick, ok := spinnerTick(cmd)
	if !m.spinning || !ok {
		t.Fatal("a track asked for did not start the spinner")
	}
	m.requested = ""
	next, cmd = m.Update(tick)
	m = next.(Model)
	if m.spinning || cmd != nil {
		t.Errorf("still spinning=%v with a next tick=%v after the track sounded", m.spinning, cmd != nil)
	}
	if strings.Contains(ansi.Strip(m.statusBlock()), labelLoading) {
		t.Error("the block still says it is loading")
	}
}

// The terminal answers a palette query with OSC 4, ended by BEL or by ST,
// saying which entry it is answering for.
func TestPaletteReplyIsRead(t *testing.T) {
	want := color.RGBA{0xe0, 0xaf, 0x68, 0xff}
	for _, seq := range []string{
		"\x1b]4;3;rgb:e0e0/afaf/6868\x07",
		"\x1b]4;3;rgb:e0e0/afaf/6868\x1b\\",
	} {
		entry, got, ok := paletteReply(seq)
		if !ok || entry != 3 || got != want {
			t.Errorf("paletteReply(%q) = %d, %v, %v; want 3, %v", seq, entry, got, ok, want)
		}
	}
	for _, seq := range []string{
		"\x1b]11;rgb:0000/0000/0000\x07", // the background, not the palette
		"\x1b]4;3;not-a-colour\x07",
		"\x1b]4;x;rgb:ffff/0000/0000\x07",
	} {
		if _, _, ok := paletteReply(seq); ok {
			t.Errorf("paletteReply(%q) was taken", seq)
		}
	}
}

// The block's four hues are asked for wherever the background is: at
// startup, and again when the window or the focus says the theme may have
// changed.
func TestTheTerminalIsAskedForTheBlocksHues(t *testing.T) {
	const want = "\x1b]4;1;?\x07\x1b]4;2;?\x07\x1b]4;3;?\x07\x1b]4;4;?\x07"
	asks := func(cmd tea.Cmd) bool {
		var walk func(tea.Msg) bool
		walk = func(msg tea.Msg) bool {
			switch msg := msg.(type) {
			case tea.BatchMsg:
				for _, c := range msg {
					if c != nil && walk(c()) {
						return true
					}
				}
			case tea.RawMsg:
				return msg.Msg == want
			}
			return false
		}
		return cmd != nil && walk(cmd())
	}
	if !asks(askColours()) {
		t.Fatal("askColours does not ask for red, green, yellow and blue")
	}
	m := New(Services{})
	if _, cmd := m.Update(tea.FocusMsg{}); !asks(cmd) {
		t.Error("coming back into focus does not ask again")
	}
}

// answered is m once the terminal has said what its red, green, yellow and
// blue are — here a Tokyo Night palette, a theme with mid-tone hues.
func answered(t *testing.T, m Model) Model {
	t.Helper()
	for _, seq := range []string{
		"\x1b]4;1;rgb:f7f7/7676/8e8e\x07",
		"\x1b]4;2;rgb:9e9e/cece/6a6a\x07",
		"\x1b]4;3;rgb:e0e0/afaf/6868\x07",
		"\x1b]4;4;rgb:7a7a/a2a2/f7f7\x07",
	} {
		next, _ := m.Update(uv.UnknownOscEvent(seq))
		m = next.(Model)
	}
	return m
}

// sgrOf is how a style's text opens, taken from the style API rather than
// spelled out.
func sgrOf(style lipgloss.Style) string {
	s := style.Render("x")
	return s[:strings.Index(s, "x")]
}

// Every state of the block, once the terminal has answered: the fill is the
// state's hue lightened, the word on it the same hue darkened, and the word
// reads on it.
func TestEveryStateIsLightenedWithDarkenedText(t *testing.T) {
	for _, hue := range blockEntries {
		m := answered(t, New(Services{}))
		c := m.blockColoursFor(hue)
		if c.fill == nil {
			t.Fatalf("entry %d: nothing derived", hue)
		}
		if luminance(c.fill) <= luminance(m.blockColoursFor(hue).ink) {
			t.Errorf("entry %d: the fill is not the lighter of the two", hue)
		}
		if got := contrast(c.ink, c.fill); got < blockContrast {
			t.Errorf("entry %d: the word reads at %.1f:1, want at least %.1f", hue, got, blockContrast)
		}
	}

	// And it is what the block draws, in each state it can be in.
	m := answered(t, New(Services{}))
	m.width = 80
	for _, tc := range []struct {
		name string
		set  func(*Model)
		hue  color.Color
	}{
		{"ready", func(*Model) {}, good},
		{"error", func(m *Model) { m.Err = errors.New("x") }, alert},
		{"playing", func(m *Model) { m.playing = Track{VideoID: "a"} }, live},
	} {
		state := m
		tc.set(&state)
		c := state.blockColoursFor(tc.hue)
		want := sgrOf(statusBlockStyle.Foreground(c.ink).Background(c.fill))
		if !strings.Contains(state.statusBlock(), want) {
			t.Errorf("%s: block %q does not open with %q", tc.name, state.statusBlock(), want)
		}
	}
}

// Before the terminal answers — and in one that never does — the block is
// what it always was: the entry itself, the background colour on it.
func TestUnansweredStatesKeepTheirPaletteColours(t *testing.T) {
	m := New(Services{})
	m.width = 80
	want := sgrOf(statusBlockStyle.Background(good))
	if !strings.Contains(m.statusBlock(), want) {
		t.Errorf("block %q does not open with %q", m.statusBlock(), want)
	}
}

// The amounts adapt to the theme: a deep hue is lightened further than a pale
// one before dark text will read on it, and the word reads on both.
func TestTheAmountsFollowTheTheme(t *testing.T) {
	deep := deriveBlock(color.RGBA{0x00, 0x00, 0xee, 0xff}) // xterm's blue
	pale := deriveBlock(color.RGBA{0xf9, 0xe2, 0xaf, 0xff}) // Catppuccin's yellow
	for name, c := range map[string]blockColours{"deep": deep, "pale": pale} {
		if got := contrast(c.ink, c.fill); got < blockContrast {
			t.Errorf("%s: the word reads at %.1f:1", name, got)
		}
		if luminance(c.fill) < blockFillLuminance {
			t.Errorf("%s: the fill is too dark to carry dark text", name)
		}
	}
}
