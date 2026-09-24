package ytm

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Shaped after a real browse response: title, artist and album in flex
// columns, duration in a fixed column, ids in playlistItemData.
func trackRow(title, artist, album, dur, videoID, setID string) string {
	return `{"musicResponsiveListItemRenderer":{
	  "flexColumns":[
	    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"` + title + `"}]}}},
	    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"` + artist + `"}]}}},
	    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"` + album + `"}]}}}],
	  "fixedColumns":[
	    {"musicResponsiveListItemFixedColumnRenderer":{"text":{"runs":[{"text":"` + dur + `"}]}}}],
	  "playlistItemData":{"videoId":"` + videoID + `","playlistSetVideoId":"` + setID + `"}}}`
}

func TestPlaylistTracks(t *testing.T) {
	var gotBrowseID string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotBrowseID, _ = body["browseId"].(string)
		_, _ = w.Write([]byte(signedInBody(
			trackRow("her room", "Yøuth", "her room", "2:54", "bbEtARu1YZc", "56B44F6D") + "," +
				trackRow("Long One", "Someone", "Album", "1:02:03", "vid2", "set2"))))
	})

	page, err := c.PlaylistTracks(context.Background(), "PL123")
	got := page.Tracks
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	// The browse id needs a VL prefix that the caller should not have to know.
	if gotBrowseID != "VLPL123" {
		t.Errorf("browseId = %q, want VLPL123", gotBrowseID)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tracks, want 2", len(got))
	}
	want := Track{
		VideoID: "bbEtARu1YZc", Title: "her room", Artist: "Yøuth",
		Album: "her room", Duration: 2*time.Minute + 54*time.Second,
		SetVideoID: "56B44F6D",
	}
	if got[0] != want {
		t.Errorf("track[0] = %+v\nwant     %+v", got[0], want)
	}
	if got[1].Duration != time.Hour+2*time.Minute+3*time.Second {
		t.Errorf("hour-long duration = %v", got[1].Duration)
	}
}

// An id that already carries the prefix must not get a second one.
func TestPlaylistTracksDoesNotDoublePrefix(t *testing.T) {
	var gotBrowseID string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotBrowseID, _ = body["browseId"].(string)
		_, _ = w.Write([]byte(signedInBody("")))
	})
	_, _ = c.PlaylistTracks(context.Background(), "VLPL123")
	if gotBrowseID != "VLPL123" {
		t.Fatalf("browseId = %q", gotBrowseID)
	}
}

// Shelf headers and other non-track rows share the renderer; without an id
// they are not tracks.
func TestRowsWithoutAVideoIDAreSkipped(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(signedInBody(
			`{"musicResponsiveListItemRenderer":{"flexColumns":[
			  {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Top result"}]}}}]}},` +
				trackRow("Real", "A", "B", "3:00", "vid1", "set1"))))
	})
	page, err := c.PlaylistTracks(context.Background(), "PL1")
	got := page.Tracks
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Real" {
		t.Fatalf("got %+v, want only the real track", got)
	}
}

func TestSearchFindsIDOutsidePlaylistItemData(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["query"] != "xtal" {
			t.Errorf("query = %v", body["query"])
		}
		// Without the songs filter the server returns videos.
		if body["params"] != songsFilter {
			t.Errorf("params = %v, want the songs filter", body["params"])
		}
		// Search rows carry the id on the play endpoint, not playlistItemData.
		_, _ = w.Write([]byte(signedInBody(`{"musicResponsiveListItemRenderer":{
		  "flexColumns":[
		    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Xtal"}]}}},
		    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Aphex Twin"}]}}}],
		  "overlay":{"watchEndpoint":{"videoId":"3qVRuKXYFFQ"}}}}`)))
	})
	page, err := c.Search(context.Background(), "xtal")
	got := page.Tracks
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].VideoID != "3qVRuKXYFFQ" || got[0].Artist != "Aphex Twin" {
		t.Fatalf("got %+v", got)
	}
}

