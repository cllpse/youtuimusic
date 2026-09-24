// Package ui is the bubbletea layer: a row of playlist tabs, a table of
// tracks, and a progress bar.
//
// Rendering is a pure function of Model. Nothing in here reaches out to the
// network or the player directly — side effects happen in tea.Cmds, so the
// view can always be rendered from state alone and tested without a terminal.
package ui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cllpse/youtuimusic/internal/player"
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

// Playlist is one tab.
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

// searchTabID names the tab holding the last search. It cannot collide with
// a playlist id, which is why it is not a plausible one.
const searchTabID = "\x00search"

// likedPlaylistID is YouTube's fixed id for the auto playlist a thumbs-up
// adds to.
const likedPlaylistID = "LM"

// Model is the whole application state.
type Model struct {
	width, height int

	services Services

	Playlists []Playlist
	Tracks    []Track

	tabCursor   int
	trackCursor int
	// trackOffset is the first row on screen. Without it a list longer than
	// the window has no way to reach its end.
	trackOffset int

	// Playback state, fed from player events.
	NowPlaying string
	Position   time.Duration
	Length     time.Duration
	Paused     bool

	// Search
	Searching bool
	Query     string
	// searchTitle labels the results tab, and is empty when there is none.
	searchTitle string

	// playing is the track mpv is on, held whole rather than by id so the
	// controls can still show and rate it after another tab is opened.
	playing Track
	repeat  Repeat
	loading bool

	// tracks already fetched, by tab id, so going back to a tab is instant.
	cache map[string][]Track
	// showingID is the tab the visible list came from, which tells a refetch
	// of the same tab from a move to another one.
	showingID string
	// lastRated is re-applied over a refreshed list. A like reaches Liked
	// Music a moment after the call returns, so a list fetched straight
	// after can still describe the track the old way.
	lastRated struct {
		videoID string
		rating  Rating
	}

	// Mouse state: a drag on the progress bar, and enough of the last click
	// to recognise the second one of a pair.
	scrubbing       bool
	lastClickAt     time.Time
	lastClickRegion region
	lastClickRow    int

	// Generations invalidate a pending prefetch or tab load when the thing
	// that armed it has moved on.
	prefetchGen int
	tabGen      int

	// now is overridable so click timing is testable.
	now func() time.Time

	// bar renders the playback position. It holds no animation state: the
	// position is drawn where it is.
	bar  progress.Model
	spin spinner.Model

	Err error
}

// New returns a Model with nothing loaded. A zero Services makes a model
// that talks to nothing, which is what the view tests use.
func New(s Services) Model {
	return Model{
		services: s,
		loading:  s.Library != nil,
		now:      time.Now,
		cache:    map[string][]Track{},
		bar:      newBar(),
		spin:     spinner.New(spinner.WithSpinner(spinner.Pulse), spinner.WithStyle(active)),
	}
}

// Init starts the first fetch and opens the stream of player events.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.fetchPlaylists(), m.watchEvents()}
	if m.loading {
		cmds = append(cmds, m.spin.Tick)
	}
	return batch(cmds...)
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

// startLoading turns the spinner on and starts its tick loop. The loop runs
// only while something is loading, so an idle screen is not redrawn eight
// times a second forever.
func (m *Model) startLoading() tea.Cmd {
	if m.loading {
		return nil // already ticking
	}
	m.loading = true
	return m.spin.Tick
}

// ---------------------------------------------------------------- tabs ---

// tabCount is the playlists plus the search results, when there are any.
func (m Model) tabCount() int {
	if m.searchTitle != "" {
		return len(m.Playlists) + 1
	}
	return len(m.Playlists)
}

func (m Model) tabAt(i int) Playlist {
	if i >= 0 && i < len(m.Playlists) {
		return m.Playlists[i]
	}
	if i == len(m.Playlists) && m.searchTitle != "" {
		return Playlist{ID: searchTabID, Title: m.searchTitle}
	}
	return Playlist{}
}

// SelectedPlaylist returns the tab in front.
func (m Model) SelectedPlaylist() (Playlist, bool) {
	if m.tabCursor < 0 || m.tabCursor >= m.tabCount() {
		return Playlist{}, false
	}
	return m.tabAt(m.tabCursor), true
}

// selectTab brings a tab to the front and shows what is in it.
func (m Model) selectTab(i int) (Model, tea.Cmd) {
	if i < 0 || i >= m.tabCount() || i == m.tabCursor {
		return m, nil
	}
	m.tabCursor = i
	return m.showTab()
}

