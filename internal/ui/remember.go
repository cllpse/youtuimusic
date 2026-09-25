package ui

import (
	"github.com/cllpse/youtuimusic/internal/state"
)

// Reopening the app is not always the user's idea — a stale session is
// fixed in the browser and then here — so what was playing is remembered
// across it: the playlist that was open, and the track in it.
//
// Nothing starts playing on its own. The track is put under the cursor
// instead, which is what the play button acts on when nothing is loaded, so
// the first press picks up where the last session left off.

// Restore arranges for the app to open on what it was last playing.
//
// Nothing can be applied yet: the playlist has to wait for the library to
// arrive, and the track for that playlist's listing, so the state is held
// until the things it names exist.
func (m Model) Restore(st state.State) Model {
	if st.Playlist != "" {
		m.restoring = &st
	}
	return m
}

// restoreTab points the tabs at the remembered playlist, if it is still
// there. A playlist that has since been deleted leaves the app opening at
// the first tab, which is where it would have opened anyway.
func (m *Model) restoreTab() {
	if m.restoring == nil {
		return
	}
	for i, p := range m.Playlists {
		if p.ID == m.restoring.Playlist {
			m.tabCursor = i
			return
		}
	}
	m.restoring = nil
}

// restorePlaying puts the remembered track under the cursor, once the
// listing it belongs to is on screen.
//
// The state is consumed either way: it named one playlist's track, and
// looking for it in a later listing would be wrong. A track that has since
// been removed from the playlist leaves the cursor at the top.
func (m *Model) restorePlaying(id string) {
	if m.restoring == nil || m.restoring.Playlist != id {
		return
	}
	track := m.restoring.Playing
	m.restoring = nil
	if track == "" {
		return
	}
	for i, t := range m.Tracks {
		if t.VideoID == track {
			m.trackCursor = i
			m.scroll()
			return
		}
	}
}

// record writes what is playing and where, for the next time the app opens.
// It is called on the way out, where a blocking write of a few dozen bytes
// costs nothing and is certain to have happened before the process ends.
func (m Model) record() {
	tab, ok := m.SelectedPlaylist()
	if !ok {
		return
	}
	// Best effort: failing to write this must not stop the app closing.
	_ = state.Save(state.State{Playlist: tab.ID, Playing: m.playing.VideoID})
}
