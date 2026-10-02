package ui

import (
	"errors"
	"fmt"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/cllpse/youtuimusic/internal/player"
)

// Update folds a message into the model and returns the next one.
//
// The convention: anything that returns a model takes one by value and returns
// the changed copy, and the private helpers that only mutate — moveCursor,
// scroll, setRating and the like — take a pointer and are called on the local
// copy before it is returned. Mixing the two the other way round is how a
// mutation lands on a copy nobody sees.
//
// Called before the return statement, and not inside it. In
// `return m, m.startLoading()` Go does not say whether m is read before the
// call changes it or after: the compiler today happens to run the call first,
// and nothing promises it will tomorrow. So the command is taken first —
// `cmd := m.startLoading(); return m, cmd` — and the model returned is the one
// it changed.
//
// On the way out, the model works out again which tabs hold the playing track
// when what it last worked out no longer describes it — see playingTabs — and
// starts the spinner if a wait has just begun — see keepSpinning.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	after, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	if !after.holds.describes(after) {
		after.holds = after.holdsFor()
	}
	spin := after.keepSpinning()
	return after, batch(cmd, spin)
}

// update is Update without the bookkeeping on the way out.
func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case uv.UnknownOscEvent:
		// One of the palette entries askColours asked for. Anything else
		// that arrives this way is nothing the app asked about.
		if entry, c, ok := paletteReply(string(msg)); ok && int(entry) < len(m.derived) {
			m.derived[entry] = deriveBlock(c)
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// The bar's width is not settled here: it depends on how wide the
		// times beside it read, which changes without the terminal doing
		// anything. renderBar sets it on the copy it draws.
		m.scroll()
		// The page may be a different colour than it was; ask again.
		return m, askColours()

	case tea.FocusMsg:
		// Coming back to the terminal is the closest thing to notice that the
		// theme changed while we were not looking.
		return m, askColours()

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
		// The loop stops as soon as nothing is waited on. That includes the
		// next-page row and a track that has not started sounding, which is
		// why this is waiting() and not loading: otherwise a spinner is drawn
		// but never moves.
		if !m.waiting() {
			m.spinning = false
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
		// A load armed for a listing the reader has since moved off is not
		// this one's to make: a newer one was armed for what replaced it, or
		// what replaced it was already in memory.
		if msg.inDetour {
			if msg.generation != m.detourGen || !m.detour.active || m.detour.tab.ID != msg.tab.ID {
				return m, nil
			}
		} else if msg.generation != m.tabGen {
			return m, nil
		} else if m.tabAt(m.tabCursor).ID != msg.tab.ID {
			// Nothing newer was armed for the tabs, so nothing else is going
			// to end this wait.
			m.loading = false
			return m, nil
		}
		return m, m.fetchTracks(msg.tab)

	case playlistsMsg:
		// A fresh slice rather than truncating in place: the model travels by
		// value, and reusing the backing array lets one copy write through to
		// another's.
		m.Playlists = make([]Playlist, 0, len(msg))
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
		m.addPage(msg.id, msg.inDetour, fromAPI(msg.page.Tracks), msg.page.Next)
		// A page fetched to keep a track going advances as soon as it lands.
		if msg.autoplay {
			if next, ok := m.following(); ok {
				cmd := m.request(next)
				return m, cmd
			}
		}
		// A sort over half a list is not the order, so it keeps going.
		cmd := m.continueSort()
		return m, cmd

	case tracksMsg:
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
		// Kept whoever is in front, so that a listing the reader has moved
		// off is there when they come back to it.
		m.cache[msg.id] = cached{tracks, msg.page.Next}
		if m.detour.active && m.detour.tab.ID == msg.id {
			// A menu is anchored to a row of the list being replaced.
			m.menu = trackMenu{}
			m.detour.fill(msg.id, tracks, msg.page.Next)
			m.Err = nil
			if len(tracks) > 0 {
				return m, m.prefetch(tracks[0].VideoID)
			}
			return m, nil
		}
		// The list in the tabs takes its own answer whatever is in front of
		// it: it is still there underneath, and the popover is not forever.
		// Asking which listing was in front instead is how a search opened
		// while a tab loaded left the tab waiting on an answer it had thrown
		// away. Anything else the reader has moved on from, and its answer
		// waits in the cache for when they come back.
		if m.tabAt(m.tabCursor).ID != msg.id {
			return m, nil
		}
		if !m.detour.active {
			m.menu = trackMenu{}
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
			cmd := m.continueSort()
			return m, cmd
		}
		m.trackCursor, m.trackOffset = 0, 0
		m.restorePlaying(msg.id)
		cmd := m.continueSort()
		if len(m.Tracks) > 0 {
			cmd = batch(m.prefetch(m.Tracks[0].VideoID), cmd)
		}
		return m, cmd

	case searchMsg:
		// Kept under the query, where a popover stepped back to will look
		// for it, and where a track played from these results says it came
		// from.
		id, tracks := searchID(msg.query), fromAPI(msg.page.Tracks)
		m.cache[id] = cached{tracks, msg.page.Next}
		// Shown only for the query the popover is on now. It may have been
		// closed or covered by an album while this was in flight, and an
		// answer to a query since replaced is not the answer: put up, it
		// would overwrite the newer results and end a wait that is still on.
		if !m.detour.active || m.detour.tab.kind != tabSearch || m.detour.searched != msg.query {
			return m, nil
		}
		// Nothing is chosen: the results are results until the reader picks
		// one, and picking the first for them was a guess that also cost a
		// stream resolve for a track nobody had asked to hear.
		m.detour.fill(id, tracks, msg.page.Next)
		m.Err = nil
		return m, nil

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
		// mpv has it now, and its own state says when it sounds. An older
		// track arriving late leaves a newer request waiting.
		if msg.track.VideoID == m.requested {
			m.requested = ""
		}
		// A different track gets its own retry. The same one keeps the
		// mark, or a stream that always fails would be retried forever.
		if msg.track.VideoID != m.retried {
			m.retried = ""
		}
		if next, ok := m.following(); ok && next.VideoID != m.playing.VideoID {
			return m, m.prefetch(next.VideoID)
		}
		return m, nil

	case eventMsg:
		return m.handleEvent(player.Event(msg))

	case errMsg:
		m.Err = msg.err
		m.requested = ""
		// The player going away ends no wait on the network. Anything else
		// may be a fetch that failed, and nothing more is coming for one.
		if !errors.Is(msg.err, errPlayerGone) {
			m.loading, m.detour.loading = false, false
		}
		return m, nil
	}
	return m, nil
}

