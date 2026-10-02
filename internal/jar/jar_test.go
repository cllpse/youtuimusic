package jar

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSendsFollowsCookieScoping(t *testing.T) {
	cases := []struct {
		hostKey, host string
		want          bool
	}{
		{".youtube.com", "music.youtube.com", true},
		{".youtube.com", "youtube.com", true},
		{"music.youtube.com", "music.youtube.com", true},
		{"music.youtube.com", "youtube.com", false},
		{"www.youtube.com", "music.youtube.com", false},
		{".google.com", "music.youtube.com", false},
		{".youtube.com", "notyoutube.com", false},
	}
	for _, c := range cases {
		if got := Sends(c.hostKey, c.host); got != c.want {
			t.Errorf("Sends(%q, %q) = %v, want %v", c.hostKey, c.host, got, c.want)
		}
	}
}

func TestPathMatches(t *testing.T) {
	cases := []struct {
		cookie string
		want   bool
	}{
		{"", true},
		{"/", true},
		{"/youtubei", true},
		{"/youtubei/", true},
		{"/youtubei/v1/", true},
		{"/youtube", false}, // a prefix, but not at a slash
		{"/watch", false},
		{"/youtubei/v2/", false},
	}
	for _, c := range cases {
		if got := PathMatches(c.cookie, RequestPath); got != c.want {
			t.Errorf("PathMatches(%q) = %v, want %v", c.cookie, got, c.want)
		}
	}
}

func TestValid(t *testing.T) {
	for _, v := range []string{"", "abc", "AJi4QfE-x_y.z/1=", `"quoted"`} {
		if !Valid(v) {
			t.Errorf("Valid(%q) = false", v)
		}
	}
	for _, v := range []string{"a;b", "tab\t", "\x00", "caf\xc3\xa9", "\x7f"} {
		if Valid(v) {
			t.Errorf("Valid(%q) = true", v)
		}
	}
}

// The most specific copy of a name is the one sent: the longer path, then
// a host-only cookie over a domain one.
func TestHeaderKeepsTheMostSpecific(t *testing.T) {
	j := Jar{Cookies: []Cookie{
		{Name: "A", Value: "domain", Host: ".youtube.com", Path: "/"},
		{Name: "A", Value: "host", Host: "music.youtube.com", Path: "/"},
		{Name: "B", Value: "short", Host: "music.youtube.com", Path: "/"},
		{Name: "B", Value: "long", Host: ".youtube.com", Path: "/youtubei"},
		{Name: "C", Value: "only", Host: ".youtube.com"},
	}}
	if got, want := j.Header(), "A=host; B=long; C=only"; got != want {
		t.Errorf("Header = %q, want %q", got, want)
	}
}

func signedIn(browser string, used time.Time) Jar {
	return Jar{Browser: browser, Profile: "Default", Cookies: []Cookie{
		{Name: Bellwether, Value: browser, LastAccess: used},
		{Name: APISID, Value: "x"},
	}}
}

// Of two signed-in profiles, the one whose session was used last wins,
// wherever it is in the list.
func TestPickPrefersTheFreshestSession(t *testing.T) {
	now := time.Now()
	got, err := Pick([]Jar{signedIn("Old", now.Add(-30*24*time.Hour)), signedIn("New", now)})
	if err != nil || got.Browser != "New" {
		t.Fatalf("Pick = %s, %v; want New", got.Name(), err)
	}
}

// Without access times, the store's own write time decides; with neither,
// the first in the list does.
func TestPickFallsBackToTheStoreThenTheOrder(t *testing.T) {
	now := time.Now()
	a, b := signedIn("A", time.Time{}), signedIn("B", time.Time{})
	if got, _ := Pick([]Jar{a, b}); got.Browser != "A" {
		t.Errorf("tie went to %s, want the first", got.Browser)
	}
	b.Modified = now
	if got, _ := Pick([]Jar{a, b}); got.Browser != "B" {
		t.Errorf("picked %s, want the store written last", got.Browser)
	}
}

// A profile that lacks either cookie is not signed in, however many others
// it holds, and the error says which half was missing.
func TestPickRejectsHalfASession(t *testing.T) {
	var noise []Cookie
	for i := 0; i < 8; i++ {
		noise = append(noise, Cookie{Name: "JUNK", Value: "x"})
	}
	half := Jar{Browser: "Half", Profile: "Default", Cookies: []Cookie{{Name: Bellwether, Value: "x"}}}
	_, err := Pick([]Jar{{Browser: "Noisy", Profile: "Default", Cookies: noise}, half})
	if !errors.Is(err, ErrNoSession) || !strings.Contains(err.Error(), APISID) {
		t.Errorf("err = %v, want one naming the missing %s", err, APISID)
	}

	_, err = Pick([]Jar{{Browser: "Noisy", Profile: "Default", Cookies: noise}})
	if !errors.Is(err, ErrNoSession) || !strings.Contains(err.Error(), "Noisy/Default") {
		t.Errorf("err = %v, want one naming the signed-out profile", err)
	}

	if _, err := Pick(nil); !errors.Is(err, ErrNoSession) {
		t.Errorf("err = %v, want ErrNoSession", err)
	}
}