// showTab puts the front tab's tracks on screen, fetching them the first
// time it is opened and serving them from memory after that.
func (m Model) showTab() (Model, tea.Cmd) {
	m.trackCursor, m.trackOffset = 0, 0
	tab := m.tabAt(m.tabCursor)

	if tracks, ok := m.cache[tab.ID]; ok {
		m.Tracks, m.loading, m.Err = tracks, false, nil
		m.showingID = tab.ID
		if len(tracks) > 0 {
			return m, m.prefetch(tracks[0].VideoID)
		}
		return m, nil
	}
	m.Tracks, m.showingID = nil, ""
	return m, batch(m.startLoading(), m.scheduleTabLoad())
}

// tabLoadDelay lets a run across the tabs settle before anything is asked
// of the server. Holding a key would otherwise be one request per tab.
const tabLoadDelay = 150 * time.Millisecond

type tabLoadMsg struct{ generation int }

func (m *Model) scheduleTabLoad() tea.Cmd {
	if m.services.Library == nil {
		return nil
	}
	m.tabGen++
	generation := m.tabGen
	return tea.Tick(tabLoadDelay, func(time.Time) tea.Msg {
		return tabLoadMsg{generation: generation}
	})
}

// -------------------------------------------------------------- cursor ---

// SelectedTrack returns the row under the cursor, if any.
func (m Model) SelectedTrack() (Track, bool) {
	if m.trackCursor < 0 || m.trackCursor >= len(m.Tracks) {
		return Track{}, false
	}
	return m.Tracks[m.trackCursor], true
}

// TrackCursor reports the highlighted track row.
func (m Model) TrackCursor() int { return m.trackCursor }

// moveCursor moves the track cursor and scrolls to keep it in view.
func (m *Model) moveCursor(delta int) {
	m.trackCursor = clamp(m.trackCursor+delta, len(m.Tracks))
	m.scroll()
}

// scrollBy moves the window and leaves the selection where it is, so the
// list can be looked through without losing the cursor's place.
func (m *Model) scrollBy(delta int) {
	height := m.bodyHeight()
	m.trackOffset = min(max(m.trackOffset+delta, 0), max(0, len(m.Tracks)-height))
}

// scroll moves the window only far enough to keep the cursor on screen.
func (m *Model) scroll() {
	height := m.bodyHeight()
	if m.trackCursor < m.trackOffset {
		m.trackOffset = m.trackCursor
	}
	if m.trackCursor >= m.trackOffset+height {
		m.trackOffset = m.trackCursor - height + 1
	}
	m.trackOffset = min(m.trackOffset, max(0, len(m.Tracks)-height))
	m.trackOffset = max(m.trackOffset, 0)
}

func clamp(v, length int) int {
	if length == 0 {
		return 0
	}
	return min(max(v, 0), length-1)
}

