package ytm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testCookie = "PREF=x; __Secure-3PAPISID=SECRET; SID=y"

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(Session{Cookie: testCookie, UserAgent: "test"})
	c.Now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	c.HTTP = srv.Client()
	// Point the client at the test server by rewriting the request host.
	c.HTTP.Transport = rewriteHost{base: srv.URL, rt: srv.Client().Transport}
	return c
}

// rewriteHost sends requests to the test server while leaving the path alone.
type rewriteHost struct {
	base string
	rt   http.RoundTripper
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL
	parsed := strings.TrimPrefix(r.base, "http://")
	u.Scheme, u.Host = "http", parsed
	return r.rt.RoundTrip(req)
}

// The auth hash is the whole authentication scheme; pin it against a value
// computed independently so a refactor can't quietly change it.
func TestAuthorizationHash(t *testing.T) {
	c := NewClient(Session{Cookie: testCookie})
	c.Now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	got, err := c.authorization()
	if err != nil {
		t.Fatalf("authorization: %v", err)
	}
	// sha1("1700000000 SECRET https://music.youtube.com"), computed
	// independently in Python so this pins cross-implementation agreement
	// rather than just restating what the Go code happens to do.
	const want = "SAPISIDHASH 1700000000_b775e3600857ebf92a806a130f297aaa9ddcc3f4"
	if !strings.HasPrefix(got, "SAPISIDHASH 1700000000_") {
		t.Fatalf("hash = %q, want the timestamp prefix", got)
	}
	if got != want {
		t.Fatalf("hash = %q\nwant %q", got, want)
	}
}

func TestMissingCookieIsAnError(t *testing.T) {
	c := NewClient(Session{Cookie: "PREF=x"})
	if _, err := c.authorization(); err == nil {
		t.Fatal("expected an error when __Secure-3PAPISID is absent")
	}
}

// clientVersion is date-stamped; a pinned one gets treated as signed out.
func TestClientVersionTracksTheDate(t *testing.T) {
	c := NewClient(Session{Cookie: testCookie})
	c.Now = func() time.Time { return time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC) }
	if got := c.clientVersion(); got != "1.20260923.01.00" {
		t.Fatalf("clientVersion = %q", got)
	}
}

func signedInBody(items string) string {
	return `{"responseContext":{"serviceTrackingParams":[{"params":[{"key":"logged_in","value":"1"}]}]},
	         "contents":{"gridRenderer":{"items":[` + items + `]}}}`
}

const newPlaylistTile = `{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"New playlist"}]}}}`

func playlistTile(title, id, subtitle string) string {
	return `{"musicTwoRowItemRenderer":{
		"title":{"runs":[{"text":"` + title + `"}]},
		"subtitle":{"runs":[{"text":"` + subtitle + `"}]},
		"navigationEndpoint":{"browseEndpoint":{"browseId":"` + id + `"}}}}`
}

func TestLibraryPlaylists(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "SAPISIDHASH ") {
			t.Errorf("missing auth header: %q", got)
		}
		if got := r.Header.Get("Cookie"); got != testCookie {
			t.Errorf("cookie not sent: %q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["browseId"] != "FEmusic_liked_playlists" {
			t.Errorf("browseId = %v", body["browseId"])
		}
		_, _ = w.Write([]byte(signedInBody(
			newPlaylistTile + "," +
				playlistTile("Liked Music", "VLLM", "Auto playlist") + "," +
				playlistTile("Favorites", "VLPL123", "1,590 songs"))))
	})

	got, err := c.LibraryPlaylists(context.Background())
	if err != nil {
		t.Fatalf("LibraryPlaylists: %v", err)
	}
	// The "New playlist" tile has no browseId and must not become a playlist.
	if len(got) != 2 {
		t.Fatalf("got %d playlists, want 2: %+v", len(got), got)
	}
	if got[0].ID != "LM" || got[0].Title != "Liked Music" {
		t.Errorf("first = %+v", got[0])
	}
	if got[0].Subtitle != "Auto playlist" {
		t.Errorf("subtitle = %q", got[0].Subtitle)
	}
	// The VL prefix is a browse id, not a playlist id.
	if got[1].ID != "PL123" {
		t.Errorf("second id = %q, want PL123", got[1].ID)
	}
}

// A signed-out response is HTTP 200 with an empty result. Reporting that as
// success is what makes a dead session look like an empty library.
func TestSignedOutIsAnError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"responseContext":{"serviceTrackingParams":
			[{"params":[{"key":"logged_in","value":"0"}]}]},"contents":{}}`))
	})
	_, err := c.LibraryPlaylists(context.Background())
	if !errors.Is(err, ErrSignedOut) {
		t.Fatalf("err = %v, want ErrSignedOut", err)
	}
}

func TestHTTPErrorIsReported(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if _, err := c.LibraryPlaylists(context.Background()); err == nil {
		t.Fatal("expected an error on HTTP 503")
	}
}

func TestRunsTextConcatenates(t *testing.T) {
	var node any
	_ = json.Unmarshal([]byte(`{"runs":[{"text":"a"},{"text":" & "},{"text":"b"}]}`), &node)
	if got := runsText(node); got != "a & b" {
		t.Fatalf("runsText = %q", got)
	}
	if got := runsText(nil); got != "" {
		t.Fatalf("runsText(nil) = %q", got)
	}
}

func TestFindAllWalksNestedRenderers(t *testing.T) {
	var tree any
	_ = json.Unmarshal([]byte(`{"a":{"b":[{"target":1},{"c":{"target":2}}]},"target":3}`), &tree)
	if got := findAll(tree, "target"); len(got) != 3 {
		t.Fatalf("found %d, want 3: %v", len(got), got)
	}
}