func TestRateUsesTheRightEndpoint(t *testing.T) {
	for _, tc := range []struct {
		rating Rating
		path   string
	}{
		{RatingUp, "/youtubei/v1/like/like"},
		{RatingDown, "/youtubei/v1/like/dislike"},
		{RatingNone, "/youtubei/v1/like/removelike"},
	} {
		var gotPath, gotVideo string
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if target, ok := body["target"].(map[string]any); ok {
				gotVideo, _ = target["videoId"].(string)
			}
			_, _ = w.Write([]byte(signedInBody("")))
		})
		if err := c.Rate(context.Background(), "vid1", tc.rating); err != nil {
			t.Fatalf("Rate(%v): %v", tc.rating, err)
		}
		if gotPath != tc.path {
			t.Errorf("rating %v hit %q, want %q", tc.rating, gotPath, tc.path)
		}
		if gotVideo != "vid1" {
			t.Errorf("rating %v sent videoId %q", tc.rating, gotVideo)
		}
	}
}

func TestRateRejectsAnEmptyID(t *testing.T) {
	c := NewClient(Session{Cookie: testCookie})
	if err := c.Rate(context.Background(), "", RatingUp); err == nil {
		t.Fatal("expected an error for an empty video id")
	}
}

func TestParseDuration(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"2:54", 2*time.Minute + 54*time.Second},
		{"0:07", 7 * time.Second},
		{"1:02:03", time.Hour + 2*time.Minute + 3*time.Second},
		{" 3:00 ", 3 * time.Minute},
		{"", 0},
		{"nonsense", 0},
		{"1:2:3:4", 0},
		{"-1:00", 0},
	} {
		if got := parseDuration(tc.in); got != tc.want {
			t.Errorf("parseDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// A track already liked has to come back that way, or the controls show an
// empty thumb for a song sitting in Liked Music.
func TestRatingIsReadFromTheResponse(t *testing.T) {
	row := func(title, status string) string {
		return `{"musicResponsiveListItemRenderer":{
		  "flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":
		    {"text":{"runs":[{"text":"` + title + `"}]}}}],
		  "menu":{"menuRenderer":{"topLevelButtons":[
		    {"likeButtonRenderer":{"likeStatus":"` + status + `"}}]}},
		  "playlistItemData":{"videoId":"` + title + `"}}}`
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(signedInBody(
			row("liked", "LIKE") + "," + row("disliked", "DISLIKE") + "," +
				row("neither", "INDIFFERENT"))))
	})
	page, err := c.PlaylistTracks(context.Background(), "PL1")
	got := page.Tracks
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	want := []Rating{RatingUp, RatingDown, RatingNone}
	if len(got) != len(want) {
		t.Fatalf("got %d tracks", len(got))
	}
	for i := range want {
		if got[i].Rating != want[i] {
			t.Errorf("%s came back %v, want %v", got[i].Title, got[i].Rating, want[i])
		}
	}
}

// A row with no like button at all is simply unrated, not an error.
func TestARowWithNoLikeStatusIsUnrated(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(signedInBody(trackRow("t", "a", "b", "3:00", "v1", "s1"))))
	})
	page, _ := c.PlaylistTracks(context.Background(), "PL1")
	got := page.Tracks
	if len(got) != 1 || got[0].Rating != RatingNone {
		t.Fatalf("got %+v", got)
	}
}

// A row links to both an album and an artist, and the two are told apart by
// where they lead rather than by which column they sit in.
func TestAlbumAndArtistIDsAreReadFromARow(t *testing.T) {
	link := func(id, pageType string) string {
		return `{"text":"x","navigationEndpoint":{"browseEndpoint":{"browseId":"` + id + `",
		  "browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":
		    {"pageType":"` + pageType + `"}}}}}`
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(signedInBody(`{"musicResponsiveListItemRenderer":{
		  "flexColumns":[
		    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Poly"}]}}},
		    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[` +
			link("UCartist", pageTypeArtist) + `]}}},
		    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[` +
			link("MPREbalbum", pageTypeAlbum) + `]}}}],
		  "playlistItemData":{"videoId":"v1"}}}`)))
	})
	page, err := c.PlaylistTracks(context.Background(), "PL1")
	got := page.Tracks
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tracks", len(got))
	}
	if got[0].AlbumID != "MPREbalbum" {
		t.Errorf("album id = %q", got[0].AlbumID)
	}
	if got[0].ArtistID != "UCartist" {
		t.Errorf("artist id = %q", got[0].ArtistID)
	}
}

