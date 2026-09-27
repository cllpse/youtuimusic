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

// hue is the colour a rated track is drawn in, and false for an unrated one,
// which is drawn in nothing in particular.
//
// It replaces a mark in front of the title. A mark costs two cells of every
// rated row and says nothing the row could not have said by being a colour,
// and it cost a glyph that had to exist in the reader's font — which is the
// last thing in the app that did.
func (r Rating) hue() (color.Color, bool) {
	switch r {
	case RatingUp:
		return liked, true
	case RatingDown:
		return disliked, true
	default:
		return nil, false
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
	// sheetOpen is the keys sheet, which is in front of everything when it is
	// up and is not part of the stack behind it: it is what the app does, not
	// somewhere you went.
	sheetOpen bool
	// history is the popovers behind the one in front. Going to an album
	// from an artist comes back to the artist rather than to nothing.
	history []detour

	// playing is the track mpv is on, held whole rather than by id so the
	// controls can still show and rate it after another tab is opened.
	playing Track
	repeat  Repeat
	loading bool

	// highlight is the selected row's fill and dimmed the colour of a line
	// that has to be quiet, both derived from the terminal's own background.
	// Nil until it answers, and some never do.
	highlight color.Color
	dimmed    color.Color
	quiet     color.Color

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
	// it rather than to the list below, and while they do nothing in the
	// list is chosen — cursor is noRow and the results are just results.
	query  string
	typing bool
	// searched is the query the results below belong to, which is not the
	// query being typed. Empty until one has been run, which is how the
	// popover tells "nothing typed yet" from "nothing found".
	searched string
}

// noRow is a cursor with nothing under it. A search popover starts there and
// goes back there when the input takes the keys again.
const noRow = -1

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
			m.dimmed = lipgloss.Lighten(msg, dimmedTint)
			m.quiet = lipgloss.Lighten(msg, quietTint)
		} else {
			m.highlight = lipgloss.Darken(msg, highlightTint)
			m.dimmed = lipgloss.Darken(msg, dimmedTint)
			m.quiet = lipgloss.Darken(msg, quietTint)
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// The bar's width is not settled here: it depends on how wide the
		// times beside it read, which changes without the terminal doing
		// anything. renderBar sets it on the copy it draws.
		m.scroll()
		// The page may be a different colour than it was; ask again.
		return m, tea.RequestBackgroundColor

	case tea.FocusMsg:
		// Coming back to the terminal is the closest thing to notice that the
		// theme changed while we were not looking.
		return m, tea.RequestBackgroundColor

	case tea.KeyPressMsg:
		if m.sheetOpen {
			if next, cmd, handled := m.handleSheetKey(msg); handled {
				return next, cmd
			}
		}
		if m.menu.open {
			return m.handleMenuKey(msg)
		}
		if m.detour.active {
			if next, cmd, handled := m.handleModalKey(msg); handled {
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
		// Nothing is chosen: the results are results until the reader picks
		// one, and picking the first for them was a guess that also cost a
		// stream resolve for a track nobody had asked to hear.
		m.detour.cursor, m.detour.offset = noRow, 0
		m.loading, m.Err = false, nil
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
	k := appKeys

	switch {
	case matches(msg, k.Quit):
		m.record()
		return m, tea.Quit

	case matches(msg, k.Help):
		return m.toggleSheet()

	case matches(msg, k.Search):
		return m.openSearch()

	case matches(msg, k.Open):
		if t, ok := m.SelectedTrack(); ok {
			return m.open(t)
		}

	case matches(msg, k.PlayPause):
		return m.press(controlPlayPause)

	case matches(msg, k.Sort):
		if m.detour.active {
			return m, nil
		}
		return m.sortBy(m.sort.next())
	case matches(msg, k.SortReverse):
		if m.detour.active {
			return m, nil
		}
		return m.sortBy(sortSpec{by: max(m.sort.by, sortTitle), desc: !m.sort.desc})

	case matches(msg, k.Next):
		return m.press(controlNext)
	case matches(msg, k.Previous):
		return m.press(controlPrevious)
	case matches(msg, k.Repeat):
		return m.press(controlRepeat)

	case matches(msg, k.PrevTab):
		return m.selectTab(m.tabCursor - 1)
	case matches(msg, k.NextTab):
		return m.selectTab(m.tabCursor + 1)

	case matches(msg, k.Up):
		m.moveCursor(-1)
		return m.afterCursorMove()
	case matches(msg, k.Down):
		m.moveCursor(1)
		return m.afterCursorMove()

	case matches(msg, k.PageUp):
		m.moveCursor(-m.listHeight())
		return m.afterCursorMove()
	case matches(msg, k.PageDown):
		m.moveCursor(m.listHeight())
		return m.afterCursorMove()

	case matches(msg, k.Top):
		m.moveCursor(-m.rowCount())
		return m.afterCursorMove()
	case matches(msg, k.Bottom):
		m.moveCursor(m.rowCount())
		return m.afterCursorMove()

	case matches(msg, k.Like):
		return m.applyRating(RatingUp)
	case matches(msg, k.Dislike):
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
	// The same track can be anywhere: in the list, in the popover over it, in
	// a popover stacked behind that one — which the inset leaves showing, so
	// a stale mark there is a stale mark on the screen — and in any listing
	// already fetched and kept, which is what a later visit is served from.
	//
	// Each in the order it arrived in as well as the order it is shown in,
	// because that is what a sort rebuilds from.
	lists := [][]Track{m.Tracks, m.arrival, m.detour.tracks, m.detour.arrival}
	for _, behind := range m.history {
		lists = append(lists, behind.tracks, behind.arrival)
	}
	for _, entry := range m.cache {
		lists = append(lists, entry.tracks)
	}
	for _, rows := range lists {
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
	return m.rated(videoID, r, previous)
}

// rated applies a rating and, where it is a dislike of what is playing, moves
// on. Nothing honours "do not play this" like not playing it.
//
// Only of what is playing: disliking a row further down the list says
// something about that row, not about the next three minutes. And only a
// dislike that lands — pressing it again takes the dislike off, which is not
// a reason to skip anything.
func (m Model) rated(videoID string, r, previous Rating) (tea.Model, tea.Cmd) {
	m.setRating(videoID, r)
	cmd := m.rate(videoID, r, previous)
	if r != RatingDown || videoID != m.playing.VideoID {
		return m, cmd
	}
	next, onward := m.skip(true)
	return next, batch(cmd, onward)
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
				Faint(true).
				Padding(0, 1)
	activeTabStyle = inactiveTabStyle.
			Border(tabBorder("╯", " ", "╰"), true).
			BorderForeground(emphasis).
			Foreground(emphasis).
			Faint(false).
			Bold(true)
)

// A tab that is not in front is dim all the way round: its label faint, its
// border the dimmed colour rather than the full foreground. The border needs
// saying at render time because the colour is not known until the terminal
// answers for it.
func (m Model) inactiveTab() lipgloss.Style {
	return inactiveTabStyle.BorderForeground(m.dimmedColor())
}

// likedTabInFront reports whether the tab in front is the liked playlist. What
// is under it is a page of that playlist, and the lines that close the page off
// — the rule the tabs sit on, and the one above the player — say so.
func (m Model) likedTabInFront() bool {
	return m.tabAt(m.tabCursor).ID == likedPlaylistID
}

// quietTab is a tab with something in front of the whole row: no faint, which
// is a step off whatever colour it is on, but the quiet colour outright.
func (m Model) quietTab() lipgloss.Style {
	return inactiveTabStyle.
		Faint(false).
		Foreground(m.quietColor()).
		BorderForeground(m.quietColor())
}

const (
	tabsHeight = 3 // border, label, border
	// playerRows is the rule, the buttons, a blank, the bar and a blank. The
	// title that used to sit up here is the status bar's now.
	playerRows = 4 + controlsRows
	statusRows = 1
	// progressRows is everything below the list.
	progressRows = playerRows + statusRows
	maxTabTitle  = 18
	tabFurniture = 4 // a border and a space either side
)

// covered reports whether anything is drawn in front of the frame. It is what
// makes the tabs and the list go quiet: with something over them, none of what
// they usually say — this tab is in front, this row is selected — is being said
// to anyone.
//
// The menu is not in it. It is a few rows anchored to what was clicked, and
// sinking the whole page behind something that small would be a lot of
// movement for a shrug.
func (m Model) covered() bool { return m.detour.active || m.sheetOpen }

// tabRuleColor is the rule the tabs sit on, which sinks with them and takes the
// liked playlist's colour while that is the tab in front.
func (m Model) tabRuleColor() color.Color {
	switch {
	case m.covered():
		return m.quietColor()
	case m.likedTabInFront():
		return liked
	}
	return m.dimmedColor()
}

// tabPen is the colour a tab's own outline takes, which is not the rule's: the
// liked playlist's magenta, the emphasis for the one in front, the dimmed
// colour for the rest, and the quiet one for all of them behind a popover.
//
// Its top edge and its two walls, that is. Not its feet — see tabFootPen.
func (m Model) tabPen(index int) color.Color {
	switch {
	case m.covered():
		return m.quietColor()
	case m.tabAt(index).ID == likedPlaylistID:
		return liked
	case index == m.tabCursor:
		return emphasis
	}
	return m.dimmedColor()
}

// tabFootPen is the colour of the two glyphs a tab puts in the rule. They are
// where the tab meets the line rather than part of the box above it, so the
// liked playlist's colour stops before them: it runs from the top edge down the
// walls and hands over at the floor.
func (m Model) tabFootPen(index int) color.Color {
	switch {
	case m.covered():
		return m.quietColor()
	case index == m.tabCursor:
		return emphasis
	}
	return m.dimmedColor()
}

// listHeight is how many tracks fit under the table's header.
func (m Model) listHeight() int { return max(m.bodyHeight()-headerRows, 1) }

// bodyHeight is how many rows the track table gets, its header included.
func (m Model) bodyHeight() int {
	if h := m.height - tabsHeight - progressRows; h > 1 {
		return h
	}
	return 1
}

// playerTop is the rule the player starts with, and everything in it is
// counted from there.
func (m Model) playerTop() int { return tabsHeight + m.bodyHeight() }

// barRow is the line the progress bar is drawn on: the rule, the buttons, a
// blank, and then the bar.
func (m Model) barRow() int { return m.playerTop() + 2 + controlsRows }

// statusRow is the bar under the player.
func (m Model) statusRow() int { return tabsHeight + m.bodyHeight() + playerRows }

// controlsRow is the first line of the buttons, directly under the rule.
// They are controlsRows tall from there.
func (m Model) controlsRow() int { return m.playerTop() + 1 }

// barGeometry is the column the progress bar starts at and how wide it is.
// Rendering and hit-testing both go through this, so a click lands where the
// bar appears to be — which is not the whole row: a time and a space sit
// either side of it.
func (m Model) barGeometry() (start, width int) {
	if !m.barShowsTimes() {
		return contentLeft, m.contentWidth()
	}
	left, right := m.barFlanks()
	return contentLeft + left, max(m.contentWidth()-left-right, 0)
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
		// Every one of them: this view is what the renderer compares the
		// next against, and a mode missing here is a mode never asked for.
		v := tea.NewView("")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		v.ReportFocus = true
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
		for depth, behind := range m.history {
			under := m
			under.detour = behind
			// Its own depth, not the front one's: a popover renders at the
			// size its place in the stack gives it.
			under.history = m.history[:depth]
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
		z++
	}
	if m.sheetOpen {
		// In front of everything, including a popover: it is what the app
		// does, and that does not change with what you have open.
		x, y, _, _ := m.sheetBounds()
		layers = append(layers, lipgloss.NewLayer(m.renderSheet()).X(x).Y(y).Z(z))
	}
	if len(layers) > 1 {
		content = lipgloss.NewCompositor(layers...).Render()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "youtuimusic"
	// Cell motion reports drags, which is what scrubbing the bar needs.
	v.MouseMode = tea.MouseModeCellMotion
	// Focus is the only notice of a theme change there is — see the comment
	// on highlightColor.
	v.ReportFocus = true
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
		return "\n\n" + lipgloss.NewStyle().Foreground(m.tabRuleColor()).
			Render(strings.Repeat("─", max(0, m.width)))
	}
	rendered := make([]string, 0, len(spans))
	for _, s := range spans {
		// With a popover in front, no tab is the tab in front and the whole
		// row sinks towards the page.
		style := m.inactiveTab()
		switch {
		case m.covered():
			style = m.quietTab()
		case s.index == m.tabCursor:
			style = activeTabStyle
		}
		// The liked playlist is what you think of a track, a tab of it, so it is
		// drawn in the same magenta a liked row is — its outline as well as its
		// label, so the tab reads as one thing rather than as a label with a box
		// of its own round it.
		//
		// The outline of an inactive one comes out a shade stronger than its
		// label, because lipgloss draws a border as a colour and faint is an
		// attribute that cannot reach it.
		if !m.covered() && m.tabAt(s.index).ID == likedPlaylistID {
			style = style.Foreground(liked)
		}
		// The bottom edge is not the tab's to draw. See tabRule.
		style = style.BorderForeground(m.tabPen(s.index)).BorderBottom(false)
		rendered = append(rendered, style.Render(truncate(m.tabAt(s.index).Title, maxTabTitle)))
	}

	// The tabs are two rows now — their top edge and their label — and the row
	// they sit in is as wide as the window whether they fill it or not.
	above := strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, rendered...), "\n")
	for i, line := range above {
		above[i] = line + strings.Repeat(" ", max(0, m.width-lipgloss.Width(line)))
	}
	return strings.Join(append(above, m.tabRule(spans)), "\n")
}

// tabRule is the line the tabs sit on, drawn as one line rather than as the
// bottom edge of each of them.
//
// That is what keeps a colour on the rule off the tabs. The line used to be six
// borders in a row, so colouring it meant colouring part of every tab — and a
// tab's two corner glyphs carry a tick up into its own walls, which made the
// colour look like it was leaking into playlists it had nothing to say about.
// Now each tab puts two feet in the rule, in its own colour, and everything
// between and around them belongs to the rule.
//
// The tab in front is open underneath rather than closed, which is what says it
// is in front.
func (m Model) tabRule(spans []tabSpan) string {
	rule := lipgloss.NewStyle().Foreground(m.tabRuleColor())
	var b strings.Builder
	at := 0
	for _, s := range spans {
		if s.start > at {
			b.WriteString(rule.Render(strings.Repeat("─", s.start-at)))
		}
		feet := lipgloss.NewStyle().Foreground(m.tabFootPen(s.index))
		inner := max(s.end-s.start-2, 0)
		if s.index == m.tabCursor && !m.covered() {
			b.WriteString(feet.Render("╯") + strings.Repeat(" ", inner) + feet.Render("╰"))
		} else {
			b.WriteString(feet.Render("┴") + rule.Render(strings.Repeat("─", inner)) + feet.Render("┴"))
		}
		at = s.end
	}
	if at < m.width {
		b.WriteString(rule.Render(strings.Repeat("─", m.width-at)))
	}
	return b.String()
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
		inactive:    m.covered(),
		quiet:       m.quietColor(),
		now:         m.clock(),
		tracks:      m.Tracks,
		cursor:      m.trackCursor,
		offset:      m.trackOffset,
		width:       width,
		height:      height,
		showRating:  m.showsRating(),
		likedList:   m.showingID == likedPlaylistID,
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

// scrollbarColumn is where it is drawn: past the blank on its left, and one
// short of the edge.
func (m Model) scrollbarColumn() int { return m.width - scrollbarWidth + 1 }

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

// The status bar is two blocks: a small coloured one saying what the app is
// doing, and one holding what is playing that takes the rest of the row.
//
// The block's fill is the state, so it can be read without reading it. Its
// text is the background colour — the end of the palette that inverts with the
// theme, so it is light text on a dark scheme's hues and dark text on a light
// scheme's, which is the closest a fixed pair gets to portable on a fill whose
// hue does not invert at all.
var statusBlockStyle = lipgloss.NewStyle().
	Foreground(background).
	Bold(true).
	Padding(0, statusBlockPadding)

const statusBlockPadding = 1

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
	block, fill := m.statusBlock(), m.statusBarStyle()
	room := max(m.width-lipgloss.Width(block), 0)

	// The way into the keys sits at the far end of the band. It takes the
	// band's own fill, so it reads as part of it rather than as something
	// dropped on top, and it carries the cell of air the other end has.
	tail := ""
	if _, ok := m.helpButtonSpan(); ok {
		state := buttonDefault
		if m.sheetOpen {
			state = buttonActive
		}
		tail = fill.Render(renderButton(labelHelp, state) + " ")
		room -= helpButtonWidth + 1
	}
	return block + fillRow(m.statusSegments(), fill, room) + tail
}

// statusBlock is the small block at the start of the bar, filled with the
// colour of whatever it says.
//
// It is as wide as the word in it. It used to be padded to the longest of them
// so that nothing moved as the state changed, and what that bought was a block
// with a hole in it most of the time.
func (m Model) statusBlock() string {
	word, hue := m.statusState()
	return statusBlockStyle.Background(hue).
		Render(truncate(word, max(m.width-2*statusBlockPadding, 0)))
}

// statusState is what the block says and the colour it says it in, together,
// because a word and a fill that disagree are worse than either alone — a
// LOADING that has gone green while a track plays says two things at once.
//
// The order is the one that matters. Trouble first, then a wait, then the
// player, and ready only when there is nothing else to say.
func (m Model) statusState() (string, color.Color) {
	switch {
	case m.Err != nil:
		return "ERROR", alert
	case m.loading || m.loadingMore:
		// A wait is not trouble and it is not nothing either: something is
		// outstanding, and yellow is how long a wait gets noticed.
		return "LOADING", busy
	case m.playing.VideoID != "" && m.Paused:
		return "PAUSED", live
	case m.playing.VideoID != "":
		return "PLAYING", live
	default:
		return "READY", good
	}
}

// statusKey is the word alone.
func (m Model) statusKey() string {
	word, _ := m.statusState()
	return word
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
// separator divides the list from the player. A line is enough to say where
// one ends and the other begins, and it costs the row a box cost four sides
// of.
func (m Model) separator() string {
	return lipgloss.NewStyle().Foreground(m.separatorColor()).
		Render(strings.Repeat("─", max(m.width, 0)))
}

// separatorColor is the dimmed colour, or the liked playlist's magenta while
// that is the tab in front: the line closes off a page of its rows, and it says
// which page that is the way the tab does.
//
// Dimmed and not quiet, and magenta whatever is in front of the list: the
// player stays live with a popover over it, so the line above the player does
// too.
func (m Model) separatorColor() color.Color {
	if m.likedTabInFront() {
		return liked
	}
	return m.dimmedColor()
}

// contentLeft is the column the bar and the buttons are laid out from, and
// contentWidth how much of the row they have. Both run edge to edge, the way
// the list and the tabs above them do — there is no frame left to sit inside.
const contentLeft = 0

func (m Model) contentWidth() int { return max(0, m.width-2*contentLeft) }

// highlightTint is how far the highlight moves off the page. Enough to see,
// little enough that a row of text still reads as text on it.
const highlightTint = 0.10

// dimmedTint is how far a dimmed line moves off the page. Half way, which is
// what faint text lands on, so the two read as the same weight.
//
// A border cannot be faint — lipgloss draws one as a colour and there is no
// attribute to give it — so a dim border has to be a dim colour, and the
// scheme has only colour 8 for that. Against a white page colour 8 is about
// 1.88:1, which is not a line. Half a step off the page is nearer 4:1 either
// way the page runs.
const dimmedTint = 0.50

// quietTint is how far a frame with something in front of it moves off the
// page. A quarter, which is nearer the page than anything else here: it is
// lighter than the dimmed line on a light theme and darker on a dark one,
// because both mean the same thing — closer to the page it is sinking into.
//
// It is knowingly under the contrast floor the rest of the frame holds to.
// That floor is for things being read, and nothing in a frame behind a
// popover is being read. It is only ever used while one is in front.
const quietTint = 0.25

// A note on switching themes underneath a running app.
//
// Everything drawn in a named palette entry follows a theme change on its
// own: 0 to 15 are resolved by the terminal on every repaint, so the moment
// the palette changes the next frame is in the new colours. That is most of
// the interface — the filled buttons, the status block, every border, the
// faint text.
//
// The highlight cannot. It is not a palette entry: it is the terminal's own
// background moved a tenth of the way towards its foreground, which has to
// be asked for and arrives as a message. Asked once at startup, it is a
// concrete colour from then on, and a theme change leaves it describing the
// old page — the selected row, the wide half of the status bar, the bar's
// groove and the scrollbar all keep the tint of a page that is no longer
// there.
//
// There is no notice of a theme change to hang a refresh on. The mode that
// would provide one, DEC 2031, is not implemented anywhere in this stack, so
// the terminal is never asked to report and never does. What is left is
// focus and resize: switching a theme usually means leaving the terminal and
// coming back to it, and often changes the window. Both re-ask.

// highlightColor is the selected row's fill. Until the terminal says what
// its background is — and some never answer — the scheme's own grey stands
// in, which is what this always used.
func (m Model) highlightColor() color.Color {
	if m.highlight != nil {
		return m.highlight
	}
	return surface
}

// dimmedColor is a line that is on the page without being read. Until the
// terminal says what colour it is, the scheme's own dim entry stands in,
// which is what every border used before this.
func (m Model) dimmedColor() color.Color {
	if m.dimmed != nil {
		return m.dimmed
	}
	return muted
}

// quietColor is what a frame behind a popover is drawn in. Until the terminal
// says what colour it is the scheme's dim entry stands in, which is the
// nearest thing it has to a page and is what faint used to do here.
func (m Model) quietColor() color.Color {
	if m.quiet != nil {
		return m.quiet
	}
	return muted
}

func (m Model) renderPlayer() string {
	blank := strings.Repeat(" ", max(m.width, 0))
	return lipgloss.JoinVertical(lipgloss.Left,
		m.separator(),
		m.renderControls(),
		blank,
		m.renderBar(),
		blank,
	)
}

// litRamp is the played part of the bar: the player's blue, flat.
//
// It used to be a gradient between two steps of the accent, then the brightest
// thing the scheme had. There is no gradient to give it — two adjacent greys
// is not one — and worse, the step that would have been the low end is the
// colour the paused bar uses, so a bar under half way was indistinguishable
// from a paused one. It says playing the same the whole way along, and now it
// says it in the same colour the row and the state block do.
//
// It is a named palette entry rather than a hex value, which is what keeps
// the bar inside the terminal's own scheme: the terminal resolves it, so it
// is whatever the theme says blue is. The component's own blend could not be
// used even when this was a gradient — it interpolates in RGB through
// lipgloss.Blend1D and emits true colour, off-scheme by construction.
func litRamp(_, _ float64) color.Color { return live }

// mutedRamp takes the colour out of the played part without taking the part
// away: paused, the bar stops saying the track is running but still says where
// the playhead is. It has to differ from the groove as well as from the lit
// state — matching the groove hid the position, which is the one thing the bar
// is for.
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

// The times either side of the bar are as wide as they read and no wider, so
// the bar gives up a cell when a track passes ten minutes and takes it back
// on the next one. Nothing is held for a digit that is not there.

// barLeastWidth is how much bar is worth keeping. Under that the times give
// way: they are a hint about the bar, and a hint that has eaten the thing it
// was hinting at is not one.
const barLeastWidth = 8

// Idle, the bar runs from nought to nothing in particular: a zero where the
// position goes, because that is where it would start, and an ellipsis where
// the length would be, because there is no track to have one. Two empty
// fields said the same thing by saying nothing, which reads as a bar that
// has not finished drawing.
const (
	labelIdlePosition = "00:00"
	labelIdleLength   = "…"
)

// barTimes is what goes either side of the bar, trimmed to what it reads.
func (m Model) barTimes() (at, runs string) {
	if m.Length <= 0 {
		return labelIdlePosition, labelIdleLength
	}
	return strings.TrimSpace(formatDuration(m.Position)),
		strings.TrimSpace(formatDuration(m.Length))
}

// barFlanks is what the times and the space beside each of them occupy.
func (m Model) barFlanks() (left, right int) {
	at, runs := m.barTimes()
	return lipgloss.Width(at) + 1, lipgloss.Width(runs) + 1
}

// barShowsTimes reports whether there is room for them.
func (m Model) barShowsTimes() bool {
	left, right := m.barFlanks()
	return m.contentWidth() >= left+right+barLeastWidth
}

// renderBar draws the position between the time it is at and the time it
// runs to, greyed out while playback is paused.
//
// The bar says how far through the track it is; the two times say how far
// that is in seconds, which a bar on its own never does.
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
	// The width is settled here and not on a resize, because the times are as
	// wide as they read and what is left over is the bar's. barGeometry works
	// it out the same way for the click that lands on it.
	_, width := m.barGeometry()
	bar.SetWidth(width)

	if !m.barShowsTimes() {
		return bar.ViewAs(m.fraction())
	}
	at, runs := m.barTimes()
	return dim.Render(at) + " " + bar.ViewAs(m.fraction()) + " " + dim.Render(runs)
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

// padLeft is pad the other way round: the text against the right edge.
func padLeft(s string, w int) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return strings.Repeat(" ", gap) + s
}

func pad(s string, w int) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}
