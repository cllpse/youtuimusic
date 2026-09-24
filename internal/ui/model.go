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

// glyph marks a rated row with the same icon the control below it uses, so
// the two cannot be read as different things.
func (r Rating) glyph() string {
	switch r {
	case RatingUp:
		return iconThumbUp
	case RatingDown:
		return iconThumbDown
	default:
		return " "
	}
}

// tabKind says what a tab holds, which is what decides how to fetch it.
type tabKind int

const (
	tabPlaylist tabKind = iota
	tabSearch
	tabAlbum
	tabArtist
)

// Playlist is one tab.
type Playlist struct {
	ID    string
	Title string
	kind  tabKind
}

// Track is one row in the table.
type Track struct {
	VideoID  string
	Title    string
	Artist   string
	Duration time.Duration
	Rating   Rating
	// Album names the tab the menu opens; AlbumID and ArtistID are where
	// its "go to" rows lead, and are empty when a row leads nowhere.
	Album    string
	AlbumID  string
	ArtistID string
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

	// Playback state, fed from player events. What is playing is held as a
	// track rather than a rendered line, so the status can show its parts
	// differently.
	Position time.Duration
	Length   time.Duration
	Paused   bool

	// Search
	Searching bool
	Query     string
	// extra is the one tab that is not a playlist — search results, an
	// album, an artist. Opening another replaces it rather than adding to
	// the row.
	extra Playlist

	menu   trackMenu
	detour detour

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
	draggingScroll  bool
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
	// position is drawn where it is. The paused one is the same bar in grey,
	// built once rather than recoloured on every frame.
	bar       progress.Model
	pausedBar progress.Model
	spin      spinner.Model

	Err error
}

