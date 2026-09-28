package ui

import (
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cllpse/youtuimusic/internal/player"
)


// Update folds a message into the model and returns the next one.
//
// The convention: anything that returns a model takes one by value and returns
// the changed copy, and the private helpers that only mutate — moveCursor,
// scroll, setRating and the like — take a pointer and are called on the local
// copy before it is returned. Mixing the two the other way round is how a
// mutation lands on a copy nobody sees.
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
		// The loop stops as soon as nothing is waiting on the network. That
		// includes the next-page row, which is why this is busy() and not
		// loading: otherwise the load-more spinner is drawn but never moves.
		if !m.busy() {
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
		// A page fetched to keep a track going advances as soon as it lands.
		if msg.autoplay {
			if next, ok := m.following(); ok {
				return m, m.play(next)
			}
		}
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
	case player.PropTimePos:
		// A drag owns the position until the button comes up. mpv carries on
		// playing and reporting where it actually is, and letting that
		// through makes the bar fight the pointer.
		if f, ok := ev.Data.(float64); ok && !m.scrubbing {
			m.Position = time.Duration(f * float64(time.Second))
		}
	case player.PropDuration:
		if f, ok := ev.Data.(float64); ok && f > 0 {
			m.Length = time.Duration(f * float64(time.Second))
		}
	case player.PropPause:
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
			// Nothing follows in what has been fetched. A mix is endless, so
			// ask for the next page and let its arrival advance playback.
			if next, cmd := m.fetchMoreToPlay(); cmd != nil {
				return next, batch(m.watchEvents(), cmd)
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