// A row that links nowhere is not an error; it just has nowhere to go.
func TestARowWithNoLinksHasNoIDs(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(signedInBody(trackRow("t", "a", "b", "3:00", "v1", "s1"))))
	})
	page, _ := c.PlaylistTracks(context.Background(), "PL1")
	got := page.Tracks
	if len(got) != 1 || got[0].AlbumID != "" || got[0].ArtistID != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestAlbumAndArtistBrowseByID(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(c *Client) (Page, error)
	}{
		{"album", func(c *Client) (Page, error) {
			return c.AlbumTracks(context.Background(), "MPREbxyz")
		}},
		{"artist", func(c *Client) (Page, error) {
			return c.ArtistPage(context.Background(), "UCxyz")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotID string
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				gotID, _ = body["browseId"].(string)
				_, _ = w.Write([]byte(signedInBody(
					trackRow("Track", "Artist", "Album", "3:00", "v1", ""))))
			})
			page, err := tc.call(c)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			got := page.Tracks
			// No VL prefix: these are not playlists.
			if gotID != "MPREbxyz" && gotID != "UCxyz" {
				t.Errorf("browseId = %q", gotID)
			}
			if len(got) != 1 || got[0].Title != "Track" {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestBrowsingWithNoIDIsAnError(t *testing.T) {
	c := NewClient(Session{Cookie: testCookie})
	if _, err := c.AlbumTracks(context.Background(), ""); err == nil {
		t.Fatal("expected an error")
	}
}

// A search result packs its type, artist, album and length into one column
// separated by bullets. Reading that column whole put the bullets into the
// artist's name.
func TestASearchRowIsUnpacked(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(signedInBody(`{"musicResponsiveListItemRenderer":{
		  "flexColumns":[
		    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Xtal"}]}}},
		    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[
		      {"text":"Song"},{"text":" • "},
		      {"text":"Aphex Twin"},{"text":" • "},
		      {"text":"Selected Ambient Works 85-92"},{"text":" • "},
		      {"text":"4:51"}]}}}],
		  "playlistItemData":{"videoId":"v1"}}}`)))
	})
	page, err := c.Search(context.Background(), "xtal")
	got := page.Tracks
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results", len(got))
	}
	if got[0].Artist != "Aphex Twin" {
		t.Errorf("artist = %q", got[0].Artist)
	}
	if got[0].Album != "Selected Ambient Works 85-92" {
		t.Errorf("album = %q", got[0].Album)
	}
	// The length is in that column too, where no fixed column carries it.
	if got[0].Duration != 4*time.Minute+51*time.Second {
		t.Errorf("duration = %v", got[0].Duration)
	}
	for _, field := range []string{got[0].Artist, got[0].Album, got[0].Title} {
		if strings.Contains(field, "•") {
			t.Errorf("a bullet survived in %q", field)
		}
	}
}

// When the parts are linked, the links say which is which rather than the
// order they happen to be in.
func TestLinkedRunsNameTheArtistAndAlbum(t *testing.T) {
	link := func(text, id, pageType string) string {
		return `{"text":"` + text + `","navigationEndpoint":{"browseEndpoint":{
		  "browseId":"` + id + `","browseEndpointContextSupportedConfigs":{
		    "browseEndpointContextMusicConfig":{"pageType":"` + pageType + `"}}}}}`
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(signedInBody(`{"musicResponsiveListItemRenderer":{
		  "flexColumns":[
		    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Poly"}]}}},
		    {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[
		      {"text":"Song"},{"text":" • "},` +
			link("DAPHNI", "UCd", pageTypeArtist) + `,{"text":" • "},` +
			link("Cherry", "MPREbC", pageTypeAlbum) + `]}}}],
		  "playlistItemData":{"videoId":"v1"}}}`)))
	})
	page, _ := c.Search(context.Background(), "poly")
	got := page.Tracks
	if len(got) != 1 || got[0].Artist != "DAPHNI" || got[0].Album != "Cherry" {
		t.Fatalf("got %+v", got)
	}
}

