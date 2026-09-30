// Package ytm is a minimal client for YouTube Music's private InnerTube API.
//
// There is no official API and no Go library that authenticates against it, so
// this implements the parts we need directly: library playlists, a playlist's
// tracks, search, and rating a track. That is the whole surface — which is why
// writing it is tractable where porting a general-purpose client would not be.
//
// Authentication is cookie-based. Requests carry a SAPISIDHASH header derived
// from the __Secure-3PAPISID cookie and the origin; this was verified against
// a real account before any of it was written, returning logged_in=1 and the
// full library.
package ytm

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	baseURL = "https://music.youtube.com/youtubei/v1/"
	origin  = "https://music.youtube.com"
)

// Session is the credential material a signed-in request needs.
type Session struct {
	Cookie    string // the whole Cookie header
	UserAgent string
	VisitorID string
	AuthUser  string
}

// sapisid pulls the cookie the auth hash is derived from.
func (s Session) sapisid() (string, error) {
	for _, part := range strings.Split(s.Cookie, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && name == "__Secure-3PAPISID" {
			return value, nil
		}
	}
	return "", errors.New("ytm: no __Secure-3PAPISID cookie — not signed in")
}

// Client talks to InnerTube.
type Client struct {
	HTTP    *http.Client
	Session Session
	// Now is overridable so the auth hash is testable.
	Now func() time.Time
}

// NewClient returns a Client with a connection-pooling HTTP client. The pool
// matters: the first request of a session pays a TLS handshake worth roughly
// half a second, and every later one reuses it.
func NewClient(s Session) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Session: s,
		Now:     time.Now,
	}
}

// ErrSignedOut means the server accepted the request but treated it as
// anonymous. It is reported separately because YouTube answers a signed-out
// library request with HTTP 200 and an empty result, which is otherwise
// indistinguishable from an account that genuinely has no playlists.
var ErrSignedOut = errors.New("ytm: request was not authenticated")

// authorization builds the SAPISIDHASH header value.
func (c *Client) authorization() (string, error) {
	sapisid, err := c.Session.sapisid()
	if err != nil {
		return "", err
	}
	ts := strconv.FormatInt(c.Now().Unix(), 10)
	sum := sha1.Sum([]byte(ts + " " + sapisid + " " + origin))
	return "SAPISIDHASH " + ts + "_" + fmt.Sprintf("%x", sum), nil
}

// clientVersion is date-stamped. A stale one gets the request treated as
// signed out, so it is derived rather than pinned.
func (c *Client) clientVersion() string {
	return "1." + c.Now().Format("20060102") + ".01.00"
}

