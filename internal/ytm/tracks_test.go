package ytm

import (
	"context"
	"encoding/json"
	"net/http"
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
