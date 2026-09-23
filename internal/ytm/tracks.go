package ytm

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Track is a song in a playlist or a search result.
type Track struct {
	VideoID  string
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
	// SetVideoID identifies this track's occurrence within a playlist, which
	// is what a removal has to target — the same song can appear twice.
	SetVideoID string
}

// Rating is a thumbs state.
type Rating int

const (
	RatingNone Rating = iota
	RatingUp
	RatingDown
)

// endpoint is the InnerTube path that applies this rating.
func (r Rating) endpoint() string {
	switch r {
	case RatingUp:
		return "like/like"
	case RatingDown:
		return "like/dislike"
	default:
		return "like/removelike"
	}
}

// PlaylistTracks returns the tracks in a playlist.
//
// The browse id is the playlist id with a VL prefix; callers pass the bare
// playlist id and this adds it, so ids round-trip with LibraryPlaylists.
func (c *Client) PlaylistTracks(ctx context.Context, playlistID string) ([]Track, error) {
	browseID := playlistID
	if !strings.HasPrefix(browseID, "VL") {
		browseID = "VL" + browseID
	}
	raw, err := c.post(ctx, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		return nil, err
	}
	return parseTracks(raw)
}

// songsFilter restricts search to songs. Without it the server answers with
// whatever matches — mostly user-uploaded videos, with view counts where the
// album should be — which is not what a music player wants.
const songsFilter = "EgWKAQIIAWoMEA4QChADEAQQCRAF"

// Search returns songs matching a query.
func (c *Client) Search(ctx context.Context, query string) ([]Track, error) {
	raw, err := c.post(ctx, "search", map[string]any{
		"query":  query,
		"params": songsFilter,
	})
	if err != nil {
		return nil, err
	}
	return parseTracks(raw)
}

// Rate sets the thumbs state of a track. Applying the rating a track already
// has is the caller's job to avoid; RatingNone clears whatever is set.
func (c *Client) Rate(ctx context.Context, videoID string, r Rating) error {
	if videoID == "" {
		return fmt.Errorf("ytm: rate: no video id")
	}
	_, err := c.post(ctx, r.endpoint(), map[string]any{
		"target": map[string]any{"videoId": videoID},
	})
	return err
}

// parseTracks pulls every track row out of a browse or search response.
func parseTracks(raw json.RawMessage) ([]Track, error) {
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("ytm: parse: %w", err)
	}

	var out []Track
	for _, node := range findAll(tree, "musicResponsiveListItemRenderer") {
		item, ok := node.(map[string]any)
		if !ok {
			continue
		}
		t := Track{
			Title:    flexColumn(item, 0),
			Artist:   flexColumn(item, 1),
			Album:    flexColumn(item, 2),
			Duration: parseDuration(fixedColumn(item, 0)),
		}
		if pid, ok := item["playlistItemData"].(map[string]any); ok {
			t.VideoID, _ = pid["videoId"].(string)
			t.SetVideoID, _ = pid["playlistSetVideoId"].(string)
		}
		if t.VideoID == "" {
			// Search results carry the id on the play endpoint instead.
			t.VideoID = firstVideoID(item)
		}
		if t.VideoID == "" {
			continue // a header or shelf row, not a track
		}
		out = append(out, t)
	}
	return out, nil
}

// flexColumn reads the nth variable-width column's text.
func flexColumn(item map[string]any, n int) string {
	cols, _ := item["flexColumns"].([]any)
	if n >= len(cols) {
		return ""
	}
	col, _ := cols[n].(map[string]any)
	r, _ := col["musicResponsiveListItemFlexColumnRenderer"].(map[string]any)
	return runsText(r["text"])
}

// fixedColumn reads the nth fixed-width column's text — duration lives here.
func fixedColumn(item map[string]any, n int) string {
	cols, _ := item["fixedColumns"].([]any)
	if n >= len(cols) {
		return ""
	}
	col, _ := cols[n].(map[string]any)
	r, _ := col["musicResponsiveListItemFixedColumnRenderer"].(map[string]any)
	return runsText(r["text"])
}

func firstVideoID(item map[string]any) string {
	for _, v := range findAll(item, "videoId") {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// parseDuration reads "2:54" and "1:02:03".
func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	var total time.Duration
	units := []time.Duration{time.Minute, time.Second}
	if len(parts) == 3 {
		units = []time.Duration{time.Hour, time.Minute, time.Second}
	}
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 {
			return 0
		}
		total += time.Duration(n) * units[i]
	}
	return total
}
