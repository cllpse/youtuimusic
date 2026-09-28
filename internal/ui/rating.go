package ui

import (
	tea "charm.land/bubbletea/v2"
)

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
	setIn := func(rows []Track) {
		for i := range rows {
			if rows[i].VideoID == videoID {
				rows[i].Rating = r
			}
		}
	}
	setIn(m.Tracks)
	setIn(m.arrival)
	setIn(m.detour.tracks)
	setIn(m.detour.arrival)
	for _, behind := range m.history {
		setIn(behind.tracks)
		setIn(behind.arrival)
	}
	for _, entry := range m.cache {
		setIn(entry.tracks)
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
