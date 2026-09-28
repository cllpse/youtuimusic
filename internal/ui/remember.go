package ui

import (
	"github.com/cllpse/youtuimusic/internal/state"
)

// Reopening the app is not always the user's idea — a stale session is
// fixed in the browser and then here — so what was playing is remembered
// across it: the playlist that was open, and the track in it. The theme
// comes back the same way, because it is a display choice and choosing it
// again every launch is a small tax of its own.
//
// Nothing starts playing on its own. The track is put under the cursor
// instead, which is what the play button acts on when nothing is loaded, so
// the first press picks up where the last session left off.

// Restore arranges for the app to open on what it was last playing, and in
// the theme it was last read in.
//
// The playlist cannot be applied yet: it has to wait for the library to
// arrive, and the track for that playlist's listing, so the state is held
// until the things it names exist. The theme has no such wait — it is about
// the screen, not about anything the server has to say — so it is applied
// here and now.
func (m Model) Restore(st state.State) Model {
	if st.Mono {
		m = m.setMono(true)
	}
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
	for i := range m.tabCount() {
		if m.tabAt(i).ID == m.restoring.Playlist {
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

// Record writes what is playing and where, for the next time the app opens.
// It is exported so main can save on a signal that does not come through the
// key handler, and it is safe to call more than once.
func (m Model) Record() { m.record() }

// record writes what is playing, where, and in what theme, for the next time
// the app opens. It is called on the way out, where a blocking write of a few
// dozen bytes costs nothing and is certain to have happened before the process
// ends.
func (m Model) record() {
	// Read what is there first: a mix is not somewhere to come back to, and
	// when one is in front the playlist already on disk is the one worth
	// keeping. The theme is written either way.
	s := state.Load()
	s.Mono = m.mono

	tab, ok := m.SelectedPlaylist()
	// A mix is gone when the app closes and its id names nothing the next
	// time. Leaving the last playlist written where it was opens on that
	// instead, which is the last page of yours the reader was on.
	if !ok || tab.kind == tabMix {
		// Best effort: failing to write this must not stop the app closing.
		_ = state.Save(s)
		return
	}
	s.Playlist, s.Playing = tab.ID, m.playing.VideoID
	_ = state.Save(s)
}
