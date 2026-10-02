package ytm

import (
	"context"
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

// RadioPrefix turns a video id into the id of the mix built around it, which
// is what YouTube Music calls a radio. It is a playlist id like any other as
// far as the rest of the app is concerned — it names a listing, and it is the
// one prefix that says the listing was made for you rather than by you.
const RadioPrefix = "RDAMVM"

// RadioID is the radio of one track.
func RadioID(videoID string) string { return RadioPrefix + videoID }

// RadioSeed is the track a radio was built around.
func RadioSeed(playlistID string) string {
	return strings.TrimPrefix(playlistID, RadioPrefix)
}

// queueEndpoint answers with a queue rather than with a listing.
const queueEndpoint = "next"

// Radio returns the mix the server builds around a track: the track itself
// first, and then what it would play after it.
//
// It goes to next rather than browse, because a mix is a queue and not a
// listing — there is nothing to browse until the server has made one. The
// three settings are what the web player sends; without
// enablePersistentPlaylistPanel the answer describes the track and leaves the
// queue out entirely.
//
// Deliberately no params. The value ytmusicapi sends for a radio,
// wAEB8gECKAE%3D, now comes back with the queue missing — measured against a
// live account: 17kB and no panel with it, 2.4MB and sixty-five tracks
// without. Nothing in the answer says why, so this sends what works.
//
// The page it hands back carries a continuation like any other, and a mix is
// endless: the server says so in the panel and it holds up — fifty more tracks
// after the first fifty-odd, no repeats among them, and another token with
// them.
func (c *Client) Radio(ctx context.Context, videoID string) (Page, error) {
	if videoID == "" {
		return Page{}, fmt.Errorf("ytm: mix: no track to start from")
	}
	return c.page(ctx, queueEndpoint, map[string]any{
		"videoId":                       videoID,
		"playlistId":                    RadioID(videoID),
		"enablePersistentPlaylistPanel": true,
		"isAudioOnly":                   true,
		"tunerSettingValue":             "AUTOMIX_SETTING_NORMAL",
	})
}

// queueTracks reads the rows of a watch queue.
//
// A queue row is a playlistPanelVideoRenderer, which is not the shape a
// listing uses: the title, the length and the byline are their own fields
// rather than columns. What the byline is made of is the same though, so the
// artist and the album are read out of it by their links the way they are
// everywhere else.
//
// No rating comes back on these rows. The panel carries a like button rather
// than a like status, so a mix opens with nothing marked and marks what you
// rate while you are in it.
func queueTracks(tree any) []Track {
	skip := counterparts(tree)
	var out []Track
	for _, node := range findAll(tree, "playlistPanelVideoRenderer") {
		item, ok := node.(map[string]any)
		if !ok {
			continue
		}
		id, _ := item["videoId"].(string)
		if id == "" || skip[id] {
			continue
		}
		r := scanRow(item)
		out = append(out, Track{
			VideoID:  id,
			Title:    tidy(runsText(item["title"])),
			Artist:   tidy(r.artist),
			Album:    tidy(r.album),
			Duration: parseDuration(runsText(item["lengthText"])),
			ArtistID: r.artistID,
			AlbumID:  r.albumID,
		})
	}
	return out
}

// counterparts is every row of a queue that is another row's music video.
//
// Some rows come wrapped: a primaryRenderer holding the song, and a
// counterpartRenderer holding the same song as a video, which is what the queue
// would play if you asked for video rather than audio. Both are
// playlistPanelVideoRenderers, so a walk that looks for those finds the song
// twice — once with its artist and album, and once without, since the video
// carries neither. Measured on a live mix: fifty rows, six of them wrapped, and
// fifty-six tracks out the other end.
//
// They are gathered by their own video ids rather than by where they sit,
// because the first page of a mix and the pages after it are not the same shape
// and an id is an id in both.
func counterparts(tree any) map[string]bool {
	out := map[string]bool{}
	for _, node := range findAll(tree, "counterpartRenderer") {
		for _, inner := range findAll(node, "playlistPanelVideoRenderer") {
			item, ok := inner.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := item["videoId"].(string); id != "" {
				out[id] = true
			}
		}
	}
	return out
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
//
// The endpoint says what shape to expect: next answers with a queue and
// everything else with a listing, and the two are made of different renderers.
// It is also what a continuation carries, so the page after a mix is read the
// same way the first one was.
func (c *Client) page(ctx context.Context, endpoint string, body map[string]any) (Page, error) {
	tree, err := c.post(ctx, endpoint, body)
	if err != nil {
		return Page{}, err
	}
	var tracks []Track
	if endpoint == queueEndpoint {
		tracks = queueTracks(tree)
	} else {
		tracks = parseTracks(tree)
	}
	return Page{
		Tracks: tracks,
		Next:   Continuation{Endpoint: endpoint, Token: continuationToken(tree)},
	}, nil
}

// continuationKeys are where a page keeps the token for the next one, in
// the order they are trusted. The newer shape came in without the older ones
// going away, so all are looked for.
var continuationKeys = [...]string{"continuationCommand", "nextContinuationData", "nextRadioContinuationData"}

// continuationToken finds the token for the next page.
//
// All three keys are looked for in one walk. A key that is not there costs a
// walk of the whole page to find out, and the last page of every listing has
// none of them, so asking for each in turn was three walks of a page whose
// answer was nothing.
func continuationToken(tree any) string {
	var found [len(continuationKeys)]map[string]any
	var walk func(node any) bool
	walk = func(node any) bool {
		switch v := node.(type) {
		case map[string]any:
			for i, key := range continuationKeys {
				if found[i] == nil {
					found[i], _ = v[key].(map[string]any)
				}
			}
			if found[0] != nil {
				return true // the preferred shape: nothing else can win
			}
			for _, child := range v {
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	walk(tree)

	for _, data := range found {
		// The two old shapes spell the field differently; the command
		// carries the token itself.
		for _, field := range []string{"token", "continuation"} {
			if token, _ := data[field].(string); token != "" {
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
	tree, err := c.post(ctx, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		return Page{}, err
	}
	return Page{
		Tracks: append(parseTracks(tree), parseReleases(tree)...),
		Next:   Continuation{Endpoint: "browse", Token: continuationToken(tree)},
	}, nil
}

// parseReleases pulls the album tiles off a page. They are a different
// renderer from a track row — a tile rather than a line — which is why the
// track parser walks straight past them.
func parseReleases(tree any) []Track {
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
		title := tidy(runsText(item["title"]))
		out = append(out, Track{
			Title:   title,
			Artist:  tidy(runsText(item["subtitle"])),
			Album:   title,
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

// parseTracks pulls every track row out of a browse or search response tree.
func parseTracks(tree any) []Track {
	var out []Track
	for _, node := range findAll(tree, "musicResponsiveListItemRenderer") {
		item, ok := node.(map[string]any)
		if !ok {
			continue
		}
		r := scanRow(item)
		t := Track{
			Title:    tidy(flexColumn(item, 0)),
			Duration: parseDuration(fixedColumn(item, 0)),
			AlbumID:  r.albumID,
			ArtistID: r.artistID,
			Rating:   r.rating,
		}
		t.Artist, t.Album = artistAndAlbum(item, r)
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
			t.VideoID = r.videoID
		}
		if t.VideoID == "" {
			continue // a header or shelf row, not a track
		}
		out = append(out, t)
	}
	return out
}

// artistAndAlbum reads a row's artist and album.
//
// A playlist puts each in its own column. A search result does not: it packs
// the type, the artist, the album and the length into one column separated
// by bullets, which read straight through as part of the artist's name. The
// links say which part is which, so they are used first; the bullets are
// only taken apart when a row links nowhere.
func artistAndAlbum(item map[string]any, r row) (artist, album string) {
	artist, album = r.artist, r.album
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

// The page a browse id leads to, which is how an album link is told from an
// artist link when both hang off the same row.
const (
	pageTypeAlbum  = "MUSIC_PAGE_TYPE_ALBUM"
	pageTypeArtist = "MUSIC_PAGE_TYPE_ARTIST"
)

func pageTypeOf(endpoint map[string]any) string {
	configs, _ := endpoint["browseEndpointContextSupportedConfigs"].(map[string]any)
	music, _ := configs["browseEndpointContextMusicConfig"].(map[string]any)
	pageType, _ := music["pageType"].(string)
	return pageType
}

// row is what the parsers look for in a track row, gathered in one walk of
// it.
//
// Where these sit differs between a playlist row, a search result and a
// queue entry — the like button hangs off the row's menu at a depth that
// varies, the links live in whichever column the artist or album landed in —
// so a row is searched rather than navigated. It is searched once for all of
// them: asking each question with its own walk was five or six walks of
// every row, which on a long page cost as much again as decoding it.
type row struct {
	// albumID and artistID are the first browse ids leading to each kind of
	// page, from anywhere in the row. album and artist are the text of the
	// first column run that links to each.
	albumID, artistID string
	album, artist     string
	// rating is the thumbs state the server reports. Without it a track
	// already liked comes back looking unrated, and the controls show an
	// empty thumb for a song in Liked Music.
	rating Rating
	rated  bool
	// videoID is the first video id anywhere in the row, which is where a
	// search result keeps it.
	videoID string
}

func scanRow(item map[string]any) row {
	var r row
	var walk func(node any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			for key, child := range v {
				switch key {
				case "browseEndpoint":
					if endpoint, ok := child.(map[string]any); ok {
						r.noteTarget(endpoint)
					}
				case "runs":
					if runs, ok := child.([]any); ok {
						r.noteRuns(runs)
					}
				case "likeStatus":
					r.noteRating(child)
				case "videoId":
					if id, _ := child.(string); r.videoID == "" {
						r.videoID = id
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(item)
	return r
}

// noteTarget keeps a browse id if it is the first to lead to its kind of
// page. The links are told apart by where they lead rather than by where
// they sit, because which column is which varies.
func (r *row) noteTarget(endpoint map[string]any) {
	id, _ := endpoint["browseId"].(string)
	if id == "" {
		return
	}
	switch pageTypeOf(endpoint) {
	case pageTypeAlbum:
		if r.albumID == "" {
			r.albumID = id
		}
	case pageTypeArtist:
		if r.artistID == "" {
			r.artistID = id
		}
	}
}

// noteRuns keeps the text of the first run linking to each kind of page.
func (r *row) noteRuns(runs []any) {
	for _, item := range runs {
		run, ok := item.(map[string]any)
		if !ok {
			continue
		}
		nav, _ := run["navigationEndpoint"].(map[string]any)
		endpoint, _ := nav["browseEndpoint"].(map[string]any)
		text, _ := run["text"].(string)
		if endpoint == nil || text == "" {
			continue
		}
		switch pageTypeOf(endpoint) {
		case pageTypeAlbum:
			if r.album == "" {
				r.album = text
			}
		case pageTypeArtist:
			if r.artist == "" {
				r.artist = text
			}
		}
	}
}

func (r *row) noteRating(status any) {
	if r.rated {
		return
	}
	switch s, _ := status.(string); s {
	case "LIKE":
		r.rating, r.rated = RatingUp, true
	case "DISLIKE":
		r.rating, r.rated = RatingDown, true
	case "INDIFFERENT":
		r.rating, r.rated = RatingNone, true
	}
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
