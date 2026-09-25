// Package ui is the bubbletea layer: a row of playlist tabs, a table of
// tracks, and a progress bar.
//
// Rendering is a pure function of Model. Nothing in here reaches out to the
// network or the player directly — side effects happen in tea.Cmds, so the
// view can always be rendered from state alone and tested without a terminal.
package ui

import (
	"image/color"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cllpse/youtuimusic/internal/player"
	"github.com/cllpse/youtuimusic/internal/state"
	"github.com/cllpse/youtuimusic/internal/ytm"
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

// Track is one row in the table. A release is a Track too: an artist's page
// lists albums beside songs, and there is nothing to play in an album.
//
// glyph is the mark in the leading column — what a release is, or how a
// track is rated.
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

// isRelease reports whether a row is an album rather than a song: it has
// somewhere to go and nothing to play.
func (t Track) isRelease() bool { return t.VideoID == "" && t.AlbumID != "" }

func (t Track) glyph() string {
	if t.isRelease() {
		return iconAlbum
	}
	return t.Rating.glyph()
}

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

	menu   trackMenu
	detour detour
	// history is the popovers behind the one in front. Going to an album
	// from an artist comes back to the artist rather than to nothing.
	history []detour

	// playing is the track mpv is on, held whole rather than by id so the
	// controls can still show and rate it after another tab is opened.
	playing Track
	repeat  Repeat
	loading bool

	// highlight is the selected row's fill, asked of the terminal at
	// startup. Nil until it answers, and some never do.
	highlight color.Color

	// restoring is what the last session was playing, held until the
	// pieces it names exist: the library for the tab, that tab's listing
	// for the track. Nil once there is nothing left to put back.
	restoring *state.State

	// cache is what each tab has fetched, so going back to one is instant.
	// It holds where the listing carries on as well as its rows: without
	// that, revisiting a tab lost the thread and could not page further.
	cache map[string]cached
	// arrival is the visible listing in the order it came in. Tracks is
	// that order sorted, so that clearing a sort puts it back.
	arrival []Track
	// autoPages counts what sorting has fetched on its own, so a server
	// that never runs out cannot spin here forever.
	autoPages int
	// showingID is the tab the visible list came from, which tells a refetch
	// of the same tab from a move to another one.
	showingID string
	// more is where the visible list carries on, and loadingMore is whether
	// the next page is already on its way. Only one list asks at a time.
	more        ytm.Continuation
	loadingMore bool
	// sort is the order both lists are in. Sorting is applied to the slice
	// rather than to the drawing, so that a cursor means the same row
	// everywhere.
	sort sortSpec
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
		cache:     map[string]cached{},
		bar:       newBar(litRamp),
		pausedBar: newBar(mutedRamp),
		spin:      newLoader(),
	}
}

// Init starts the first fetch and opens the stream of player events.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.fetchPlaylists(), m.watchEvents(), tea.RequestBackgroundColor}
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

// cached is a listing as far as it has been read.
type cached struct {
	tracks []Track
	next   ytm.Continuation
}

// detour is an album or an artist, shown in a popover over the main view.
// It keeps its own list, because what is underneath is still there and must
// not be disturbed.
type detour struct {
	active  bool
	tab     Playlist
	tracks  []Track
	arrival []Track
	cursor  int
	offset  int
	more    ytm.Continuation

	// A search popover carries its own input. typing is whether keys go to
	// it rather than to the list below.
	query  string
	typing bool
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
	if m.detour.active {
		// Copied rather than appended in place: the model is passed around
		// by value, and a shared backing array would let one copy write
		// over another's history.
		m.history = append(append([]detour(nil), m.history...), m.detour)
	}
	m.detour = detour{active: true, tab: tab}

	if entry, ok := m.cache[tab.ID]; ok {
		m.detour.arrival, m.detour.more = entry.tracks, entry.next
		m.detour.tracks = entry.tracks
		if len(m.detour.tracks) > 0 {
			return m, m.prefetch(m.detour.tracks[0].VideoID)
		}
		return m, nil
	}
	return m, batch(m.startLoading(), m.scheduleTabLoad())
}

