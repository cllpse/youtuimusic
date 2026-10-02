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
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	return "", fmt.Errorf("%w: no __Secure-3PAPISID cookie", ErrSignedOut)
}

// Client talks to InnerTube.
type Client struct {
	HTTP *http.Client
	// Now is overridable so the auth hash and the refresh interval are
	// testable.
	Now func() time.Time

	// Refresh, when set, fetches a current session to retry with when a
	// request comes back signed out.
	//
	// The cookie Google authenticates with rotates about every ten minutes
	// and the API never hands back a replacement; only the browser gets one.
	// So a session read at startup dies some time into a long listen, and
	// the browser's newer copy is what revives it.
	Refresh func() (Session, error)

	// mu guards the fields below. It is only ever held for a moment: a
	// refresh can wait minutes on a keyring prompt, and every request needs
	// the session to start.
	mu          sync.Mutex
	session     Session
	lastRefresh time.Time
	// refreshing is open while a Refresh runs and closed when it ends, so
	// requests that fail meanwhile wait for its answer instead of asking the
	// browser again.
	refreshing chan struct{}
}

// refreshInterval is how often a signed-out answer may send the client back
// to the browser. A session that is gone for good — signed out in the
// browser too — would otherwise cost a keyring read on every request.
const refreshInterval = 30 * time.Second

// NewClient returns a Client with a connection-pooling HTTP client. The pool
// matters: the first request of a session pays a TLS handshake worth roughly
// half a second, and every later one reuses it.
func NewClient(s Session) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		session: s,
		Now:     time.Now,
	}
}

// ErrSignedOut means the server accepted the request but treated it as
// anonymous. It is reported separately because YouTube answers a signed-out
// library request with HTTP 200 and an empty result, which is otherwise
// indistinguishable from an account that genuinely has no playlists.
var ErrSignedOut = errors.New("ytm: request was not authenticated")

// current is the session to send with, taken whole so one request does not
// mix two of them.
func (c *Client) current() Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

// errNothingNewer means a refresh was not worth retrying with: it was asked
// for too recently, or the browser held the same session.
var errNothingNewer = errors.New("no newer session")

// refreshed returns a session to retry with after stale was turned away.
//
// Several requests can fail on the same stale session at once. The first
// runs Refresh, outside the lock; the rest wait for it — or for their own
// context — and then retry with whatever it found.
func (c *Client) refreshed(ctx context.Context, stale Session) (Session, error) {
	for {
		c.mu.Lock()
		if c.session.Cookie != stale.Cookie {
			s := c.session
			c.mu.Unlock()
			return s, nil
		}
		if wait := c.refreshing; wait != nil {
			c.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return Session{}, ctx.Err()
			}
		}
		if c.Refresh == nil || c.Now().Sub(c.lastRefresh) < refreshInterval {
			c.mu.Unlock()
			return Session{}, errNothingNewer
		}
		c.lastRefresh = c.Now()
		done := make(chan struct{})
		c.refreshing = done
		c.mu.Unlock()

		fresh, err := c.Refresh()
		newer := err == nil && fresh.Cookie != "" && fresh.Cookie != stale.Cookie

		c.mu.Lock()
		if newer {
			c.session = fresh
		}
		c.refreshing = nil
		close(done)
		c.mu.Unlock()

		switch {
		case err != nil:
			return Session{}, err
		case !newer:
			return Session{}, errNothingNewer
		}
		return fresh, nil
	}
}

// sign builds the SAPISIDHASH header value for a session.
func (c *Client) sign(s Session) (string, error) {
	sapisid, err := s.sapisid()
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

// post sends a request and returns the parsed response tree. A request
// turned away as signed out is retried once with a refreshed session; when
// the refresh itself failed, that is said alongside, since "signed out"
// alone would hide a locked keyring.
func (c *Client) post(ctx context.Context, endpoint string, body map[string]any) (map[string]any, error) {
	session := c.current()
	tree, err := c.send(ctx, session, endpoint, body)
	if !errors.Is(err, ErrSignedOut) || c.Refresh == nil {
		return tree, err
	}
	fresh, refreshErr := c.refreshed(ctx, session)
	switch {
	case errors.Is(refreshErr, errNothingNewer):
		return tree, err
	case refreshErr != nil:
		return tree, fmt.Errorf("%w (reading the browser again: %w)", err, refreshErr)
	}
	return c.send(ctx, fresh, endpoint, body)
}

// send makes one request with one session. It decodes exactly once: every
// reader of an InnerTube response searches the same tree, and unmarshalling
// per reader is what made a mix page cost four passes.
//
// Compression is left to the transport, which asks for gzip and undoes it
// on its own as long as the request does not set Accept-Encoding itself.
func (c *Client) send(ctx context.Context, session Session, endpoint string, body map[string]any) (map[string]any, error) {
	authz, err := c.sign(session)
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
	if session.Cookie != "" {
		req.Header.Set("Cookie", session.Cookie)
	}
	req.Header.Set("Authorization", authz)
	req.Header.Set("Origin", origin)
	if session.UserAgent != "" {
		req.Header.Set("User-Agent", session.UserAgent)
	}
	if session.VisitorID != "" {
		req.Header.Set("X-Goog-Visitor-Id", session.VisitorID)
	}
	if session.AuthUser != "" {
		req.Header.Set("X-Goog-AuthUser", session.AuthUser)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ytm: %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Drained so the connection goes back to the pool.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		// A signature the server will not accept is a 401 rather than an
		// anonymous answer, and it means the same thing.
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w: %s: HTTP 401", ErrSignedOut, endpoint)
		}
		return nil, fmt.Errorf("ytm: %s: HTTP %d", endpoint, resp.StatusCode)
	}

	var tree map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
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
// this function used to cost. The walk starts at responseContext, where the
// parameter lives; from the root, map order would send it through the whole
// page first about half the time.
func loggedIn(tree map[string]any) bool {
	from, ok := tree["responseContext"]
	if !ok {
		from = tree
	}
	value, ok := findParam(from, "logged_in")
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
		id, _ := browseTile(item)
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
