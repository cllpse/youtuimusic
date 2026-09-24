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

	got, err := c.PlaylistTracks(context.Background(), "PL123")
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
	got, err := c.PlaylistTracks(context.Background(), "PL1")
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
	got, err := c.Search(context.Background(), "xtal")
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
	got, err := c.PlaylistTracks(context.Background(), "PL1")
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
	got, _ := c.PlaylistTracks(context.Background(), "PL1")
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
	got, err := c.PlaylistTracks(context.Background(), "PL1")
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
	got, _ := c.PlaylistTracks(context.Background(), "PL1")
	if len(got) != 1 || got[0].AlbumID != "" || got[0].ArtistID != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestAlbumAndArtistBrowseByID(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(c *Client) ([]Track, error)
	}{
		{"album", func(c *Client) ([]Track, error) {
			return c.AlbumTracks(context.Background(), "MPREbxyz")
		}},
		{"artist", func(c *Client) ([]Track, error) {
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
			got, err := tc.call(c)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
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
	got, err := c.Search(context.Background(), "xtal")
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
	got, _ := c.Search(context.Background(), "poly")
	if len(got) != 1 || got[0].Artist != "DAPHNI" || got[0].Album != "Cherry" {
		t.Fatalf("got %+v", got)
	}
}

// Twenty is one page. The rest come back behind a token.
func TestSearchFollowsContinuations(t *testing.T) {
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls++
		row := trackRow("page"+itoa(calls), "a", "b", "3:00", "v"+itoa(calls), "")
		// Hand out a token twice, then stop.
		if calls < 3 {
			_, _ = w.Write([]byte(`{"responseContext":{"serviceTrackingParams":
			  [{"params":[{"key":"logged_in","value":"1"}]}]},
			  "contents":{"gridRenderer":{"items":[` + row + `]}},
			  "continuationItemRenderer":{"continuationEndpoint":
			    {"continuationCommand":{"token":"more` + itoa(calls) + `"}}}}`))
			return
		}
		_, _ = w.Write([]byte(signedInBody(row)))
	})

	got, err := c.Search(context.Background(), "x")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if calls != 3 {
		t.Errorf("made %d requests, want 3", calls)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results across the pages: %+v", len(got), got)
	}
	if got[2].Title != "page3" {
		t.Errorf("the last page is %q", got[2].Title)
	}
}

// A listing that never stops handing out tokens has to be cut off.
func TestContinuationsAreCapped(t *testing.T) {
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"responseContext":{"serviceTrackingParams":
		  [{"params":[{"key":"logged_in","value":"1"}]}]},
		  "contents":{"gridRenderer":{"items":[` +
			trackRow("t", "a", "b", "3:00", "v"+itoa(calls), "") + `]}},
		  "continuationItemRenderer":{"continuationEndpoint":
		    {"continuationCommand":{"token":"endless"}}}}`))
	})
	if _, err := c.Search(context.Background(), "x"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if calls != maxPages {
		t.Errorf("made %d requests, want the cap of %d", calls, maxPages)
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

	got, err := c.ArtistPage(context.Background(), "UCd")
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
		if release.AlbumID == "" {
			t.Errorf("a release has no album to open: %+v", release)
		}
	}
	if got[1].Title != "Cherry" || got[2].Title != "Joli Mai" {
		t.Errorf("releases = %q, %q", got[1].Title, got[2].Title)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