// leaveDetour steps back one popover, closing when there is nothing behind
// this one. Nothing has to be put back: the view underneath was never
// touched.
func (m Model) leaveDetour() (Model, tea.Cmd) {
	m.menu = trackMenu{}
	if n := len(m.history); n > 0 {
		m.detour = m.history[n-1]
		m.history = m.history[: n-1 : n-1]
		return m, nil
	}
	m.detour = detour{}
	return m, nil
}

// closeDetour dismisses the popover and everything behind it, which is what
// clicking away from one means.
func (m Model) closeDetour() (Model, tea.Cmd) {
	m.detour, m.history, m.menu = detour{}, nil, trackMenu{}
	return m, nil
}

// selectedDetourTrack is the row under the popover's cursor.
func (m Model) selectedDetourTrack() (Track, bool) {
	if m.detour.cursor < 0 || m.detour.cursor >= len(m.detour.tracks) {
		return Track{}, false
	}
	return m.detour.tracks[m.detour.cursor], true
}

// detourRowCount is the popover's tracks plus its offer of another page.
func (m Model) detourRowCount() int {
	if m.detour.more.More() {
		return len(m.detour.tracks) + 1
	}
	return len(m.detour.tracks)
}

// moveDetour moves the popover's cursor and scrolls to keep it in view.
func (m *Model) moveDetour(delta int) {
	m.detour.cursor = clamp(m.detour.cursor+delta, m.detourRowCount())
	m.detour.offset = keepVisible(m.detour.cursor, m.detour.offset,
		m.modalRowsHeight(), m.detourRowCount())
}

// afterDetourMove takes up the offer of another page when the cursor
// reaches it, the same way the list underneath does.
func (m Model) afterDetourMove() (tea.Model, tea.Cmd, bool) {
	if m.detour.more.More() && m.detour.cursor == len(m.detour.tracks) {
		next, cmd := m.fetchMore(true)
		return next, cmd, true
	}
	return m, nil, true
}

// tabCount is the playlists. Nothing else lives in the row: a search, an
// album and an artist are all popovers.
func (m Model) tabCount() int { return len(m.Playlists) }

