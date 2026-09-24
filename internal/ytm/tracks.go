package ytm

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Track is a song in a playlist or a search result.
type Track struct {
	VideoID  string
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
	// AlbumID and ArtistID are browse ids, empty when the row does not
	// link anywhere — a single with no album page, say.
	AlbumID  string
	ArtistID string
	// Rating is the thumbs state the server already has for this track.
	Rating Rating
	// Added is when the track joined the listing, and is the zero time
	// where the listing does not say.
	Added time.Time
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
func (c *Client) PlaylistTracks(ctx context.Context, playlistID string) (Page, error) {
	browseID := playlistID
	if !strings.HasPrefix(browseID, "VL") {
		browseID = "VL" + browseID
	}
	return c.page(ctx, "browse", map[string]any{"browseId": browseID})
}

// songsFilter restricts search to songs. Without it the server answers with
// whatever matches — mostly user-uploaded videos, with view counts where the
// album should be — which is not what a music player wants.
const songsFilter = "EgWKAQIIAWoMEA4QChADEAQQCRAF"

// Search returns songs matching a query.
//
// The server answers twenty at a time and hands back a token for the rest,
// which the caller asks for when it wants them.
func (c *Client) Search(ctx context.Context, query string) (Page, error) {
	return c.page(ctx, "search", map[string]any{
		"query":  query,
		"params": songsFilter,
	})
}

// Page is a chunk of a listing, and what is needed to ask for the next one.
//
// Listings are paged at the server and a page is all anyone looks at first,
// so this hands one back with the means to go on rather than spending the
// requests up front.
type Page struct {
	Tracks []Track
	Next   Continuation
}

// Continuation is a place in a listing. It carries the endpoint as well as
// the token because the two are not interchangeable: a search continues at
// search and a browse at browse.
type Continuation struct {
	Endpoint string
	Token    string
}

// More reports whether there is another page.
func (c Continuation) More() bool { return c.Token != "" }

// More fetches the page after one already read.
func (c *Client) More(ctx context.Context, from Continuation) (Page, error) {
	if !from.More() {
		return Page{}, nil
	}
	return c.page(ctx, from.Endpoint, map[string]any{"continuation": from.Token})
}

// page reads one page of a listing.
func (c *Client) page(ctx context.Context, endpoint string, body map[string]any) (Page, error) {
	raw, err := c.post(ctx, endpoint, body)
	if err != nil {
		return Page{}, err
	}
	tracks, err := c.parseTracks(raw)
	if err != nil {
		return Page{}, err
	}
	return Page{
		Tracks: tracks,
		Next:   Continuation{Endpoint: endpoint, Token: continuationToken(raw)},
	}, nil
}

// continuationToken finds the token for the next page. The newer shape came
// in without the older one going away, so both are looked for.
func continuationToken(raw json.RawMessage) string {
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return ""
	}
	for _, node := range findAll(tree, "continuationCommand") {
		if command, ok := node.(map[string]any); ok {
			if token, _ := command["token"].(string); token != "" {
				return token
			}
		}
	}
	for _, node := range findAll(tree, "nextContinuationData") {
		if data, ok := node.(map[string]any); ok {
			if token, _ := data["continuation"].(string); token != "" {
				return token
			}
		}
	}
	return ""
}

// AlbumTracks returns the tracks on an album.
func (c *Client) AlbumTracks(ctx context.Context, browseID string) (Page, error) {
	if browseID == "" {
		return Page{}, fmt.Errorf("ytm: browse: no id")
	}
	return c.page(ctx, "browse", map[string]any{"browseId": browseID})
}

// ArtistPage returns what an artist's page leads with: their songs, and
// then their releases.
//
// A release comes back as a Track with no video id — there is nothing to
// play — carrying the album's browse id instead, so that opening one is the
// same operation as going to a track's album.
func (c *Client) ArtistPage(ctx context.Context, browseID string) (Page, error) {
	if browseID == "" {
		return Page{}, fmt.Errorf("ytm: browse: no id")
	}
	raw, err := c.post(ctx, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		return Page{}, err
	}
	songs, err := c.parseTracks(raw)
	if err != nil {
		return Page{}, err
	}
	return Page{
		Tracks: append(songs, parseReleases(raw)...),
		Next:   Continuation{Endpoint: "browse", Token: continuationToken(raw)},
	}, nil
}

// parseReleases pulls the album tiles off a page. They are a different
// renderer from a track row — a tile rather than a line — which is why the
// track parser walks straight past them.
func parseReleases(raw json.RawMessage) []Track {
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil
	}
	var out []Track
	seen := make(map[string]bool)
	for _, node := range findAll(tree, "musicTwoRowItemRenderer") {
		item, ok := node.(map[string]any)
		if !ok {
			continue
		}
		id, pageType := browseTile(item)
		if id == "" || pageType != pageTypeAlbum || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Track{
			Title:   tidy(runsText(item["title"])),
			Artist:  tidy(runsText(item["subtitle"])),
			AlbumID: id,
		})
	}
	return out
}

