package ui

import (
	"github.com/cllpse/youtuimusic/internal/state"
)

// Reopening the app is not always the user's idea — a stale session is
// fixed in the browser and then here — so where they were is remembered
// across it. What is worth keeping is the view: which playlist was open,
// where in it they had got to, and how it was ordered.

// sortNames is the persisted spelling of each column, and the order of this
// list is not the enum's: the file says "artist", so renumbering the enum
// later cannot silently change what a saved state means.
var sortNames = map[sortColumn]string{
	sortTitle:  "title",
	sortArtist: "artist",
	sortLength: "length",
	sortAdded:  "added",
}

func (c sortColumn) name() string { return sortNames[c] }

func parseSortColumn(name string) sortColumn {
	for col, n := range sortNames {
		if n == name {
			return col
		}
	}
	return sortNone
}

var repeatNames = map[Repeat]string{
	RepeatOff: "off",
	RepeatAll: "all",
	RepeatOne: "one",
}

func (r Repeat) name() string { return repeatNames[r] }

func parseRepeat(name string) Repeat {
	for r, n := range repeatNames {
		if n == name {
			return r
		}
	}
	return RepeatOff
}

// Restore arranges for the app to open where it last closed.
//
// What can be applied now is: the order and the repeat mode depend on
// nothing. The playlist has to wait for the library to arrive and the
// position for that playlist's tracks, so those are held until the thing
// they name exists.
func (m Model) Restore(st state.State) Model {
	m.sort = sortSpec{by: parseSortColumn(st.Sort), desc: st.Descending}
	m.repeat = parseRepeat(st.Repeat)
	if st.Playlist != "" {
		m.restoring = &st
	}
	return m
}

// restoreTab points the tabs at the remembered playlist, if it is still
// there. A playlist that has since been deleted or renamed leaves the app
// opening at the first tab, which is where it would have opened anyway.
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

// restorePosition puts the cursor back, once the listing it refers to is on
// screen. It is consumed either way: the position is for the tab that was
// open at the time, and applying it to a later one would be wrong.
func (m *Model) restorePosition(id string) {
	if m.restoring == nil || m.restoring.Playlist != id {
		return
	}
	// A listing that has since got shorter clamps rather than scrolling
	// past its end.
	m.trackCursor = clamp(m.restoring.Cursor, len(m.Tracks))
	m.trackOffset = m.restoring.Offset
	m.restoring = nil
	m.scroll()
}

// record writes where the app is, for the next time it opens. It is called
// on the way out, where a blocking write of a few hundred bytes costs
// nothing and is certain to have happened before the process ends.
func (m Model) record() {
	tab, ok := m.SelectedPlaylist()
	if !ok {
		return
	}
	// Best effort: failing to write this must not stop the app closing.
	_ = state.Save(state.State{
		Playlist:   tab.ID,
		Cursor:     m.trackCursor,
		Offset:     m.trackOffset,
		Sort:       m.sort.by.name(),
		Descending: m.sort.desc,
		Repeat:     m.repeat.name(),
	})
}