func (m Model) tabAt(i int) Playlist {
	if i >= 0 && i < len(m.Playlists) {
		return m.Playlists[i]
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
	// An order was asked for about one listing, so it does not follow the
	// reader to the next: arriving at a playlist shows the playlist's own
	// order, which is how it reads in the app it came from.
	m.sort, m.autoPages = sortSpec{}, 0
	tab := m.tabAt(m.tabCursor)

	if entry, ok := m.cache[tab.ID]; ok {
		m.arrival, m.more = entry.tracks, entry.next
		m.Tracks, m.loading, m.Err = sorted(entry.tracks, m.sort), false, nil
		m.showingID = tab.ID
		m.restorePlaying(tab.ID)
		if len(m.Tracks) > 0 {
			return m, m.prefetch(m.Tracks[0].VideoID)
		}
		return m, nil
	}
	m.Tracks, m.arrival = nil, nil
	m.showingID, m.more = "", ytm.Continuation{}
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

// rowCount is the tracks plus the row that offers the next page.
func (m Model) rowCount() int {
	if m.more.More() {
		return len(m.Tracks) + 1
	}
	return len(m.Tracks)
}

// atMoreRow reports whether the cursor is sitting on the offer of another
// page.
func (m Model) atMoreRow() bool {
	return m.more.More() && m.trackCursor == len(m.Tracks)
}

// viewingMoreRow reports whether the offer of another page is on screen.
func (m Model) viewingMoreRow() bool {
	return m.more.More() && m.trackOffset+m.listHeight() > len(m.Tracks)
}

// afterCursorMove warms the row the cursor landed on — or, when that row is
// the offer of another page, takes it up. Reaching the end of a list is the
// same gesture as asking for more of it.
func (m Model) afterCursorMove() (tea.Model, tea.Cmd) {
	if m.atMoreRow() {
		return m.fetchMore(false)
	}
	return m.schedulePrefetch()
}

// fetchMore asks for the next page of a list, unless one is already coming.
func (m Model) fetchMore(inDetour bool) (tea.Model, tea.Cmd) {
	if m.loadingMore {
		return m, nil
	}
	from := m.more
	if inDetour {
		from = m.detour.more
	}
	if !from.More() {
		return m, nil
	}
	m.loadingMore = true
	return m, m.loadMore(from, inDetour)
}

// moveCursor moves the track cursor and scrolls to keep it in view.
func (m *Model) moveCursor(delta int) {
	m.trackCursor = clamp(m.trackCursor+delta, m.rowCount())
	m.scroll()
}

// scrollBy moves the window and leaves the selection where it is, so the
// list can be looked through without losing the cursor's place.
func (m *Model) scrollBy(delta int) {
	m.trackOffset = clampOffset(m.trackOffset+delta, m.listHeight(), m.rowCount())
}

// scroll moves the window only far enough to keep the cursor on screen.
func (m *Model) scroll() {
	m.trackOffset = keepVisible(m.trackCursor, m.trackOffset, m.listHeight(), m.rowCount())
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
	case tea.BackgroundColorMsg:
		// A tint of the page rather than an entry from the scheme. The
		// sixteen colours have exactly one grey for a highlight, and a
		// light theme has to spend it on being a shade of the background:
		// #BDBDBD on a #FFFFFF page is a band, not a highlight. Nudging the
		// terminal's own background toward its foreground gives a lighter
		// mark than the scheme can name, and one that follows the theme.
		// A terminal that does not know its own background can answer with
		// nothing, and a tint of nothing is nothing: the row would be
		// styled and invisible. Keep the fallback instead.
		if msg.Color == nil {
			return m, nil
		}
		if _, _, _, alpha := msg.RGBA(); alpha == 0 {
			return m, nil
		}
		if msg.IsDark() {
			m.highlight = lipgloss.Lighten(msg, highlightTint)
		} else {
			m.highlight = lipgloss.Darken(msg, highlightTint)
		}
		return m, nil

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
		m.restoreTab()
		if m.tabCount() == 0 {
			m.loading = false
			return m, nil
		}
		return m.showTab()

	case moreMsg:
		m.loadingMore = false
		if msg.err != nil {
			m.Err = msg.err
			return m, nil
		}
		if msg.inDetour {
			m.detour.arrival = append(m.detour.arrival, fromAPI(msg.page.Tracks)...)
			m.detour.more = msg.page.Next
			// The popover shows its arrival order, so the new page is
			// what it shows. applySort does not reach it any more.
			m.detour.tracks = m.detour.arrival
			m.cache[m.detour.tab.ID] = cached{m.detour.arrival, m.detour.more}
		} else {
			m.arrival = append(m.arrival, fromAPI(msg.page.Tracks)...)
			m.more = msg.page.Next
			if m.showingID != "" {
				m.cache[m.showingID] = cached{m.arrival, m.more}
			}
		}
		m.applySort()
		// A sort over half a list is not the order, so it keeps going.
		return m, m.continueSort()

	case tracksMsg:
		// A menu is anchored to a row of the list being replaced.
		m.menu = trackMenu{}
		tracks := fromAPI(msg.page.Tracks)
		// The server can still describe a just-rated track the old way, so
		// what this app did wins over what the list says.
		if m.lastRated.videoID != "" {
			for i := range tracks {
				if tracks[i].VideoID == m.lastRated.videoID {
					tracks[i].Rating = m.lastRated.rating
				}
			}
		}
		m.cache[msg.id] = cached{tracks, msg.page.Next}
		if m.detour.active && m.detour.tab.ID == msg.id {
			m.detour.arrival, m.detour.more = tracks, msg.page.Next
			m.detour.tracks = tracks
			m.detour.cursor, m.detour.offset = 0, 0
			m.loading, m.Err = false, nil
			if len(m.detour.tracks) > 0 {
				return m, batch(m.prefetch(m.detour.tracks[0].VideoID), m.continueSort())
			}
			return m, m.continueSort()
		}
		if m.currentTab().ID != msg.id {
			return m, nil // the view moved on while this was in flight
		}
		// A refetch of the list already on screen keeps the reader's place
		// in it; arriving at a new tab starts at the top.
		refresh := m.showingID == msg.id
		m.arrival, m.loading, m.Err = tracks, false, nil
		m.Tracks = sorted(tracks, m.sort)
		m.showingID, m.more = msg.id, msg.page.Next
		if refresh {
			m.trackCursor = clamp(m.trackCursor, len(m.Tracks))
			m.scroll()
			return m, m.continueSort()
		}
		m.trackCursor, m.trackOffset = 0, 0
		m.restorePlaying(msg.id)
		if len(m.Tracks) > 0 {
			return m, batch(m.prefetch(m.Tracks[0].VideoID), m.continueSort())
		}
		return m, m.continueSort()

	case searchMsg:
		// The popover may have been closed, or replaced by an album, while
		// this was in flight.
		if !m.detour.active || m.detour.tab.kind != tabSearch {
			return m, nil
		}
		m.detour.arrival, m.detour.more = fromAPI(msg.page.Tracks), msg.page.Next
		m.detour.tracks = m.detour.arrival
		m.detour.cursor, m.detour.offset = 0, 0
		m.loading, m.Err = false, nil
		if len(m.detour.tracks) > 0 {
			return m, batch(m.prefetch(m.detour.tracks[0].VideoID), m.continueSort())
		}
		return m, m.continueSort()

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

		if msg.applied != RatingUp {
			// Take the row out here rather than refetching. The server can
			// take a moment to agree, and a refetch that still listed the
			// track would put it straight back.
			m.dropFromLiked(msg.videoID)
			return m, nil
		}
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

	switch key {
	case "ctrl+c":
		m.record()
		return m, tea.Quit

	case "/":
		return m.openSearch()

	case "enter":
		if t, ok := m.SelectedTrack(); ok {
			return m.open(t)
		}

	case " ", "space":
		return m.press(controlPlayPause)

	case "s":
		if m.detour.active {
			return m, nil
		}
		return m.sortBy(m.sort.next())
	case "S":
		if m.detour.active {
			return m, nil
		}
		return m.sortBy(sortSpec{by: max(m.sort.by, sortTitle), desc: !m.sort.desc})

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
		return m.afterCursorMove()
	case "down", "j":
		m.moveCursor(1)
		return m.afterCursorMove()

	case "pgup", "ctrl+u":
		m.moveCursor(-m.listHeight())
		return m.afterCursorMove()
	case "pgdown", "ctrl+d":
		m.moveCursor(m.listHeight())
		return m.afterCursorMove()

	case "home", "g":
		m.moveCursor(-m.rowCount())
		return m.afterCursorMove()
	case "end", "G":
		m.moveCursor(m.rowCount())
		return m.afterCursorMove()

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
	// The same track can be in the list, in the popover over it, or both —
	// and in the order each arrived in, which is what a sort rebuilds from.
	for _, rows := range [][]Track{m.Tracks, m.arrival, m.detour.tracks, m.detour.arrival} {
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
// The interface is monochrome: one hue, and it is the terminal's own. There
// is no accent. Everything that has to stand out does it by weight —
// faint, ordinary, bright — or by being turned inside out, a fill of the
// foreground with the background as its text. Shape does the rest.
var (
	// alert is the one exception, and it earns it: an error announcing
	// itself by colour is the point of colouring it.
	alert = lipgloss.Red

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
	// onSurface is text on that surface. It cannot be muted, because muted
	// is the surface.
	onSurface = foreground
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
	failed = lipgloss.NewStyle().Foreground(alert)
	active = lipgloss.NewStyle().Foreground(emphasis).Bold(true)
)

// dropFromLiked removes a track from the liked playlist wherever it is on
// screen, which is what unliking it means there.
func (m *Model) dropFromLiked(videoID string) {
	if m.showingID == likedPlaylistID {
		m.arrival = without(m.arrival, videoID)
		m.Tracks = without(m.Tracks, videoID)
		m.trackCursor = clamp(m.trackCursor, len(m.Tracks))
		m.scroll()
	}
	if m.detour.active && m.detour.tab.ID == likedPlaylistID {
		m.detour.arrival = without(m.detour.arrival, videoID)
		m.detour.tracks = without(m.detour.tracks, videoID)
		m.detour.cursor = clamp(m.detour.cursor, len(m.detour.tracks))
	}
}

func without(tracks []Track, videoID string) []Track {
	out := make([]Track, 0, len(tracks))
	for _, t := range tracks {
		if t.VideoID != videoID {
			out = append(out, t)
		}
	}
	return out
}

// sortBy reorders both lists. The slices themselves are sorted, not the
// drawing of them, so that a cursor keeps meaning the same row.
func (m Model) sortBy(spec sortSpec) (tea.Model, tea.Cmd) {
	m.sort, m.autoPages = spec, 0
	m.applySort()
	m.trackCursor, m.trackOffset = 0, 0
	m.detour.cursor, m.detour.offset = 0, 0
	return m, m.continueSort()
}

// setTracks puts a listing on screen, keeping the order it came in so that
// a sort can be cleared again.
func (m *Model) setTracks(tracks []Track) {
	m.arrival = tracks
	m.Tracks = sorted(tracks, m.sort)
}

// setDetourTracks does the same for the popover, which keeps the order it
// arrived in because it cannot be sorted.
func (m *Model) setDetourTracks(tracks []Track) {
	m.detour.arrival, m.detour.tracks = tracks, tracks
}

// applySort rebuilds both lists from the order they arrived in. Sorting the
// arrival order rather than the last sorted one is what lets the sort be
// cleared: otherwise the order a listing came in is gone after the first
// sort, and there is nothing to put back.
func (m *Model) applySort() {
	// A list with no arrival order behind it has nothing to rebuild from,
	// and rebuilding anyway would empty it.
	if m.arrival != nil {
		m.Tracks = sorted(m.arrival, m.sort)
	}
}

// sorted is a copy of a listing in a given order.
func sorted(tracks []Track, spec sortSpec) []Track {
	out := slices.Clone(tracks)
	sortTracks(out, spec)
	return out
}

// maxAutoPages caps what a sort will fetch on its own. The server decides
// when a listing ends, and this decides what happens if it never does.
const maxAutoPages = 50

// continueSort fetches the rest of a listing while it is being sorted.
// Ordering half a list puts the wrong rows at the top, so an order is only
// true once everything is in — and asking for it is the only way to know.
func (m *Model) continueSort() tea.Cmd {
	// A popover cannot be sorted, so there is no order to complete there.
	if m.sort.by == sortNone || m.loadingMore || m.detour.active ||
		m.autoPages >= maxAutoPages {
		return nil
	}
	if !m.more.More() {
		return nil
	}
	m.autoPages++
	m.loadingMore = true
	return m.loadMore(m.more, false)
}

// isPlaying reports whether a row is the track mpv is on.
func (m Model) isPlaying(t Track) bool {
	return m.playing.VideoID != "" && t.VideoID == m.playing.VideoID
}

// tabBorder is a rounded box whose bottom edge is open on the tab in front,
// so it reads as joined to the table below it. Its corners are the light arc
// the rest of the frame uses, which is the tightest radius a character grid
// has — the Powerline half circles are a whole cell of curve and read as a
// pill rather than as a corner.
func tabBorder(left, middle, right string) lipgloss.Border {
	b := lipgloss.RoundedBorder()
	b.BottomLeft, b.Bottom, b.BottomRight = left, middle, right
	return b
}

var (
	inactiveTabStyle = lipgloss.NewStyle().
				Border(tabBorder("┴", "─", "┴"), true).
				BorderForeground(foreground).
				Faint(true).
				Padding(0, 1)
	activeTabStyle = inactiveTabStyle.
			Border(tabBorder("╯", " ", "╰"), true).
			BorderForeground(emphasis).
			Foreground(emphasis).
			Faint(false).
			Bold(true)
	// The gap is the rule that carries on past the last tab. It inherits the
	// tab's padding unless that is cleared, which would push the row two
	// cells past the terminal.
	tabGapStyle = inactiveTabStyle.
			BorderTop(false).BorderLeft(false).BorderRight(false).
			Padding(0, 0)
)

const (
	tabsHeight = 3 // border, label, border
	// playerRows is the box: border, bar, blank, the buttons, border. The
	// title that used to sit in it is the status bar's now.
	playerRows = 4 + controlsRows
	statusRows = 1
	// progressRows is everything below the list.
	progressRows = playerRows + statusRows
	maxTabTitle  = 18
	tabFurniture = 4 // a border and a space either side
)

// listHeight is how many tracks fit under the table's header.
func (m Model) listHeight() int { return max(m.bodyHeight()-headerRows, 1) }

// bodyHeight is how many rows the track table gets, its header included.
func (m Model) bodyHeight() int {
	if h := m.height - tabsHeight - progressRows; h > 1 {
		return h
	}
	return 1
}

// barRow is the line the progress bar is drawn on: past the list and the
// box's own border.
func (m Model) barRow() int { return tabsHeight + m.bodyHeight() + 1 }

// statusRow is the bar under the player.
func (m Model) statusRow() int { return tabsHeight + m.bodyHeight() + playerRows }

// controlsRow is the first line of the buttons, a blank line below the bar.
// They are controlsRows tall from there.
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
		m.renderStatusBar(),
	)
	// Anything floating sits over the frame rather than in it, so opening
	// one reflows nothing underneath.
	//
	// This goes through a compositor rather than composing layers onto a
	// canvas directly: a layer's own Draw ignores its position, and only
	// the compositor works out where each one belongs.
	layers := []*lipgloss.Layer{lipgloss.NewLayer(content)}
	z := 1
	if m.detour.active {
		// Every popover in the stack, not only the one in front: an album
		// is inset on the artist it opened from so that the artist is still
		// there around it, and it can only be there if it is drawn.
		for _, behind := range m.history {
			under := m
			under.detour = behind
			x, y, _, _ := under.modalBounds()
			layers = append(layers,
				lipgloss.NewLayer(under.renderModal()).X(x).Y(y).Z(z))
			z++
		}
		x, y, _, _ := m.modalBounds()
		layers = append(layers, lipgloss.NewLayer(m.renderModal()).X(x).Y(y).Z(z))
		z++
	}
	if m.menu.open {
		layers = append(layers,
			lipgloss.NewLayer(m.renderMenu()).X(m.menu.x).Y(m.menu.y).Z(z))
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
		return "\n\n" + lipgloss.NewStyle().Foreground(foreground).
			Render(strings.Repeat("─", max(0, m.width)))
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
	// The wait belongs where it is happening, not down in the status bar.
	if m.loading && len(m.Tracks) == 0 {
		return m.centredLoader(width, height)
	}
	return m.table(width, height).render()
}

// table is the main list as the shared table sees it.
func (m Model) table(width, height int) trackTable {
	return trackTable{
		sort:        m.sort,
		highlight:   m.highlightColor(),
		now:         m.clock(),
		tracks:      m.Tracks,
		cursor:      m.trackCursor,
		offset:      m.trackOffset,
		width:       width,
		height:      height,
		showRating:  m.showsRating(),
		playing:     m.playing.VideoID,
		more:        m.more.More(),
		loadingMore: m.loadingMore,
		loader:      m.loader(),
	}
}

// hasScrollbar reports whether the list is longer than the window. The
// column only exists when it has something to say, so a list that fits is
// not made narrower for nothing.
func (m Model) hasScrollbar() bool {
	return needsScrollbar(m.rowCount(), m.listHeight())
}

// scrollbarColumn is where it is drawn.
func (m Model) scrollbarColumn() int { return m.width - scrollbarWidth }

// scrollTo puts the list where a point on the scrollbar says it should be.
func (m *Model) scrollTo(y int) {
	height := m.listHeight()
	total := m.rowCount()
	if height <= 1 || total <= height {
		return
	}
	row := min(max(y-tabsHeight-headerRows, 0), height-1)
	furthest := total - height
	m.trackOffset = min(max(row*furthest/(height-1), 0), furthest)
}

// showsRating is false on the liked playlist, where every row is liked and
// the column would say the same thing all the way down.
func (m Model) showsRating() bool { return m.showingID != likedPlaylistID }

// The status bar is two blocks: a small bright one saying what the app is
// doing, and one holding what is playing that takes the rest of the row.
var (
	statusKeyStyle = lipgloss.NewStyle().
			Background(emphasis).
			Foreground(background).
			Bold(true).
			Padding(0, 1)
	statusAlertStyle = statusKeyStyle.Background(alert)
)

// statusBarStyle fills the wide half of the bar with the row highlight, so
// the two read as the same surface. No foreground with it: the highlight is
// a tint of the terminal's own background, so the terminal's own text colour
// still reads on it whatever the theme is.
func (m Model) statusBarStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(m.highlightColor())
}

// statusSegment is a run of text on the bar with its own fill.
//
// Each carries it, rather than the row being wrapped in one style: a nested
// style ends with a reset, and a reset clears the background as well as the
// weight. A bold title inside a filled row therefore ended the fill at the
// title — the bar simply stopped, mid-sentence, wherever the title did.
type statusSegment struct {
	text  string
	style lipgloss.Style
}

// renderStatusBar draws the row under the player.
func (m Model) renderStatusBar() string {
	key, style := m.statusKey(), statusKeyStyle
	if m.Err != nil {
		style = statusAlertStyle
	}

	// The block is as wide as the word in it. It used to be padded to the
	// longest of them so that nothing moved as the state changed, and what
	// that bought was a block with a hole in it most of the time.
	block := style.Render(truncate(key, max(m.width-2, 0)))
	return block + fillRow(m.statusSegments(), m.statusBarStyle(),
		max(m.width-lipgloss.Width(block), 0))
}

// statusKey is the word in the small block: what the app is doing, in the
// order that matters. Trouble first, then a wait, then the player — and
// ready only when there is nothing else to say.
func (m Model) statusKey() string {
	switch {
	case m.Err != nil:
		return "ERROR"
	case m.loading || m.loadingMore:
		return "LOADING"
	case m.playing.VideoID != "" && m.Paused:
		return "PAUSED"
	case m.playing.VideoID != "":
		return "PLAYING"
	default:
		return "READY"
	}
}

// statusSegments is what the wide block holds: the trouble, or the track.
func (m Model) statusSegments() []statusSegment {
	fill := m.statusBarStyle()
	switch {
	case m.Err != nil:
		return []statusSegment{{m.Err.Error(), fill}}
	case m.playing.VideoID == "":
		return []statusSegment{{"Nothing playing", fill}}
	case m.playing.Album == "":
		return []statusSegment{{m.playing.Title, fill.Bold(true)}}
	}
	// Bold carries the title against the album, since both sit on the same
	// fill and a second colour on it would be hard to read.
	return []statusSegment{
		{m.playing.Title, fill.Bold(true)},
		{", " + m.playing.Album, fill},
	}
}

// fillRow lays segments across a width and pads to it with the same fill,
// so the bar runs unbroken from one end of the row to the other.
func fillRow(segments []statusSegment, fill lipgloss.Style, width int) string {
	if width <= 0 {
		return ""
	}
	const padding = 1

	var b strings.Builder
	used := 0
	write := func(text string, style lipgloss.Style) {
		if text == "" {
			return
		}
		b.WriteString(style.Render(text))
		used += lipgloss.Width(text)
	}

	write(strings.Repeat(" ", min(padding, width)), fill)
	for _, segment := range segments {
		room := width - used - padding
		if room <= 0 {
			break
		}
		write(truncate(segment.text, room), segment.style)
	}
	if used < width {
		write(strings.Repeat(" ", width-used), fill)
	}
	return b.String()
}

// playerBox is the frame around the bar and the controls. Its border
// replaces the blank lines that used to separate them from the list, so it
// costs no height.
// Its border is the foreground and not the dim colour: lipgloss draws a
// border as a colour and cannot draw one faint, and the dim colour is too
// close to a light page to be a line at all.
var playerBox = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(foreground).
	Padding(0, playerPadding)

const (
	playerBorder  = 1
	playerPadding = 1
	// contentLeft is the first column inside the box, and contentWidth what
	// is left of the row once both sides are taken. The bar and the buttons
	// are laid out from it, and the border and padding put it there.
	contentLeft = playerBorder + playerPadding
)

func (m Model) contentWidth() int { return max(0, m.width-2*contentLeft) }

// highlightTint is how far the highlight moves off the page. Enough to see,
// little enough that a row of text still reads as text on it.
const highlightTint = 0.10

// highlightColor is the selected row's fill. Until the terminal says what
// its background is — and some never answer — the scheme's own grey stands
// in, which is what this always used.
func (m Model) highlightColor() color.Color {
	if m.highlight != nil {
		return m.highlight
	}
	return surface
}

func (m Model) renderPlayer() string {
	// One blank line, under the bar, so it is not wedged against the
	// buttons. Above it the box's own border is separation enough.
	inner := lipgloss.JoinVertical(lipgloss.Left,
		m.renderBar(),
		strings.Repeat(" ", m.contentWidth()),
		m.renderControls(),
	)
	return playerBox.Render(inner)
}

// litRamp is the played part of the bar: the brightest thing the scheme has,
// flat.
//
// It used to be a gradient between two steps of the accent. Monochrome there
// is no gradient to give it — two adjacent greys is not one — and worse, the
// step that would have been the low end is the colour the paused bar uses,
// so a bar under half way would have been indistinguishable from a paused
// one. Weight says playing here, and it says it the same the whole way
// along.
//
// It is a named palette entry rather than a hex value, which is what keeps
// the bar inside the terminal's own scheme: the terminal resolves it, so it
// is whatever the theme says it is. The component's own blend could not be
// used even when this was a gradient — it interpolates in RGB through
// lipgloss.Blend1D and emits true colour, off-scheme by construction.
func litRamp(_, _ float64) color.Color { return emphasis }

// mutedRamp takes the played part down a step without taking it away:
// paused, the bar stops being the brightest thing on the screen but still
// says where the playhead is. It has to differ from the groove as well as
// from the lit state — matching the groove hid the position, which is the one
// thing the bar is for.
func mutedRamp(_, _ float64) color.Color { return played }

// emptyCell is what the bar has not reached yet: a solid block, so the track
// reads as a filled groove rather than as texture. The played part is told
// apart by its colour, not by its weight. The groove's colour is the row
// highlight, set at render time — see renderBar.
//
// A perforated glyph would have been closer to the idea, but the ones that
// exist — U+1FB95 CHECKER BOARD FILL, the crosshatched squares at
// U+25A6..U+25A9 — are in none of the fonts here, and a fallback font draws
// at whatever width it likes. Only the shade and block characters are safe.
const emptyCell = '█'

func newBar(fill progress.ColorFunc) progress.Model {
	bar := progress.New(
		progress.WithoutPercentage(),
		progress.WithColorFunc(fill),
		progress.WithFillCharacters(progress.DefaultFullCharHalfBlock, emptyCell),
	)
	// Overwritten per render with the row highlight; this is only what an
	// unrendered bar holds.
	bar.EmptyColor = surface
	return bar
}

// renderBar draws the position, greyed out while playback is paused.
//
// The groove takes the row highlight, so the bar sits on the same surface
// the selected row and the status bar do. It is set here rather than when the
// bar is built because the highlight is not known until the terminal answers
// for it; progress.Model is a value, so the copy carries the width already
// set on the original.
func (m Model) renderBar() string {
	bar := m.bar
	if m.Paused {
		bar = m.pausedBar
	}
	bar.EmptyColor = m.highlightColor()
	return bar.ViewAs(m.fraction())
}

// truncate cuts a string to fit a number of screen cells, ending it with an
// ellipsis to say that something was cut.
//
// It counts cells and not runes, which is not the same count: a CJK
// character or an emoji occupies two, a combining mark none. Slicing by rune
// index against a width measured in cells panics on the first title that is
// not Latin.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		// No room for both a character and the mark saying there was more.
		return "…"
	}

	var b strings.Builder
	b.Grow(len(s))
	width := 0
	for _, r := range s {
		cells := lipgloss.Width(string(r))
		if width+cells > w-1 {
			break
		}
		b.WriteRune(r)
		width += cells
	}
	return b.String() + "…"
}

func pad(s string, w int) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}
