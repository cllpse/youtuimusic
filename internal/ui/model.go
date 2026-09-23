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

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cllpse/youtuimusic/internal/player"
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

	services Services

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

	// playingID is the track mpv is on, which is not necessarily the one
	// under the cursor — it is what "next" is relative to when a track ends.
	playingID string
	// status describes what the table is showing.
	status  string
	loading bool

	// Mouse state: a drag on the progress bar, and enough of the last click
	// to recognise the second one of a pair.
	scrubbing       bool
	lastClickAt     time.Time
	lastClickRegion region
	lastClickRow    int

	// prefetchGen invalidates a pending prefetch when the cursor moves
	// again before it fires.
	prefetchGen int

	// now is overridable so click timing is testable.
	now func() time.Time

	// bar springs towards the playback position rather than jumping to it.
	bar progress.Model

	Err error
}

const sidebarWidth = 28

// New returns a Model with nothing loaded. A zero Services makes a model
// that talks to nothing, which is what the view tests use.
func New(s Services) Model {
	return Model{
		focus:    PaneSidebar,
		services: s,
		loading:  s.Library != nil,
		now:      time.Now,
		// The blend needs the half block: two colours per cell doubles the
		// resolution the gradient has to work with. The empty half stays a
		// thin rule so the untravelled part of the bar keeps quiet.
		bar: progress.New(
			progress.WithoutPercentage(),
			progress.WithDefaultBlend(),
			progress.WithFillCharacters(progress.DefaultFullCharHalfBlock, '─'),
		),
	}
}

// syncBar aims the progress bar at the current position. The bar springs
// towards it over the next few frames rather than snapping.
func (m *Model) syncBar() tea.Cmd {
	if m.Length <= 0 {
		return m.bar.SetPercent(0)
	}
	return m.bar.SetPercent(float64(m.Position) / float64(m.Length))
}

// Init starts the first fetch and opens the stream of player events.
func (m Model) Init() tea.Cmd {
	return batch(m.fetchPlaylists(), m.watchEvents())
}

// batch drops the nil commands a zero Services produces.
func batch(cmds ...tea.Cmd) tea.Cmd {
	live := make([]tea.Cmd, 0, len(cmds))
	for _, c := range cmds {
		if c != nil {
			live = append(live, c)
		}
	}
	if len(live) == 0 {
		return nil
	}
	return tea.Batch(live...)
}

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
		_, barWidth := m.barGeometry()
		m.bar.SetWidth(barWidth)
		return m, nil

	case progress.FrameMsg:
		bar, cmd := m.bar.Update(msg)
		m.bar = bar
		return m, cmd

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case prefetchTickMsg:
		// A later move armed its own; this one is stale.
		if msg.generation != m.prefetchGen {
			return m, nil
		}
		if t, ok := m.SelectedTrack(); ok {
			return m, m.prefetch(t.VideoID)
		}
		return m, nil

	case playlistsMsg:
		m.Playlists = m.Playlists[:0]
		for _, p := range msg {
			m.Playlists = append(m.Playlists, Playlist{ID: p.ID, Title: p.Title})
		}
		m.sidebarCursor, m.loading, m.Err = 0, false, nil
		// Open the first playlist, so the table is not empty on arrival.
		if len(m.Playlists) > 0 {
			m.loading = true
			return m, m.fetchTracks(m.Playlists[0])
		}
		return m, nil

	case tracksMsg:
		m.Tracks = fromAPI(msg.tracks)
		m.trackCursor, m.loading, m.Err = 0, false, nil
		m.status = msg.source
		if len(m.Tracks) > 0 {
			return m, m.prefetch(m.Tracks[0].VideoID)
		}
		return m, nil

	case ratedMsg:
		if msg.err != nil {
			// The row was changed before the call; put it back.
			m.setRating(msg.videoID, msg.previous)
			m.Err = msg.err
		}
		return m, nil

	case playingMsg:
		m.playingID, m.NowPlaying, m.Length = msg.videoID, msg.title, msg.length
		m.Position, m.Paused, m.Err = 0, false, nil
		if next, ok := m.trackAfter(msg.videoID); ok {
			return m, batch(m.prefetch(next.VideoID), m.syncBar())
		}
		return m, m.syncBar()

	case eventMsg:
		return m.handleEvent(player.Event(msg))

	case errMsg:
		m.Err, m.loading = msg.err, false
		return m, nil
	}
	return m, nil
}

// handleEvent folds an mpv property change into the model. Every branch
// re-arms the watch; forgetting to would silently end the event stream.
func (m Model) handleEvent(ev player.Event) (tea.Model, tea.Cmd) {
	switch ev.Name {
	case "time-pos":
		// A drag owns the position until the button comes up. mpv carries on
		// playing and reporting where it actually is, and letting that
		// through makes the bar fight the pointer.
		if f, ok := ev.Data.(float64); ok && !m.scrubbing {
			m.Position = time.Duration(f * float64(time.Second))
			return m, batch(m.watchEvents(), m.syncBar())
		}
	case "duration":
		if f, ok := ev.Data.(float64); ok && f > 0 {
			m.Length = time.Duration(f * float64(time.Second))
			return m, batch(m.watchEvents(), m.syncBar())
		}
	case "pause":
		if b, ok := ev.Data.(bool); ok {
			m.Paused = b
		}
	case "eof-reached":
		// mpv reports this at the end of a file, which is where a playlist
		// advances on its own.
		if b, ok := ev.Data.(bool); ok && b {
			if next, ok := m.trackAfter(m.playingID); ok {
				return m, batch(m.watchEvents(), m.play(next))
			}
		}
	}
	return m, m.watchEvents()
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
			if m.Query != "" {
				m.loading, m.focus = true, PaneTracks
				return m, m.runSearch(m.Query)
			}
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

	case "enter":
		if m.focus == PaneSidebar {
			if p, ok := m.SelectedPlaylist(); ok {
				m.loading = true
				return m, m.fetchTracks(p)
			}
			return m, nil
		}
		if t, ok := m.SelectedTrack(); ok {
			// Show the track at once; resolving it takes a moment.
			m.NowPlaying, m.Position, m.Length = nowPlaying(t), 0, t.Duration
			return m, m.play(t)
		}

	case " ", "space":
		return m, m.togglePause()

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
		return m.afterCursorMove()
	case "down", "j":
		m.moveCursor(1)
		return m.afterCursorMove()

	case "+", "=":
		return m.applyRating(RatingUp)
	case "-", "_":
		return m.applyRating(RatingDown)
	}
	return m, nil
}