// Twenty is one page, and the token for the rest comes back with it rather
// than being spent on the spot.
func TestAPageCarriesItsContinuation(t *testing.T) {
	var calls int
	var gotToken string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls++
		gotToken, _ = body["continuation"].(string)
		row := trackRow("page"+itoa(calls), "a", "b", "3:00", "v"+itoa(calls), "")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"responseContext":{"serviceTrackingParams":
			  [{"params":[{"key":"logged_in","value":"1"}]}]},
			  "contents":{"gridRenderer":{"items":[` + row + `]}},
			  "continuationItemRenderer":{"continuationEndpoint":
			    {"continuationCommand":{"token":"more"}}}}`))
			return
		}
		_, _ = w.Write([]byte(signedInBody(row)))
	})

	first, err := c.Search(context.Background(), "x")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if calls != 1 {
		t.Fatalf("made %d requests for one page", calls)
	}
	if !first.Next.More() {
		t.Fatal("the page does not say there is more")
	}
	if len(first.Tracks) != 1 || first.Tracks[0].Title != "page1" {
		t.Fatalf("first page = %+v", first.Tracks)
	}

	second, err := c.More(context.Background(), first.Next)
	if err != nil {
		t.Fatalf("More: %v", err)
	}
	if gotToken != "more" {
		t.Errorf("continued with %q", gotToken)
	}
	if len(second.Tracks) != 1 || second.Tracks[0].Title != "page2" {
		t.Fatalf("second page = %+v", second.Tracks)
	}
	if second.Next.More() {
		t.Error("the last page still claims there is more")
	}
}

// A search continues at search and a browse at browse; the token alone does
// not say which.
func TestAContinuationRemembersItsEndpoint(t *testing.T) {
	var paths []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"responseContext":{"serviceTrackingParams":
		  [{"params":[{"key":"logged_in","value":"1"}]}]},
		  "contents":{"gridRenderer":{"items":[]}},
		  "continuationItemRenderer":{"continuationEndpoint":
		    {"continuationCommand":{"token":"more"}}}}`))
	})

	search, _ := c.Search(context.Background(), "x")
	_, _ = c.More(context.Background(), search.Next)
	browse, _ := c.PlaylistTracks(context.Background(), "PL1")
	_, _ = c.More(context.Background(), browse.Next)

	want := []string{
		"/youtubei/v1/search", "/youtubei/v1/search",
		"/youtubei/v1/browse", "/youtubei/v1/browse",
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("request %d went to %s, want %s", i, paths[i], want[i])
		}
	}
}

// Asking to continue a finished listing is not a request.
func TestContinuingTheEndDoesNothing(t *testing.T) {
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(signedInBody("")))
	})
	page, err := c.More(context.Background(), Continuation{})
	if err != nil {
		t.Fatalf("More: %v", err)
	}
	if calls != 0 || len(page.Tracks) != 0 {
		t.Errorf("made %d requests for %d tracks", calls, len(page.Tracks))
	}
}

