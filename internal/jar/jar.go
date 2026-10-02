// Package jar is what the browser readers have in common: a cookie, the set
// of them one profile would send to a host, and which of several profiles
// holds the session worth using.
//
// internal/chromium and internal/gecko only know how to get cookies out of
// their own store. Deciding which cookies a request would carry, and which
// profile is signed in, is the same question for both, so it is answered once
// here.
package jar

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Cookie is one stored cookie.
type Cookie struct {
	Name  string
	Value string
	// Host is the domain the cookie is filed under. A leading dot means it
	// covers subdomains, which is how the account cookies reach
	// music.youtube.com from .youtube.com.
	Host string
	// Path is the cookie's path attribute; empty is treated as "/".
	Path    string
	Expires time.Time // zero for a session cookie
	// LastAccess is when the browser last sent the cookie, zero when the
	// store does not say.
	LastAccess time.Time
}

// The two cookies a usable YouTube session has.
const (
	// Bellwether is the cookie Google authenticates with. Measured: remove it
	// and a library request comes back signed out, while removing
	// __Secure-3PSIDTS or the SIDCC family changes nothing.
	Bellwether = "__Secure-1PSIDTS"
	// APISID is the cookie every request is signed with (see
	// ytm.Session.sapisid). A profile without it cannot make a request, so
	// it is not signed in in any sense that matters here.
	APISID = "__Secure-3PAPISID"
)

// Sends reports whether a cookie filed under hostKey would be sent to host.
func Sends(hostKey, host string) bool {
	hostKey, host = strings.ToLower(hostKey), strings.ToLower(host)
	if strings.HasPrefix(hostKey, ".") {
		return host == hostKey[1:] || strings.HasSuffix(host, hostKey)
	}
	return host == hostKey
}

// RequestPath is the path every InnerTube request goes to. A cookie scoped
// to some other path is not sent with them.
const RequestPath = "/youtubei/v1/"

// PathMatches is RFC 6265's path-match: the cookie's path is the request's,
// or a prefix of it ending at a slash.
func PathMatches(cookiePath, requestPath string) bool {
	if cookiePath == "" || cookiePath == "/" {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	return len(requestPath) == len(cookiePath) ||
		strings.HasSuffix(cookiePath, "/") ||
		requestPath[len(cookiePath)] == '/'
}

// Valid reports whether a value could be a cookie at all: printable ASCII
// with no separator. It is what catches a wrong decryption key. A wrong key
// still produces a plausible padding byte about once in 256 tries, and what
// comes out of that is noise, which this rejects.
func Valid(value string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x20 || c > 0x7e || c == ';' {
			return false
		}
	}
	return true
}

// Jar is the cookies one browser profile would send to a host.
type Jar struct {
	Browser string
	Profile string
	Cookies []Cookie
	// Modified is when the store was last written. It stands in for a
	// cookie's LastAccess when the store does not record one.
	Modified time.Time
}

// StoreModified is when a cookie store was last written: its write-ahead log
// counts, since that is where both engines' writes land first. It is what
// Jar.Modified is filled with.
func StoreModified(path string) time.Time {
	var at time.Time
	for _, p := range []string{path, path + "-wal"} {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(at) {
			at = info.ModTime()
		}
	}
	return at
}

// Name is the browser and profile, for messages.
func (j Jar) Name() string { return j.Browser + "/" + j.Profile }

func (j Jar) has(name string) (Cookie, bool) {
	for _, c := range j.Cookies {
		if c.Name == name {
			return c, true
		}
	}
	return Cookie{}, false
}

// SignedIn reports whether the jar holds a session a request can use.
func (j Jar) SignedIn() bool {
	_, session := j.has(Bellwether)
	_, signing := j.has(APISID)
	return session && signing
}

// freshness is when the session in this jar was last used. Google rotates
// the bellwether while a tab is open, so the profile that touched it last is
// the one whose copy is current.
func (j Jar) freshness() time.Time {
	if c, ok := j.has(Bellwether); ok && !c.LastAccess.IsZero() {
		return c.LastAccess
	}
	return j.Modified
}

// Header renders the jar as a Cookie request header.
//
// A name can be stored more than once: under .youtube.com and
// music.youtube.com, or under two paths. A browser sends both, but the server
// reads the first, so only the most specific is kept rather than leaving the
// choice to whichever came out of the table first.
func (j Jar) Header() string {
	best := map[string]Cookie{}
	for _, c := range j.Cookies {
		if prev, ok := best[c.Name]; !ok || moreSpecific(c, prev) {
			best[c.Name] = c
		}
	}
	names := make([]string, 0, len(best))
	for name := range best {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+best[name].Value)
	}
	return strings.Join(parts, "; ")
}

// moreSpecific orders two cookies of the same name: the longer path first,
// as browsers send them, then a host-only cookie over a domain one, then the
// narrower domain.
func moreSpecific(a, b Cookie) bool {
	if len(a.Path) != len(b.Path) {
		return len(a.Path) > len(b.Path)
	}
	aDomain, bDomain := strings.HasPrefix(a.Host, "."), strings.HasPrefix(b.Host, ".")
	if aDomain != bDomain {
		return !aDomain
	}
	return len(a.Host) > len(b.Host)
}

// ErrNoSession means no profile held a session a request can use.
var ErrNoSession = errors.New("no signed-in browser profile found")

// Pick chooses the profile to use: of the signed-in ones, the one whose
// session was used last. Ties keep the order given, so a caller can still say
// which engine it trusts more when the stores cannot tell them apart.
//
// Picking by freshness rather than by browser matters on a machine with two
// browsers signed in: the one left signed in a month ago still has the
// cookies, and they still look valid, but Google stopped rotating them.
func Pick(jars []Jar) (Jar, error) {
	var picked Jar
	found := false
	for _, j := range jars {
		if !j.SignedIn() {
			continue
		}
		if !found || j.freshness().After(picked.freshness()) {
			picked, found = j, true
		}
	}
	if found {
		return picked, nil
	}

	// Say what was nearly there, so "signed out" and "half a session" are
	// not the same message.
	for _, j := range jars {
		if _, ok := j.has(Bellwether); ok {
			return Jar{}, fmt.Errorf("%w: %s has %s but no %s",
				ErrNoSession, j.Name(), Bellwether, APISID)
		}
	}
	var seen []string
	for _, j := range jars {
		if len(j.Cookies) > 0 {
			seen = append(seen, j.Name())
		}
	}
	if len(seen) > 0 {
		return Jar{}, fmt.Errorf("%w: %s %s YouTube cookies but no %s",
			ErrNoSession, strings.Join(seen, ", "), plural(len(seen), "has", "have"), Bellwether)
	}
	return Jar{}, ErrNoSession
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