// afterCursorMove arms a prefetch when the move happened in the track list.
func (m Model) afterCursorMove() (tea.Model, tea.Cmd) {
	if m.focus != PaneTracks {
		return m, nil
	}
	return m.schedulePrefetch()
}

// prefetchDelay is how long the cursor has to sit still before the row under
// it is resolved. Without it, holding j down the length of a playlist starts
// a yt-dlp process per row.
const prefetchDelay = 250 * time.Millisecond

type prefetchTickMsg struct{ generation int }

// schedulePrefetch arms a delayed resolve of the highlighted row, cancelling
// any earlier one by moving the generation past it.
func (m Model) schedulePrefetch() (tea.Model, tea.Cmd) {
	if m.services.Streams == nil {
		return m, nil
	}
	m.prefetchGen++
	generation := m.prefetchGen
	return m, tea.Tick(prefetchDelay, func(time.Time) tea.Msg {
		return prefetchTickMsg{generation: generation}
	})
}

// trackAfter returns the row following a video id, which is what plays when
// the current one ends.
func (m Model) trackAfter(videoID string) (Track, bool) {
	for i, t := range m.Tracks {
		if t.VideoID == videoID && i+1 < len(m.Tracks) {
			return m.Tracks[i+1], true
		}
	}
	return Track{}, false
}

func (m *Model) setRating(videoID string, r Rating) {
	for i := range m.Tracks {
		if m.Tracks[i].VideoID == videoID {
			m.Tracks[i].Rating = r
			return
		}
	}
}

// moveCursor moves the focused pane's cursor, clamped to its list.
func (m *Model) moveCursor(delta int) {
	if m.focus == PaneSidebar {
		m.sidebarCursor = clamp(m.sidebarCursor+delta, len(m.Playlists))
		return
	}
	m.trackCursor = clamp(m.trackCursor+delta, len(m.Tracks))
}

// applyRating toggles the thumbs state of the highlighted track: rating it
// the same way twice clears it, which is what the YouTube Music API does.
//
// The row changes immediately and the server is told afterwards. Waiting for
// the round trip would make a keystroke feel like a network call; if it
// fails, the message handler puts the row back.
func (m Model) applyRating(r Rating) (tea.Model, tea.Cmd) {
	if m.focus != PaneTracks || m.trackCursor >= len(m.Tracks) {
		return m, nil
	}
	previous := m.Tracks[m.trackCursor].Rating
	if previous == r {
		r = RatingNone
	}
	m.Tracks[m.trackCursor].Rating = r
	return m, m.rate(m.Tracks[m.trackCursor].VideoID, r, previous)
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
	failed   = lipgloss.NewStyle().Foreground(lipgloss.Color("204"))
	selected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("170"))
	active   = lipgloss.NewStyle().Foreground(lipgloss.Color("170"))
)

func (m Model) View() tea.View {
	if m.width == 0 {
		// Before the first resize there is nothing to draw, but the terminal
		// modes still have to be declared or the frame turns them back off.
		v := tea.NewView("")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	bodyHeight := m.bodyHeight()
	sidebar := m.renderSidebar(bodyHeight)
	tracks := m.renderTracks(m.width-sidebarWidth, bodyHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, tracks)

	// v2 makes terminal modes part of the view rather than a program option,
	// so the alt screen is declared here alongside the content.
	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, body, m.renderProgress()))
	v.AltScreen = true
	v.WindowTitle = "youtuimusic"
	// Cell motion reports drags, which is what scrubbing the bar needs.
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// progressBlock is the four rows below the lists: a blank, the title, the
// bar, and a blank.
const progressBlock = 4

// bodyHeight is how many rows the lists get.
func (m Model) bodyHeight() int {
	if h := m.height - progressBlock; h > 1 {
		return h
	}
	return 1
}

// fraction is how far through the track the position is.
func (m Model) fraction() float64 {
	if m.Length <= 0 {
		return 0
	}
	return float64(m.Position) / float64(m.Length)
}

// barGeometry is the column the progress bar starts at and how wide it is.
// Rendering and hit-testing both go through this, so a click lands where the
// bar appears to be.
func (m Model) barGeometry() (start, width int) {
	width = m.width - 16
	if width < 4 {
		width = 4
	}
	return lipgloss.Width(formatDuration(m.Position)) + 1, width
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
	if m.Paused && title != "" {
		title += dim.Render("  paused")
	}
	switch {
	case m.Searching:
		title = "/" + m.Query + "█"
	case m.Err != nil:
		title = failed.Render(truncate(m.Err.Error(), m.width))
	case m.loading:
		title = dim.Render("Loading…")
	case title == "" && m.status != "":
		title = dim.Render(m.status)
	case title == "":
		title = dim.Render("Nothing playing")
	}

	// A drag renders the exact position so the bar tracks the pointer; the
	// spring would lag behind it. Everything else is animated.
	bar := m.bar.View()
	if m.scrubbing {
		bar = m.bar.ViewAs(m.fraction())
	}

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
