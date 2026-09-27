// Package dates parses the many date formats that appear in news markup.
//
// Indonesian and regional sites are inconsistent in a way that no single
// layout covers: ISO timestamps in <time datetime>, "26 September 2026" in
// visible text, "26/09/2026" in an attribute, and bare "2026-09-26" paths. The
// parsers here are ordered most-specific first so an unambiguous ISO value is
// never reinterpreted as a day-first date.
package dates

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Layouts tried in order against a trimmed value. Time-only and partial dates
// come last so they cannot shadow a full date.
var layouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	// Offsets written without a colon, which some CMSs emit and which the
	// RFC3339 layout rejects.
	"2006-01-02T15:04:05-0700",
	"2006-01-02T15:04:05-07:00",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006/01/02",
	"02/01/2006",
	"01/02/2006",
	"02-01-2006",
	"2006-01-02T15:04",
	"15:04 02 January 2006",
	"02 January 2006 15:04",
	"02 January 2006",
	"2 January 2006",
	"2 Jan 2006",
	"02Jan2006",
	"Jan 2, 2006",
	"January 2, 2006",
	"Jan 02, 2006 15:04",
	"January 2, 2006 15:04",
	"2 January 2006 15.04",
	"02.01.2006 15:04",
	"02.01.2006",
	"20060102",
	"20060102150405",
	"15:04",
	"15:04:05",
}

// monthNames maps the long and abbreviated month names seen in Indonesian,
// English and German copy to their index.
var monthNames = map[string]time.Month{
	"januari": time.January, "jan": time.January, "january": time.January,
	"februari": time.February, "feb": time.February, "february": time.February,
	"maret": time.March, "mar": time.March, "march": time.March,
	"april": time.April, "apr": time.April,
	"mei": time.May, "may": time.May,
	"juni": time.June, "jun": time.June, "june": time.June,
	"juli": time.July, "jul": time.July, "july": time.July,
	"agustus": time.August, "agu": time.August, "aug": time.August, "august": time.August,
	"september": time.September, "sep": time.September, "sept": time.September,
	"oktober": time.October, "okt": time.October, "oct": time.October, "october": time.October,
	"november": time.November, "nov": time.November, "n": time.November,
	"desember": time.December, "des": time.December, "dec": time.December, "december": time.December,
}

// Parse makes a best effort at reading a date. A zero time with a nil error
// means the input held no recognisable date, which is normal and must not be
// treated as a failure by callers scanning a page for one.
func Parse(s string) (time.Time, error) {
	s = clean(s)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	if t, ok := parseNamedMonth(s); ok {
		return t, nil
	}
	// A bare year is too weak to be a publication date, so it is rejected.
	if n, err := strconv.Atoi(s); err == nil && (n < 1000 || n > 9999) {
		return time.Time{}, nil
	}
	return time.Time{}, nil
}

// ParseTime is Parse for callers that treat an unrecognised value as simply
// absent, which is the common case when scanning a page for one date. A zero
// time is returned rather than an error.
func ParseTime(s string) time.Time {
	t, _ := Parse(s)
	return t
}

// clean strips the wrappers sites put around dates: parenthetical timezone
// notes, ordinal suffixes, and collapse internal whitespace.
func clean(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// "(GMT+7)" and similar trailing notes.
	if i := strings.IndexByte(s, '('); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.ReplaceAll(s, " ", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// parseNamedMonth handles a day number followed by a month name in any order
// that the layout table cannot express, such as "26 September 2026 19:30 WIB"
// or "Senin, 26 September 2026".
func parseNamedMonth(s string) (time.Time, bool) {
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return time.Time{}, false
	}
	day, month, year := -1, time.Month(0), -1
	hour, minute := 0, 0

	for _, f := range fields {
		f = strings.Trim(f, ".,:-")
		if f == "" {
			continue
		}
		if m, ok := monthNames[strings.ToLower(f)]; ok && month == 0 {
			month = m
			continue
		}
		if n, err := strconv.Atoi(f); err == nil {
			switch {
			case n >= 1000:
				year = n
			case n >= 1 && n <= 31 && day == -1:
				day = n
			case n < 24 && hour == 0 && day != -1:
				hour = n
			case n < 60:
				minute = n
			}
		}
	}
	if day == -1 || month == 0 || year < 1000 {
		return time.Time{}, false
	}
	if day > daysIn(year, month) {
		return time.Time{}, false
	}
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC), true
}