// -------------------------------------------------------------- update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		_, barWidth := m.barGeometry()
		m.bar.SetWidth(barWidth)
		m.scroll()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case spinner.TickMsg:
		// The loop stops as soon as nothing is loading.
		if !m.loading {
			return m, nil
		}
		spin, cmd := m.spin.Update(msg)
		m.spin = spin
		return m, cmd

	case prefetchTickMsg:
		if msg.generation != m.prefetchGen {
			return m, nil // a later move armed its own
		}
		if t, ok := m.SelectedTrack(); ok {
			return m, m.prefetch(t.VideoID)
		}
		return m, nil

	case tabLoadMsg:
		if msg.generation != m.tabGen {
			return m, nil
		}
		tab := m.tabAt(m.tabCursor)
		if tab.ID == "" || tab.ID == searchTabID {
			return m, nil
		}
		return m, m.fetchTracks(tab)

	case playlistsMsg:
		m.Playlists = m.Playlists[:0]
		for _, p := range msg {
			m.Playlists = append(m.Playlists, Playlist{ID: p.ID, Title: p.Title})
		}
		m.tabCursor, m.Err = 0, nil
		if m.tabCount() == 0 {
			m.loading = false
			return m, nil
		}
		return m.showTab()

	case tracksMsg:
		tracks := fromAPI(msg.tracks)
		// The server can still describe a just-rated track the old way, so
		// what this app did wins over what the list says.
		if m.lastRated.videoID != "" {
			for i := range tracks {
				if tracks[i].VideoID == m.lastRated.videoID {
					tracks[i].Rating = m.lastRated.rating
				}
			}
		}
		m.cache[msg.id] = tracks
		if m.tabAt(m.tabCursor).ID != msg.id {
			return m, nil // the tab moved on while this was in flight
		}
		// A refetch of the list already on screen keeps the reader's place
		// in it; arriving at a new tab starts at the top.
		refresh := m.showingID == msg.id
		m.Tracks, m.loading, m.Err = tracks, false, nil
		m.showingID = msg.id
		if refresh {
			m.trackCursor = clamp(m.trackCursor, len(tracks))
			m.scroll()
			return m, nil
		}
		m.trackCursor, m.trackOffset = 0, 0
		if len(m.Tracks) > 0 {
			return m, m.prefetch(m.Tracks[0].VideoID)
		}
		return m, nil

	case searchMsg:
		m.searchTitle = msg.query
		m.cache[searchTabID] = fromAPI(msg.tracks)
		m.tabCursor = m.tabCount() - 1
		return m.showTab()

	case ratedMsg:
		if msg.err != nil {
			// The row was changed before the call; put it back.
			m.setRating(msg.videoID, msg.previous)
			m.Err = msg.err
			return m, nil
		}
		m.lastRated.videoID, m.lastRated.rating = msg.videoID, msg.applied
		// A thumbs-up adds the track to Liked Music and clearing one takes
		// it out again, so what is held for that tab no longer describes it.
		delete(m.cache, likedPlaylistID)
		if tab := m.tabAt(m.tabCursor); tab.ID == likedPlaylistID {
			return m, m.fetchTracks(tab)
		}
		return m, nil

	case playingMsg:
		m.NowPlaying, m.Length = msg.title, msg.length
		m.Position, m.Paused, m.Err = 0, false, nil
		m.playing = msg.track
		if next, ok := m.following(); ok && next.VideoID != m.playing.VideoID {
			return m, m.prefetch(next.VideoID)
		}
		return m, nil

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
		}
	case "duration":
		if f, ok := ev.Data.(float64); ok && f > 0 {
			m.Length = time.Duration(f * float64(time.Second))
		}
	case "pause":
		if b, ok := ev.Data.(bool); ok {
			m.Paused = b
		}
	case player.EndFile:
		// Only a track running out advances the list. Loading a replacement
		// ends the previous file too, and advancing on that would run away
		// through the playlist.
		//
		// This is deliberately not the eof-reached property: mpv unloads the
		// file at the same moment, so the property goes unavailable rather
		// than true and nothing ever fires.
		if reason, _ := ev.Data.(string); reason == "eof" {
			if next, ok := m.following(); ok {
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
				return m, batch(m.startLoading(), m.runSearch(m.Query))
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
		if t, ok := m.SelectedTrack(); ok {
			return m.start(t)
		}

	case " ", "space":
		return m.press(controlPlayPause)

	case "n":
		return m.press(controlNext)
	case "p":
		return m.press(controlPrevious)
	case "r":
		return m.press(controlRepeat)

	case "left", "h", "shift+tab":
		return m.selectTab(m.tabCursor - 1)
	case "right", "l", "tab":
		return m.selectTab(m.tabCursor + 1)

	case "up", "k":
		m.moveCursor(-1)
		return m.schedulePrefetch()
	case "down", "j":
		m.moveCursor(1)
		return m.schedulePrefetch()

	case "pgup", "ctrl+u":
		m.moveCursor(-m.bodyHeight())
		return m.schedulePrefetch()
	case "pgdown", "ctrl+d":
		m.moveCursor(m.bodyHeight())
		return m.schedulePrefetch()

	case "home", "g":
		m.moveCursor(-len(m.Tracks))
		return m.schedulePrefetch()
	case "end", "G":
		m.moveCursor(len(m.Tracks))
		return m.schedulePrefetch()

	case "+", "=":
		return m.applyRating(RatingUp)
	case "-", "_":
		return m.applyRating(RatingDown)
	}
	return m, nil
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

// setRating puts a rating on the row and on the playing track, which are
// not always the same object.
func (m *Model) setRating(videoID string, r Rating) {
	if m.playing.VideoID == videoID {
		m.playing.Rating = r
	}
	for i := range m.Tracks {
		if m.Tracks[i].VideoID == videoID {
			m.Tracks[i].Rating = r
			return
		}
	}
}

// applyRating toggles the thumbs state of the highlighted track: rating it
// the same way twice clears it, which is what the YouTube Music API does.
//
// The row changes immediately and the server is told afterwards. Waiting for
// the round trip would make a keystroke feel like a network call; if it
// fails, the message handler puts the row back.
func (m Model) applyRating(r Rating) (tea.Model, tea.Cmd) {
	if m.trackCursor >= len(m.Tracks) {
		return m, nil
	}
	videoID := m.Tracks[m.trackCursor].VideoID
	previous := m.Tracks[m.trackCursor].Rating
	if previous == r {
		r = RatingNone
	}
	m.setRating(videoID, r)
	return m, m.rate(videoID, r, previous)
}

// --------------------------------------------------------------- view ----

// Every colour in the interface comes from these three, so recolouring it
// is one edit rather than a search.
//
// They are named palette entries, not indices into the 256-colour cube:
// 0-15 are the terminal's own scheme, and anything above that is a fixed
// table that ignores it.
var (
	accent = lipgloss.Blue
	// accentBright is the same hue, one slot up, which is how a sixteen
	// colour palette does emphasis.
	accentBright = lipgloss.BrightBlue
	// muted is grey rather than blue on purpose: it is what the accent has
	// to stand out against.
	muted = lipgloss.BrightBlack
	// alert is the one thing that is not the accent. An error announcing
	// itself by colour is the point of colouring it.
	alert = lipgloss.Red
)

var (
	dim      = lipgloss.NewStyle().Foreground(muted)
	failed   = lipgloss.NewStyle().Foreground(alert)
	selected = lipgloss.NewStyle().Bold(true).Foreground(accent)
	active   = lipgloss.NewStyle().Foreground(accent)
)

// tabBorder is a rounded box whose bottom edge is open on the tab in front,
// so it reads as joined to the table below it.
func tabBorder(left, middle, right string) lipgloss.Border {
	b := lipgloss.RoundedBorder()
	b.BottomLeft, b.Bottom, b.BottomRight = left, middle, right
	return b
}

var (
	inactiveTabStyle = lipgloss.NewStyle().
				Border(tabBorder("┴", "─", "┴"), true).
				BorderForeground(muted).
				Foreground(muted).
				Padding(0, 1)
	activeTabStyle = inactiveTabStyle.
			Border(tabBorder("┘", " ", "└"), true).
			BorderForeground(accent).
			Foreground(accent).
			Bold(true)
	// The gap is the rule that carries on past the last tab. It inherits the
	// tab's padding unless that is cleared, which would push the row two
	// cells past the terminal.
	tabGapStyle = inactiveTabStyle.
			BorderTop(false).BorderLeft(false).BorderRight(false).
			Padding(0, 0)
)

const (
	tabsHeight   = 3 // border, label, border
	progressRows = 5 // blank, title, bar, controls, blank
	maxTabTitle  = 18
	tabFurniture = 4 // a border and a space either side
)

// bodyHeight is how many rows the track table gets.
func (m Model) bodyHeight() int {
	if h := m.height - tabsHeight - progressRows; h > 1 {
		return h
	}
	return 1
}

// barRow is the line the progress bar is drawn on.
func (m Model) barRow() int { return tabsHeight + m.bodyHeight() + 2 }

// controlsRow is the line of buttons under the bar.
func (m Model) controlsRow() int { return m.barRow() + 1 }

// barGeometry is the column the progress bar starts at and how wide it is.
// Rendering and hit-testing both go through this, so a click lands where the
// bar appears to be. The bar is the whole row: nothing flanks it.
func (m Model) barGeometry() (start, width int) {
	if m.width < 4 {
		return 0, 4
	}
	return 0, m.width
}

// fraction is how far through the track the position is.
func (m Model) fraction() float64 {
	if m.Length <= 0 {
		return 0
	}
	return float64(m.Position) / float64(m.Length)
}

func (m Model) View() tea.View {
	if m.width == 0 {
		// Before the first resize there is nothing to draw, but the terminal
		// modes still have to be declared or the frame turns them back off.
		v := tea.NewView("")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		m.renderTabs(),
		m.renderTracks(m.width, m.bodyHeight()),
		m.renderProgress(),
	)

	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "youtuimusic"
	// Cell motion reports drags, which is what scrubbing the bar needs.
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// tabSpan is where a tab sits on the row, for hit-testing.
type tabSpan struct {
	index      int
	start, end int // half open
}

func tabWidth(title string) int {
	return lipgloss.Width(truncate(title, maxTabTitle)) + tabFurniture
}

// tabSpans lays out the tabs that fit, always including the one in front.
// Rendering and hit-testing share it, so a click lands on the tab it looks
// like it should.
func (m Model) tabSpans() []tabSpan {
	count := m.tabCount()
	if count == 0 || m.width <= 0 {
		return nil
	}
	// Walk back from the selected tab until the row is full, then forward.
	first := clamp(m.tabCursor, count)
	used := tabWidth(m.tabAt(first).Title)
	for i := first - 1; i >= 0; i-- {
		w := tabWidth(m.tabAt(i).Title)
		if used+w > m.width {
			break
		}
		used, first = used+w, i
	}
	spans := []tabSpan{}
	at := 0
	for i := first; i < count; i++ {
		w := tabWidth(m.tabAt(i).Title)
		if at+w > m.width {
			break
		}
		spans = append(spans, tabSpan{index: i, start: at, end: at + w})
		at += w
	}
	return spans
}

func (m Model) renderTabs() string {
	spans := m.tabSpans()
	if len(spans) == 0 {
		// With no tabs the row still has to be exactly as tall, or
		// everything below it moves up and the mouse lands on the wrong
		// thing. Two empty lines and the rule the tabs would have sat on.
		return "\n\n" + dim.Render(strings.Repeat("─", max(0, m.width)))
	}
	rendered := make([]string, 0, len(spans))
	for _, s := range spans {
		style := inactiveTabStyle
		if s.index == m.tabCursor {
			style = activeTabStyle
		}
		rendered = append(rendered, style.Render(truncate(m.tabAt(s.index).Title, maxTabTitle)))
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
	gap := tabGapStyle.Render(strings.Repeat(" ", max(0, m.width-lipgloss.Width(row))))
	return lipgloss.JoinHorizontal(lipgloss.Bottom, row, gap)
}

func (m Model) renderTracks(width, height int) string {
	var b strings.Builder
	for i := 0; i < height; i++ {
		row := i + m.trackOffset
		line := ""
		if row < len(m.Tracks) {
			line = truncate(m.trackLine(m.Tracks[row], width), width)
			if row == m.trackCursor {
				line = selected.Render(line)
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
	const durCol, rateCol = 6, 2
	rest := max(width-durCol-rateCol-2, 10)
	titleW := rest / 2
	artistW := rest - titleW
	return fmt.Sprintf("%s %-*s %-*s %*s",
		t.Rating.glyph(),
		titleW, truncate(t.Title, titleW),
		artistW, truncate(t.Artist, artistW),
		durCol, formatDuration(t.Duration))
}

// statusLine is the one line above the bar. Everything on it is cut to the
// width: a long track title would otherwise push the frame wider than the
// terminal and take every other row with it.
func (m Model) statusLine() string {
	const pausedNote = "  paused"
	switch {
	case m.Searching:
		return truncate("/"+m.Query+"█", m.width)
	case m.Err != nil:
		return failed.Render(truncate(m.Err.Error(), m.width))
	case m.loading:
		return m.spin.View() + " " + dim.Render(truncate("Loading…", max(0, m.width-2)))
	case m.NowPlaying != "" && m.Paused:
		return truncate(m.NowPlaying, max(0, m.width-len(pausedNote))) + dim.Render(pausedNote)
	case m.NowPlaying != "":
		return truncate(m.NowPlaying, m.width)
	default:
		return dim.Render(truncate("Ready", m.width))
	}
}

func (m Model) renderProgress() string {
	return "\n" + m.statusLine() + "\n" + m.bar.ViewAs(m.fraction()) + "\n" +
		m.renderControls() + "\n"
}

// barRamp is the bar's gradient, as ANSI palette entries. Naming the
// palette rather than a hex value is what keeps the bar inside the
// terminal's own colour scheme: the terminal resolves these, so they are
// whatever the user's theme says they are.
//
// The component's own blend cannot be used for this. It interpolates in RGB
// through lipgloss.Blend1D, which has to invent concrete values for the
// steps in between and emits them as true colour — off-scheme by
// construction, however the endpoints were named.
var barRamp = []color.Color{
	accent,
	accentBright,
}

// rampAt picks the ramp entry for a position along the bar. Sixteen colours
// cannot make a smooth blend, so this steps rather than fades; the half
// block softens it, carrying a foreground and a background so each cell can
// show two steps.
//
// The ramp is laid along the track rather than squeezed into the played
// part, so a colour means a place in the song and stays put as it plays.
func rampAt(_, position float64) color.Color {
	i := int(position * float64(len(barRamp)))
	return barRamp[min(max(i, 0), len(barRamp)-1)]
}

func newBar() progress.Model {
	bar := progress.New(
		progress.WithoutPercentage(),
		progress.WithColorFunc(rampAt),
	)
	// The default is a fixed grey, which is off-scheme like the rest.
	bar.EmptyColor = muted
	return bar
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