// An artist's page carries their releases as tiles, which the track parser
// walks past.
func TestArtistPageIncludesReleases(t *testing.T) {
	tile := func(title, id, subtitle string) string {
		return `{"musicTwoRowItemRenderer":{
		  "title":{"runs":[{"text":"` + title + `"}]},
		  "subtitle":{"runs":[{"text":"` + subtitle + `"}]},
		  "navigationEndpoint":{"browseEndpoint":{"browseId":"` + id + `",
		    "browseEndpointContextSupportedConfigs":{
		      "browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_ALBUM"}}}}}}`
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(signedInBody(
			trackRow("A Song", "DAPHNI", "Cherry", "3:00", "v1", "") + "," +
				tile("Cherry", "MPREbCherry", "Album • 2017") + "," +
				tile("Joli Mai", "MPREbJoli", "Album • 2017") + "," +
				tile("Cherry", "MPREbCherry", "Album • 2017"))))
	})

	page, err := c.ArtistPage(context.Background(), "UCd")
	got := page.Tracks
	if err != nil {
		t.Fatalf("ArtistPage: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want a song and two releases: %+v", len(got), got)
	}
	if got[0].VideoID != "v1" {
		t.Errorf("the song is not first: %+v", got[0])
	}
	// A release has nothing to play and carries the album to open instead.
	for _, release := range got[1:] {
		if release.VideoID != "" {
			t.Errorf("a release has a video id: %+v", release)
		}
		// The header of the album page comes from Album, not Title.
		if release.Album != release.Title {
			t.Errorf("a release does not carry its album name: %+v", release)
		}
		if release.AlbumID == "" {
			t.Errorf("a release has no album to open: %+v", release)
		}
	}
	if got[1].Title != "Cherry" || got[2].Title != "Joli Mai" {
		t.Errorf("releases = %q, %q", got[1].Title, got[2].Title)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// The bullet YouTube draws between parts of a field reads as a glyph that
// went wrong in the middle of a line.
func TestBulletsBecomeCommas(t *testing.T) {
	tile := func(title, id, subtitle string) string {
		return `{"musicTwoRowItemRenderer":{
		  "title":{"runs":[{"text":"` + title + `"}]},
		  "subtitle":{"runs":[{"text":"` + subtitle + `"}]},
		  "navigationEndpoint":{"browseEndpoint":{"browseId":"` + id + `",
		    "browseEndpointContextSupportedConfigs":{
		      "browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_ALBUM"}}}}}}`
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(signedInBody(tile("Cherry", "MPREbC", "Album • 2017"))))
	})
	page, err := c.ArtistPage(context.Background(), "UCd")
	if err != nil {
		t.Fatalf("ArtistPage: %v", err)
	}
	if len(page.Tracks) != 1 {
		t.Fatalf("got %d rows", len(page.Tracks))
	}
	if got := page.Tracks[0].Artist; got != "Album, 2017" {
		t.Errorf("subtitle = %q, want the bullet replaced", got)
	}
	if strings.Contains(page.Tracks[0].Artist, "•") {
		t.Error("a bullet survived")
	}
}

func TestTidy(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Album • 2017", "Album, 2017"},
		{"a•b", "a, b"},
		{"no separator", "no separator"},
		{"  padded • thing  ", "padded, thing"},
		{"", ""},
	} {
		if got := tidy(tc.in); got != tc.want {
			t.Errorf("tidy(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Fields arrive with characters a terminal cannot lay out. They take no
// width, so a column counted in runes stops lining up with one counted in
// cells.
func TestTidyStripsWhatCannotBeDrawn(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"zero width joiner", "Bo‍wie", "Bowie"},
		{"zero width space", "A​B", "AB"},
		{"direction mark", "‏Hebrew‎", "Hebrew"},
		{"soft hyphen", "co­operate", "cooperate"},
		{"control character", "Track\x07Name", "TrackName"},
		{"newline", "Two\nLines", "Two Lines"},
		{"tabs", "A\t\tB", "A B"},
		{"collapsed spaces", "  too   much   space  ", "too much space"},
		{"private use", "junkhere", "junkhere"},
		{"kept: accents", "Björk", "Björk"},
		{"kept: combining", "éclair", "éclair"},
		{"kept: cjk", "宇多田ヒカル", "宇多田ヒカル"},
		{"kept: punctuation", "Don't Stop (Remix) [2024]", "Don't Stop (Remix) [2024]"},
		{"kept: emoji", "party 🎉", "party 🎉"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tidy(tc.in); got != tc.want {
				t.Errorf("tidy(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// And it runs on every field that gets drawn.
func TestEveryFieldIsTidied(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(signedInBody(trackRow(
			"Ti‍tle", "Ar​tist", "Al­bum", "3:00", "v1", "s1"))))
	})
	page, err := c.PlaylistTracks(context.Background(), "PL1")
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	got := page.Tracks[0]
	if got.Title != "Title" || got.Artist != "Artist" || got.Album != "Album" {
		t.Errorf("got %q / %q / %q", got.Title, got.Artist, got.Album)
	}
}