// post sends a request and returns the parsed response tree. It unmarshals
// exactly once: every reader of an InnerTube response searches the same tree,
// and unmarshalling per reader is what made a mix page cost four passes.
func (c *Client) post(ctx context.Context, endpoint string, body map[string]any) (map[string]any, error) {
	authz, err := c.authorization()
	if err != nil {
		return nil, err
	}

	body["context"] = map[string]any{
		"client": map[string]any{
			"clientName":    "WEB_REMIX",
			"clientVersion": c.clientVersion(),
			"hl":            "en",
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+endpoint+"?alt=json", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Session.Cookie != "" {
		req.Header.Set("Cookie", c.Session.Cookie)
	}
	req.Header.Set("Authorization", authz)
	req.Header.Set("Origin", origin)
	req.Header.Set("Accept-Encoding", "gzip")
	if c.Session.UserAgent != "" {
		req.Header.Set("User-Agent", c.Session.UserAgent)
	}
	if c.Session.VisitorID != "" {
		req.Header.Set("X-Goog-Visitor-Id", c.Session.VisitorID)
	}
	if c.Session.AuthUser != "" {
		req.Header.Set("X-Goog-AuthUser", c.Session.AuthUser)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ytm: %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("ytm: %s: gzip: %w", endpoint, err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("ytm: %s: read: %w", endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ytm: %s: HTTP %d", endpoint, resp.StatusCode)
	}

	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("ytm: %s: parse: %w", endpoint, err)
	}
	if !loggedIn(tree) {
		return tree, ErrSignedOut
	}
	return tree, nil
}

// loggedIn reads the server's own view of the session out of the response
// tree. Trusting this rather than the presence of cookies is deliberate:
// cookies can be present, unexpired and still rejected, which is exactly how a
// dead session hides as "you have no playlists".
//
// It walks for the one parameter rather than unmarshalling an envelope: the
// tree is already in hand, and a second parse of a multi-megabyte page is what
// this function used to cost.
func loggedIn(tree any) bool {
	value, ok := findParam(tree, "logged_in")
	return ok && value == "1"
}

// findParam finds the value of a serviceTrackingParams entry by key.
func findParam(node any, key string) (string, bool) {
	switch v := node.(type) {
	case map[string]any:
		if k, _ := v["key"].(string); k == key {
			value, _ := v["value"].(string)
			return value, true
		}
		for _, child := range v {
			if value, ok := findParam(child, key); ok {
				return value, true
			}
		}
	case []any:
		for _, child := range v {
			if value, ok := findParam(child, key); ok {
				return value, true
			}
		}
	}
	return "", false
}

// Playlist is an entry in the library sidebar.
type Playlist struct {
	ID       string
	Title    string
	Subtitle string
}

// LibraryPlaylists returns the playlists in the signed-in user's library.
func (c *Client) LibraryPlaylists(ctx context.Context) ([]Playlist, error) {
	tree, err := c.post(ctx, "browse", map[string]any{"browseId": "FEmusic_liked_playlists"})
	if err != nil {
		return nil, err
	}

	var out []Playlist
	for _, node := range findAll(tree, "musicTwoRowItemRenderer") {
		item, _ := node.(map[string]any)
		id := browseID(item)
		// The grid leads with a "New playlist" tile that has no browseId.
		if id == "" {
			continue
		}
		out = append(out, Playlist{
			ID:       strings.TrimPrefix(id, "VL"),
			Title:    tidy(runsText(item["title"])),
			Subtitle: tidy(runsText(item["subtitle"])),
		})
	}
	return out, nil
}

func browseID(item map[string]any) string {
	nav, _ := item["navigationEndpoint"].(map[string]any)
	be, _ := nav["browseEndpoint"].(map[string]any)
	id, _ := be["browseId"].(string)
	return id
}

// runsText flattens InnerTube's {"runs":[{"text":...}]} shape.
func runsText(node any) string {
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	runs, ok := m["runs"].([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, r := range runs {
		if rm, ok := r.(map[string]any); ok {
			if t, ok := rm["text"].(string); ok {
				b.WriteString(t)
			}
		}
	}
	return b.String()
}

// findAll walks the response for every value stored under key. InnerTube
// nests renderers at unpredictable depths, so searching beats hard-coding a
// path that changes whenever the server's layout does.
func findAll(node any, key string) []any {
	var out []any
	var walk func(any)
	walk = func(n any) {
		switch v := n.(type) {
		case map[string]any:
			if hit, ok := v[key]; ok {
				out = append(out, hit)
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(node)
	return out
}

// findFirst is findAll for the many callers that stop at the first hit. It
// does not build the slice findAll would, which matters when the hit is found
// near the top of a multi-megabyte page.
func findFirst(node any, key string) (any, bool) {
	switch v := node.(type) {
	case map[string]any:
		if hit, ok := v[key]; ok {
			return hit, true
		}
		for _, child := range v {
			if hit, ok := findFirst(child, key); ok {
				return hit, true
			}
		}
	case []any:
		for _, child := range v {
			if hit, ok := findFirst(child, key); ok {
				return hit, true
			}
		}
	}
	return nil, false
}
