package ytm

import (
	"context"
	"net/http"
	"testing"
	"time"
)

var refNow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func TestParseAdded(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"5 days ago", refNow.Add(-5 * 24 * time.Hour), true},
		{"a day ago", refNow.Add(-24 * time.Hour), true},
		{"an hour ago", refNow.Add(-time.Hour), true},
		{"1 week ago", refNow.Add(-7 * 24 * time.Hour), true},
		{"3 years ago", refNow.Add(-3 * 365 * 24 * time.Hour), true},
		{"Aug 2, 2024", time.Date(2024, 8, 2, 0, 0, 0, 0, time.UTC), true},
		{"2 August 2024", time.Date(2024, 8, 2, 0, 0, 0, 0, time.UTC), true},
		{"2024-08-02", time.Date(2024, 8, 2, 0, 0, 0, 0, time.UTC), true},
		{"Selected Ambient Works", time.Time{}, false},
		{"3:42", time.Time{}, false},
		{"", time.Time{}, false},
		{"ago", time.Time{}, false},
		{"-2 days ago", time.Time{}, false},
	} {
		got, ok := parseAdded(tc.in, refNow)
		if ok != tc.ok {
			t.Errorf("parseAdded(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && !got.Equal(tc.want) {
			t.Errorf("parseAdded(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// A row that carries a date gives it up whichever column it is in; one that
// does not is simply undated.
func TestAddedIsFoundWhereverItSits(t *testing.T) {
	column := func(text string) string {
		return `{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"` + text + `"}]}}}`
	}
	row := func(columns ...string) string {
		return `{"musicResponsiveListItemRenderer":{"flexColumns":[` +
			joinAll(columns) + `],"playlistItemData":{"videoId":"v1"}}}`
	}
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"fourth column", row(column("Title"), column("Artist"), column("Album"), column("5 days ago")), true},
		{"third column", row(column("Title"), column("Artist"), column("Aug 2, 2024")), true},
		{"no date at all", row(column("Title"), column("Artist"), column("Album")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(signedInBody(tc.body)))
			})
			c.Now = func() time.Time { return refNow }
			page, err := c.PlaylistTracks(context.Background(), "PL1")
			if err != nil {
				t.Fatalf("PlaylistTracks: %v", err)
			}
			if len(page.Tracks) != 1 {
				t.Fatalf("got %d rows", len(page.Tracks))
			}
			if got := !page.Tracks[0].Added.IsZero(); got != tc.want {
				t.Errorf("has a date = %v, want %v (%v)", got, tc.want, page.Tracks[0].Added)
			}
		})
	}
}

func joinAll(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
