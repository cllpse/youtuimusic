// Package ui is the bubbletea layer: a sidebar of playlists, a table of
// tracks, and a progress bar.
//
// Rendering is a pure function of Model. Nothing in here reaches out to the
// network or the player directly — side effects happen in tea.Cmds, so the
// view can always be rendered from state alone and tested without a terminal.
package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Pane identifies which half of the split has keyboard focus.
type Pane int

const (
	PaneSidebar Pane = iota
	PaneTracks
)

// Rating is a track's thumbs state.
type Rating int

const (
	RatingNone Rating = iota
	RatingUp
	RatingDown
)

func (r Rating) glyph() string {
	switch r {
	case RatingUp:
		return "+"
	case RatingDown:
		return "-"
	default:
		return " "
	}
}

// Playlist is one row in the sidebar.
type Playlist struct {
	ID    string
	Title string
	Count int
}

// Track is one row in the table.
type Track struct {
	VideoID  string
	Title    string
	Artist   string
	Duration time.Duration
	Rating   Rating
}

// Model is the whole application state.
type Model struct {
	width, height int

	Playlists []Playlist
	Tracks    []Track

	sidebarCursor int
	trackCursor   int
	focus         Pane

	// Playback state, fed from player events.
	NowPlaying string
	Position   time.Duration
	Length     time.Duration
	Paused     bool

	// Search
	Searching bool
	Query     string

	Err error
}

const sidebarWidth = 28

// New returns a Model with nothing loaded.
func New() Model {
	return Model{focus: PaneSidebar}
}

func (m Model) Init() tea.Cmd { return nil }

// SelectedTrack returns the row under the cursor, if any.
func (m Model) SelectedTrack() (Track, bool) {
	if m.trackCursor < 0 || m.trackCursor >= len(m.Tracks) {
		return Track{}, false
	}
	return m.Tracks[m.trackCursor], true
}

// SelectedPlaylist returns the sidebar row under the cursor, if any.
func (m Model) SelectedPlaylist() (Playlist, bool) {
	if m.sidebarCursor < 0 || m.sidebarCursor >= len(m.Playlists) {
		return Playlist{}, false
	}
	return m.Playlists[m.sidebarCursor], true
}

// Focus reports which pane has keyboard focus.
func (m Model) Focus() Pane { return m.focus }

// TrackCursor reports the highlighted track row.
func (m Model) TrackCursor() int { return m.trackCursor }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// While searching, everything but the control keys is literal text.
	if m.Searching {
		switch key {
		case "esc":
			m.Searching, m.Query = false, ""
		case "enter":
			m.Searching = false
		case "backspace":
			if m.Query != "" {
				m.Query = m.Query[:len(m.Query)-1]
			}
		default:
			if len(key) == 1 {
				m.Query += key
			}
		}
		return m, nil
	}

	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "/":
		m.Searching, m.Query = true, ""

	case "tab":
		if m.focus == PaneSidebar {
			m.focus = PaneTracks
		} else {
			m.focus = PaneSidebar
		}

	case "left", "h":
		m.focus = PaneSidebar
	case "right", "l":
		m.focus = PaneTracks

	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)

	case "+", "=":
		m.rateSelected(RatingUp)
	case "-", "_":
		m.rateSelected(RatingDown)
	}
	return m, nil
}

// moveCursor moves the focused pane's cursor, clamped to its list.
func (m *Model) moveCursor(delta int) {
	if m.focus == PaneSidebar {
		m.sidebarCursor = clamp(m.sidebarCursor+delta, len(m.Playlists))
		return
	}
	m.trackCursor = clamp(m.trackCursor+delta, len(m.Tracks))
}

// rateSelected toggles the thumbs state of the highlighted track: rating it
// the same way twice clears it, which is what the YouTube Music API does.
func (m *Model) rateSelected(r Rating) {
	if m.focus != PaneTracks || m.trackCursor >= len(m.Tracks) {
		return
	}
	if m.Tracks[m.trackCursor].Rating == r {
		m.Tracks[m.trackCursor].Rating = RatingNone
		return
	}
	m.Tracks[m.trackCursor].Rating = r
}

func clamp(v, length int) int {
	if length == 0 {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v >= length {
		return length - 1
	}
	return v
}

var (
	dim      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	selected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("170"))
	active   = lipgloss.NewStyle().Foreground(lipgloss.Color("170"))
)

func (m Model) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}

	bodyHeight := m.height - 4 // progress block: blank, title, bar, blank
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	sidebar := m.renderSidebar(bodyHeight)
	tracks := m.renderTracks(m.width-sidebarWidth, bodyHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, tracks)

	// v2 makes terminal modes part of the view rather than a program option,
	// so the alt screen is declared here alongside the content.
	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, body, m.renderProgress()))
	v.AltScreen = true
	v.WindowTitle = "youtuimusic"
	return v
}

func (m Model) renderSidebar(height int) string {
	var b strings.Builder
	for i := 0; i < height; i++ {
		line := ""
		if i < len(m.Playlists) {
			p := m.Playlists[i]
			line = truncate(p.Title, sidebarWidth-2)
			switch {
			case i == m.sidebarCursor && m.focus == PaneSidebar:
				line = selected.Render("> " + line)
			case i == m.sidebarCursor:
				line = active.Render("  " + line)
			default:
				line = dim.Render("  " + line)
			}
		}
		b.WriteString(pad(line, sidebarWidth))
		if i < height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (m Model) renderTracks(width, height int) string {
	if width < 10 {
		width = 10
	}
	var b strings.Builder
	for i := 0; i < height; i++ {
		line := ""
		if i < len(m.Tracks) {
			line = m.trackLine(m.Tracks[i], width)
			switch {
			case i == m.trackCursor && m.focus == PaneTracks:
				line = selected.Render(line)
			case i == m.trackCursor:
				line = active.Render(line)
			}
		}
		b.WriteString(pad(line, width))
		if i < height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (m Model) trackLine(t Track, width int) string {
	durCol := 6
	rateCol := 2
	rest := width - durCol - rateCol - 2
	if rest < 10 {
		rest = 10
	}
	titleW := rest / 2
	artistW := rest - titleW
	return fmt.Sprintf("%s %-*s %-*s %*s",
		t.Rating.glyph(),
		titleW, truncate(t.Title, titleW),
		artistW, truncate(t.Artist, artistW),
		durCol, formatDuration(t.Duration))
}

func (m Model) renderProgress() string {
	title := m.NowPlaying
	if title == "" {
		title = dim.Render("Nothing playing")
	}
	if m.Searching {
		title = "/" + m.Query + "█"
	}

	barWidth := m.width - 16
	if barWidth < 4 {
		barWidth = 4
	}
	filled := 0
	if m.Length > 0 {
		filled = int(float64(barWidth) * (float64(m.Position) / float64(m.Length)))
		filled = clamp(filled, barWidth+1)
	}
	bar := active.Render(strings.Repeat("━", filled)) +
		dim.Render(strings.Repeat("─", barWidth-filled))

	return "\n" + title + "\n" +
		fmt.Sprintf("%s %s %s",
			formatDuration(m.Position), bar, formatDuration(m.Length)) + "\n"
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return " 0:00"
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%2d:%02d", total/60, total%60)
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	runes := []rune(s)
	if w == 1 {
		return string(runes[:1])
	}
	return string(runes[:w-1]) + "…"
}

func pad(s string, w int) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}
