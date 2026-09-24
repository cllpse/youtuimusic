package ytm

import (
	"strconv"
	"strings"
	"time"
)

// addedOn reads the date a track was added to a library listing.
//
// It is not in a column of its own everywhere — a playlist has three
// columns where a library listing can have four — so the columns past the
// album are searched for something that reads as a date rather than a fixed
// index being trusted. A listing that does not carry one gives back the
// zero time, and the column is then not drawn at all.
func addedOn(item map[string]any, now func() time.Time) time.Time {
	for i := 2; i < 6; i++ {
		text := tidy(flexColumn(item, i))
		if text == "" {
			continue
		}
		if when, ok := parseAdded(text, now()); ok {
			return when
		}
	}
	return time.Time{}
}

// relativeUnits are the spans YouTube counts back in.
var relativeUnits = map[string]time.Duration{
	"second": time.Second,
	"minute": time.Minute,
	"hour":   time.Hour,
	"day":    24 * time.Hour,
	"week":   7 * 24 * time.Hour,
	"month":  30 * 24 * time.Hour,
	"year":   365 * 24 * time.Hour,
}

// dateLayouts are the absolute forms seen in these responses. The server
// picks one by locale, so several are tried rather than one assumed.
var dateLayouts = []string{
	"Jan 2, 2006",
	"January 2, 2006",
	"2 Jan 2006",
	"2 January 2006",
	"2006-01-02",
	"02/01/2006",
	"1/2/2006",
}

// parseAdded reads either form a date arrives in: counted back from now
// ("5 days ago"), or written out.
func parseAdded(s string, now time.Time) (time.Time, bool) {
	if when, ok := parseRelative(s, now); ok {
		return when, true
	}
	for _, layout := range dateLayouts {
		if when, err := time.Parse(layout, s); err == nil {
			return when, true
		}
	}
	return time.Time{}, false
}

func parseRelative(s string, now time.Time) (time.Time, bool) {
	fields := strings.Fields(strings.ToLower(s))
	if len(fields) != 3 || fields[2] != "ago" {
		return time.Time{}, false
	}
	// "a day ago" counts as one.
	count := 1
	if fields[0] != "a" && fields[0] != "an" {
		n, err := strconv.Atoi(fields[0])
		if err != nil || n < 0 {
			return time.Time{}, false
		}
		count = n
	}
	unit, ok := relativeUnits[strings.TrimSuffix(fields[1], "s")]
	if !ok {
		return time.Time{}, false
	}
	return now.Add(-time.Duration(count) * unit), true
}