// browseTile reads where a tile leads and what kind of page that is.
func browseTile(item map[string]any) (id, pageType string) {
	nav, _ := item["navigationEndpoint"].(map[string]any)
	endpoint, _ := nav["browseEndpoint"].(map[string]any)
	if endpoint == nil {
		return "", ""
	}
	id, _ = endpoint["browseId"].(string)
	return id, pageTypeOf(endpoint)
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
func (c *Client) parseTracks(raw json.RawMessage) ([]Track, error) {
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
			Duration: parseDuration(fixedColumn(item, 0)),
			AlbumID:  browseTarget(item, pageTypeAlbum),
			ArtistID: browseTarget(item, pageTypeArtist),
			Rating:   ratingOf(item),
			Added:    addedOn(item, c.Now),
		}
		t.Title = tidy(t.Title)
		t.Artist, t.Album = artistAndAlbum(item)
		t.Artist, t.Album = tidy(t.Artist), tidy(t.Album)
		if t.Duration == 0 {
			t.Duration = durationIn(flexColumn(item, 1))
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

// artistAndAlbum reads a row's artist and album.
//
// A playlist puts each in its own column. A search result does not: it packs
// the type, the artist, the album and the length into one column separated
// by bullets, which read straight through as part of the artist's name. The
// links say which part is which, so they are used first; the bullets are
// only taken apart when a row links nowhere.
func artistAndAlbum(item map[string]any) (artist, album string) {
	artist = linkedText(item, pageTypeArtist)
	album = linkedText(item, pageTypeAlbum)
	if artist != "" && album != "" {
		return artist, album
	}

	parts := bulletParts(flexColumn(item, 1))
	if artist == "" && len(parts) > 0 {
		artist = parts[0]
	}
	if album == "" && len(parts) > 1 {
		album = parts[1]
	}
	if album == "" {
		album = flexColumn(item, 2)
	}
	return artist, album
}

// linkedText finds the text of the run that leads to a given kind of page.
func linkedText(item map[string]any, pageType string) string {
	for _, node := range findAll(item, "runs") {
		runs, ok := node.([]any)
		if !ok {
			continue
		}
		for _, r := range runs {
			run, ok := r.(map[string]any)
			if !ok {
				continue
			}
			nav, _ := run["navigationEndpoint"].(map[string]any)
			endpoint, _ := nav["browseEndpoint"].(map[string]any)
			if endpoint == nil || pageTypeOf(endpoint) != pageType {
				continue
			}
			if text, _ := run["text"].(string); text != "" {
				return text
			}
		}
	}
	return ""
}

// tidy makes a field fit to draw in a column.
//
// It replaces the bullet YouTube separates parts of a field with — "Album •
// 2017" reads, mid-line, as a glyph that went wrong — and then strips what a
// terminal cannot lay out. Fields come back with zero-width joiners,
// direction marks and the occasional control character: they take no width,
// and then a column that was counted in runes does not line up with one
// counted in cells.
//
// Whitespace is collapsed for the same reason. A newline inside a title
// would otherwise break the row in half.
func tidy(s string) string {
	s = strings.ReplaceAll(s, " • ", ", ")
	s = strings.ReplaceAll(s, "•", ", ")

	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			pendingSpace = b.Len() > 0
		case !unicode.IsGraphic(r):
			// Control, format, surrogate, private use, unassigned: nothing
			// a terminal can draw.
		default:
			if pendingSpace {
				b.WriteByte(' ')
				pendingSpace = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// rowTypes are the words a search result leads with, which name the kind of
// thing rather than the thing.
var rowTypes = map[string]bool{
	"song": true, "video": true, "album": true, "single": true,
	"ep": true, "playlist": true, "artist": true,
}

// bulletParts splits a packed column and keeps only the parts that name
// something — not the row's type, its length, or how many times it has been
// played.
func bulletParts(s string) []string {
	var out []string
	for _, part := range strings.Split(s, "•") {
		part = strings.TrimSpace(part)
		if part == "" || rowTypes[strings.ToLower(part)] || parseDuration(part) > 0 {
			continue
		}
		if strings.HasSuffix(part, "views") || strings.HasSuffix(part, "plays") {
			continue
		}
		out = append(out, part)
	}
	return out
}

// durationIn finds a length among a packed column's parts.
func durationIn(s string) time.Duration {
	for _, part := range strings.Split(s, "•") {
		if d := parseDuration(strings.TrimSpace(part)); d > 0 {
			return d
		}
	}
	return 0
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

// ratingOf reads the thumbs state the server reports. Without it a track
// already liked comes back looking unrated, and the controls show an empty
// thumb for a song in Liked Music.
//
// It is searched for rather than reached: the like button hangs off the row's
// menu at a depth that differs between a playlist and a search result.
func ratingOf(item map[string]any) Rating {
	for _, v := range findAll(item, "likeStatus") {
		switch s, _ := v.(string); s {
		case "LIKE":
			return RatingUp
		case "DISLIKE":
			return RatingDown
		case "INDIFFERENT":
			return RatingNone
		}
	}
	return RatingNone
}

// The page a browse id leads to, which is how an album link is told from an
// artist link when both hang off the same row.
const (
	pageTypeAlbum  = "MUSIC_PAGE_TYPE_ALBUM"
	pageTypeArtist = "MUSIC_PAGE_TYPE_ARTIST"
)

// browseTarget finds the browse id on a row that leads to a given kind of
// page. The links live in the column runs — the artist in one, the album in
// another — but which column is which varies, so they are told apart by
// where they lead rather than by where they sit.
func browseTarget(item map[string]any, pageType string) string {
	for _, node := range findAll(item, "browseEndpoint") {
		endpoint, ok := node.(map[string]any)
		if !ok {
			continue
		}
		id, _ := endpoint["browseId"].(string)
		if id == "" || pageTypeOf(endpoint) != pageType {
			continue
		}
		return id
	}
	return ""
}

func pageTypeOf(endpoint map[string]any) string {
	configs, _ := endpoint["browseEndpointContextSupportedConfigs"].(map[string]any)
	music, _ := configs["browseEndpointContextMusicConfig"].(map[string]any)
	pageType, _ := music["pageType"].(string)
	return pageType
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