func daysIn(year int, m time.Month) int {
	return time.Date(year, m, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, -1).Day()
}

// ambiguousNumeric matches a day-first or month-first numeric date such as
// 05-09-2026. The two readings disagree, so a caller that must not guess needs
// to be able to detect this shape.
var ambiguousNumeric = regexp.MustCompile(`^(\d{1,2})[-/.](\d{1,2})[-/.](\d{4})$`)

// ParseUnambiguous parses s and refuses any input whose reading depends on a
// day-first versus month-first convention.
//
// This exists for user-typed boundaries such as a feed's date filter. Parse
// resolves 05/09/2026 as 5 September because most of the world writes dates that
// way, and that is right for a news article, where the surrounding text usually
// settles it. It is wrong for a filter box labelled YYYY-MM-DD, where silently
// reading 05-09-2026 as September when the user meant May produces a feed with a
// three-month gap in it and no indication why.
//
// A value whose first component exceeds 12 cannot be month-first, so it is
// accepted: 26-09-2026 is not a guess.
func ParseUnambiguous(s string) (time.Time, bool) {
	if m := ambiguousNumeric.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		first, err := strconv.Atoi(m[1])
		if err == nil && first <= 12 {
			return time.Time{}, false
		}
	}
	t, _ := Parse(s)
	return t, !t.IsZero()
}

// dateInText matches a date embedded in surrounding prose, which is how a
// listing card usually presents it: "26 September 2026 19:30 WIB" arrives glued
// to the headline in the same text node.
//
// The groups are tried in order from most to least specific, so an ISO date is
// never reinterpreted as a day-first slash date.
// monthAlt is the alternation of month names the search below accepts, in
// Indonesian and English, long and abbreviated.
//
// It is wrapped in a non-capturing group because it is embedded inside other
// groups: without the wrapper its own "|" operators would split them apart.
var monthAlt = `(?:Januari|Februari|Maret|April|Mei|Juni|Juli|Agustus|September|Oktober|November|Desember` +
	`|January|February|March|April|May|June|July|August|September|October|November|December` +
	`|Jan|Feb|Mar|Apr|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec)`

// dateInText matches a date embedded in surrounding prose, which is how a
// listing card usually presents it: "26 September 2026 19:30 WIB" arrives glued
// to the headline in one text node.
//
// The alternatives are ordered most specific first, so an ISO date is never
// reinterpreted as a day-first slash date. Group 2 is the slash form, and it is
// read day-first because that is how most of the world writes it.
var dateInText = regexp.MustCompile(`(?i)` +
	`(\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2})?)?)` +
	`|` + `(\d{1,2}\s+` + monthAlt + `\s+\d{4})` +
	`|` + `(` + monthAlt + `\s+\d{1,2},?\s+\d{4})` +
	`|` + `(\d{1,2}/\d{1,2}/\d{4})`)

// Find searches arbitrary text for the first date it can read and returns it.
// It is the last-resort reader for markup that has no <time> element, no date
// attribute and no separator, where the date is simply part of a sentence.
//
// The earliest match wins, so a card that leads with a date in its text is read
// as that date rather than as some number further down.
func Find(s string) (time.Time, bool) {
	for _, m := range dateInText.FindAllStringSubmatch(s, -1) {
		for _, g := range m[1:] {
			if g == "" {
				continue
			}
			if t := ParseTime(g); !t.IsZero() {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// FormatRFC1123Z renders a time the way RSS pubDate requires.
func FormatRFC1123Z(t time.Time) string { return t.UTC().Format(time.RFC1123Z) }

// FormatISO renders a time as a full ISO timestamp, for JSON feeds and
// Atom's published/updated fields.
func FormatISO(t time.Time) string { return t.UTC().Format(time.RFC3339) }