// New returns a Model with nothing loaded. A zero Services makes a model
// that talks to nothing, which is what the view tests use.
func New(s Services) Model {
	return Model{
		services:  s,
		loading:   s.Library != nil,
		now:       time.Now,
		cache:     map[string][]Track{},
		bar:       newBar(rampAt),
		pausedBar: newBar(mutedRamp),
		spin:      spinner.New(spinner.WithSpinner(spinner.Pulse), spinner.WithStyle(active)),
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

// detour is an album or an artist, shown in a popover over the main view.
// It keeps its own list, because what is underneath is still there and must
// not be disturbed.
type detour struct {
	active bool
	tab    Playlist
	tracks []Track
	cursor int
	offset int
}

// currentTab is what a fetch in flight belongs to: the popover when one is
// open, the tab in front otherwise.
func (m Model) currentTab() Playlist {
	if m.detour.active {
		return m.detour.tab
	}
	return m.tabAt(m.tabCursor)
}

// enterDetour opens the popover on an album or an artist, replacing
// whatever it was showing.
func (m Model) enterDetour(tab Playlist) (Model, tea.Cmd) {
	if tab.ID == "" {
		return m, nil
	}
	m.menu = trackMenu{}
	m.detour = detour{active: true, tab: tab}

	if tracks, ok := m.cache[tab.ID]; ok {
		m.detour.tracks = tracks
		if len(tracks) > 0 {
			return m, m.prefetch(tracks[0].VideoID)
		}
		return m, nil
	}
	return m, batch(m.startLoading(), m.scheduleTabLoad())
}

// leaveDetour closes it. Nothing has to be put back: the view underneath was
// never touched.
func (m Model) leaveDetour() (Model, tea.Cmd) {
	m.detour = detour{}
	m.menu = trackMenu{}
	return m, nil
}

// selectedDetourTrack is the row under the popover's cursor.
func (m Model) selectedDetourTrack() (Track, bool) {
	if m.detour.cursor < 0 || m.detour.cursor >= len(m.detour.tracks) {
		return Track{}, false
	}
	return m.detour.tracks[m.detour.cursor], true
}

// moveDetour moves the popover's cursor and scrolls to keep it in view.
func (m *Model) moveDetour(delta int) {
	height := m.modalListHeight()
	m.detour.cursor = clamp(m.detour.cursor+delta, len(m.detour.tracks))
	if m.detour.cursor < m.detour.offset {
		m.detour.offset = m.detour.cursor
	}
	if m.detour.cursor >= m.detour.offset+height {
		m.detour.offset = m.detour.cursor - height + 1
	}
	m.detour.offset = min(m.detour.offset, max(0, len(m.detour.tracks)-height))
	m.detour.offset = max(m.detour.offset, 0)
}

// tabCount is the playlists plus the search results, when there are any.
func (m Model) tabCount() int {
	if m.extra.ID != "" {
		return len(m.Playlists) + 1
	}
	return len(m.Playlists)
}

func (m Model) tabAt(i int) Playlist {
	if i >= 0 && i < len(m.Playlists) {
		return m.Playlists[i]
	}
	if i == len(m.Playlists) && m.extra.ID != "" {
		return m.extra
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
		m.pausedBar.SetWidth(barWidth)
		m.scroll()
		return m, nil

	case tea.KeyPressMsg:
		if m.menu.open {
			return m.handleMenuKey(msg.String())
		}
		if m.detour.active {
			if next, cmd, handled := m.handleModalKey(msg.String()); handled {
				return next, cmd
			}
		}
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
		tab := m.currentTab()
		// Search results arrive with the search; there is nothing to fetch.
		if tab.ID == "" || tab.kind == tabSearch {
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
		// A menu is anchored to a row of the list being replaced.
		m.menu = trackMenu{}
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
		if m.detour.active && m.detour.tab.ID == msg.id {
			m.detour.tracks = tracks
			m.detour.cursor, m.detour.offset = 0, 0
			m.loading, m.Err = false, nil
			if len(tracks) > 0 {
				return m, m.prefetch(tracks[0].VideoID)
			}
			return m, nil
		}
		if m.currentTab().ID != msg.id {
			return m, nil // the view moved on while this was in flight
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
		m.extra = Playlist{ID: searchTabID, Title: msg.query, kind: tabSearch}
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
		m.Length = msg.length
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
	// The same track can be in the list, in the popover over it, or both.
	for _, rows := range [][]Track{m.Tracks, m.detour.tracks} {
		for i := range rows {
			if rows[i].VideoID == videoID {
				rows[i].Rating = r
			}
		}
	}
	if m.menu.track.VideoID == videoID {
		m.menu.track.Rating = r
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
	dim    = lipgloss.NewStyle().Foreground(muted)
	failed = lipgloss.NewStyle().Foreground(alert)
	active = lipgloss.NewStyle().Foreground(accent)
)

// A row carries two independent things: whether it is the track playing,
// and whether it is the one under the cursor. Colour says the first and a
// filled background says the second, so a row can say both at once — which
// it has to, since the cursor is usually on the track that is playing.
var (
	rowPlaying  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	rowSelected = lipgloss.NewStyle().Background(muted)
	rowBoth     = lipgloss.NewStyle().Bold(true).Foreground(accent).Background(muted)
)

// rowStyle picks how a row is drawn, and reports whether it is styled at
// all. An unstyled row mutes its own columns; a styled one must not, since
// grey on a filled background is nothing.
func rowStyle(playing, selected bool) (lipgloss.Style, bool) {
	switch {
	case playing && selected:
		return rowBoth, true
	case playing:
		return rowPlaying, true
	case selected:
		return rowSelected, true
	}
	return lipgloss.Style{}, false
}

// isPlaying reports whether a row is the track mpv is on.
func (m Model) isPlaying(t Track) bool {
	return m.playing.VideoID != "" && t.VideoID == m.playing.VideoID
}

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
	progressRows = 7 // border, title, blank, bar, blank, controls, border
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

// barRow is the line the progress bar is drawn on: past the list, the box's
// own border, the title and the blank line under it.
func (m Model) barRow() int { return tabsHeight + m.bodyHeight() + 3 }

// controlsRow is the line of buttons, a blank line below the bar.
func (m Model) controlsRow() int { return m.barRow() + 2 }

// barGeometry is the column the progress bar starts at and how wide it is.
// Rendering and hit-testing both go through this, so a click lands where the
// bar appears to be. The bar is the whole row: nothing flanks it.
func (m Model) barGeometry() (start, width int) {
	// Never wider than the box: a floor here would push the border out and
	// take the whole frame with it.
	return contentLeft, m.contentWidth()
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
		m.renderPlayer(),
	)
	// Anything floating sits over the frame rather than in it, so opening
	// one reflows nothing underneath.
	//
	// This goes through a compositor rather than composing layers onto a
	// canvas directly: a layer's own Draw ignores its position, and only
	// the compositor works out where each one belongs.
	layers := []*lipgloss.Layer{lipgloss.NewLayer(content)}
	if m.detour.active {
		x, y, _, _ := m.modalBounds()
		layers = append(layers, lipgloss.NewLayer(m.renderModal()).X(x).Y(y).Z(1))
	}
	if m.menu.open {
		layers = append(layers,
			lipgloss.NewLayer(m.renderMenu()).X(m.menu.x).Y(m.menu.y).Z(2))
	}
	if len(layers) > 1 {
		content = lipgloss.NewCompositor(layers...).Render()
	}

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
	// The wait belongs where it is happening, not down in the status line.
	if m.loading && len(m.Tracks) == 0 {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
			m.spin.View()+" "+dim.Render("Loading…"))
	}

	bar := m.scrollbar(height)
	if bar != nil {
		width -= scrollbarWidth
	}

	var b strings.Builder
	for i := 0; i < height; i++ {
		row := i + m.trackOffset
		line := ""
		if row < len(m.Tracks) {
			t := m.Tracks[row]
			style, styled := rowStyle(m.isPlaying(t), row == m.trackCursor)
			line = m.trackLine(t, width, m.showsRating(), styled)
			if styled {
				line = style.Render(line)
			}
		}
		b.WriteString(pad(line, width))
		if bar != nil {
			b.WriteString(bar[i])
		}
		if i < height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// hasScrollbar reports whether the list is longer than the window. The
// column only exists when it has something to say, so a list that fits is
// not made narrower for nothing.
func (m Model) hasScrollbar() bool {
	return len(m.Tracks) > m.bodyHeight()
}

// scrollbarWidth is the bar itself plus a blank column to its right, so it
// does not sit against the edge of the terminal.
const scrollbarWidth = 2

// scrollbarColumn is where it is drawn.
func (m Model) scrollbarColumn() int { return m.width - scrollbarWidth }

// scrollbar returns the column down the right of the list, or nil when
// everything fits. The thumb is the same grey as the trough: the glyphs
// carry the difference, as they do on the progress bar.
func (m Model) scrollbar(height int) []string {
	return scrollbarFor(len(m.Tracks), m.trackOffset, height)
}

// scrollbarFor draws a trough and thumb for any list.
func scrollbarFor(total, offset, height int) []string {
	if height <= 0 || total <= height {
		return nil
	}
	thumb := max(1, height*height/total)
	start := 0
	if span, furthest := height-thumb, total-height; furthest > 0 {
		start = min(offset*span/furthest, span)
	}

	out := make([]string, height)
	for i := range out {
		if i >= start && i < start+thumb {
			out[i] = dim.Render("█") + " "
			continue
		}
		out[i] = dim.Render("│") + " "
	}
	return out
}

// scrollTo puts the list where a point on the scrollbar says it should be.
func (m *Model) scrollTo(y int) {
	height := m.bodyHeight()
	total := len(m.Tracks)
	if height <= 1 || total <= height {
		return
	}
	row := min(max(y-tabsHeight, 0), height-1)
	furthest := total - height
	m.trackOffset = min(max(row*furthest/(height-1), 0), furthest)
}

// trackLine draws one row. The leading column is the same two cells whether
// or not it holds a rating, so the titles line up across tabs.
//
// Everything but the title is muted, which leaves the eye one thing to read
// down. A highlighted row is drawn plain and coloured whole by the caller —
// dimming part of it would fight the highlight.
func (m Model) trackLine(t Track, width int, showRating, highlighted bool) string {
	const durCol, rateCol = 6, 2
	prefix := "  "
	if showRating {
		prefix = t.Rating.glyph() + " "
	}
	rest := width - durCol - rateCol - 2
	if rest < 4 {
		// No room for columns; the title is the only thing worth keeping.
		return pad(truncate(prefix+t.Title, width), width)
	}
	titleW := rest / 2
	artistW := rest - titleW

	artist := pad(truncate(t.Artist, artistW), artistW)
	duration := pad(formatDuration(t.Duration), durCol)
	if !highlighted {
		artist, duration = dim.Render(artist), dim.Render(duration)
	}
	return prefix + pad(truncate(t.Title, titleW), titleW) + " " + artist + " " + duration
}

// showsRating is false on the liked playlist, where every row is liked and
// the column would say the same thing all the way down.
func (m Model) showsRating() bool { return m.showingID != likedPlaylistID }

// statusLine is the one line above the bar. Everything on it is cut to the
// width: a long track title would otherwise push the frame wider than the
// terminal and take every other row with it.
//
// Nothing here says "paused". The bar goes grey and the control becomes a
// play triangle, which is two ways of saying it already.
func (m Model) statusLine() string {
	switch {
	case m.Searching:
		return truncate("/"+m.Query+"█", m.contentWidth())
	case m.Err != nil:
		return failed.Render(truncate(m.Err.Error(), m.contentWidth()))
	case m.playing.VideoID != "":
		return m.nowPlayingLine()
	default:
		return dim.Render(truncate("Ready", m.contentWidth()))
	}
}

// nowPlayingLine is the track's title, with its album muted behind it.
func (m Model) nowPlayingLine() string {
	width := m.contentWidth()
	title := truncate(m.playing.Title, width)
	room := width - lipgloss.Width(title) - 2
	if m.playing.Album == "" || room < 4 {
		return title
	}
	return title + "  " + dim.Render(truncate(m.playing.Album, room))
}

// playerBox is the frame around the title, the bar and the controls. Its
// border replaces the blank lines that used to separate them from the list,
// so it costs no height.
var playerBox = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(muted).
	Padding(0, playerPadding)

const (
	playerBorder  = 1
	playerPadding = 1
	// contentLeft is the first column inside the box, and contentWidth what
	// is left of the row once both sides are taken.
	contentLeft = playerBorder + playerPadding
)

func (m Model) contentWidth() int { return max(0, m.width-2*contentLeft) }

func (m Model) renderPlayer() string {
	// The bar is given air either side rather than being wedged between the
	// title and the buttons.
	blank := strings.Repeat(" ", m.contentWidth())
	inner := lipgloss.JoinVertical(lipgloss.Left,
		pad(m.statusLine(), m.contentWidth()),
		blank,
		m.renderBar(),
		blank,
		m.renderControls(),
	)
	return playerBox.Render(inner)
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

// mutedRamp greys the played part out. It lands on the same colour as the
// unplayed part, which is the point: paused, the bar keeps its shape but
// stops being the one lit thing on the screen. The two halves stay legible
// because the characters differ — a solid block against a light shade.
func mutedRamp(_, _ float64) color.Color { return muted }

func newBar(fill progress.ColorFunc) progress.Model {
	bar := progress.New(
		progress.WithoutPercentage(),
		progress.WithColorFunc(fill),
	)
	// The default is a fixed grey, which is off-scheme like the rest.
	bar.EmptyColor = muted
	return bar
}

// renderBar draws the position, greyed out while playback is paused.
func (m Model) renderBar() string {
	if m.Paused {
		return m.pausedBar.ViewAs(m.fraction())
	}
	return m.bar.ViewAs(m.fraction())
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