// handleEvent folds an mpv property change into the model. Every branch
// re-arms the watch; forgetting to would silently end the event stream.
func (m Model) handleEvent(ev player.Event) (tea.Model, tea.Cmd) {
	switch ev.Name {
	case player.PropTimePos:
		// A drag owns the position until the button comes up. mpv carries on
		// playing and reporting where it actually is, and letting that
		// through makes the bar fight the pointer.
		if f, ok := ev.Data.(float64); ok && !m.scrubbing {
			m.Position = seconds(f)
		}
	case player.PropDuration:
		if f, ok := ev.Data.(float64); ok && f > 0 {
			m.Length = seconds(f)
		}
	case player.PropPause:
		if b, ok := ev.Data.(bool); ok {
			m.Paused = b
		}
	case player.PropCoreIdle:
		if b, ok := ev.Data.(bool); ok {
			m.coreIdle = b
		}
	case player.PropIdleActive:
		if b, ok := ev.Data.(bool); ok {
			m.idleActive = b
		}
	case player.EndFile:
		// Only a track running out advances the list. Loading a replacement
		// ends the previous file too, and advancing on that would run away
		// through the playlist.
		//
		// This is deliberately not the eof-reached property: mpv unloads the
		// file at the same moment, so the property goes unavailable rather
		// than true and nothing ever fires.
		switch reason, _ := ev.Data.(string); reason {
		case "eof":
			if next, ok := m.following(); ok {
				cmd := m.request(next)
				return m, batch(m.watchEvents(), cmd)
			}
			// Nothing follows in what has been fetched. A mix is endless, so
			// ask for the next page and let its arrival advance playback.
			if next, cmd := m.fetchMoreToPlay(); cmd != nil {
				return next, batch(m.watchEvents(), cmd)
			}
		case "error":
			return m.playbackFailed(ev.Err)
		}
	}
	return m, m.watchEvents()
}

// playbackFailed handles mpv refusing the stream it was given.
//
// The usual cause is a cached URL YouTube no longer honours, which fails the
// moment it is opened, so the first failure of a track forgets its URL and
// resolves it again. A second is the track's own and is said, rather than the
// screen claiming it plays at 0:00 while nothing comes out.
func (m Model) playbackFailed(reason string) (tea.Model, tea.Cmd) {
	t := m.playing
	if t.VideoID == "" {
		return m, m.watchEvents()
	}
	if m.retried != t.VideoID {
		m.retried = t.VideoID
		cmd := m.replay(t)
		if cmd != nil {
			m.requested = t.VideoID
		}
		return m, batch(m.watchEvents(), cmd)
	}
	if reason == "" {
		reason = "the stream would not play"
	}
	m.requested = ""
	m.Err = fmt.Errorf("playing %s: %s", t.Title, reason)
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
		if m.detour.active || m.tabAt(m.tabCursor).kind == tabMix {
			return m, nil
		}
		return m.sortBy(m.sort.next())
	case matches(msg, k.SortReverse):
		if m.detour.active || m.tabAt(m.tabCursor).kind == tabMix {
			return m, nil
		}
		return m.sortBy(sortSpec{by: max(m.sort.by, sortTitle), desc: !m.sort.desc})

	case matches(msg, k.Monochrome):
		return m.toggleMono()

	case matches(msg, k.Refresh):
		return m.refreshTab()

	case matches(msg, k.Next):
		return m.press(controlNext)
	case matches(msg, k.Previous):
		return m.press(controlPrevious)
	case matches(msg, k.Repeat):
		return m.press(controlRepeat)
	case matches(msg, k.Mix):
		return m.press(controlMix)

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
