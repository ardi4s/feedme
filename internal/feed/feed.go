// Package feed renders a list of items as RSS 2.0, Atom 1.0 or JSON Feed.
//
// The three formats share one intermediate representation so a rule added for
// one is correct in all of them: a title that needs escaping, a date that must
// be RFC 1123, an item that has no GUID, and a description that has to fall
// back to a truncated plain-text form when no full text was fetched.
package feed

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"feedme/internal/domx"
	"feedme/internal/listpage"
	"feedme/internal/urlx"
)

// Format identifies an output format.
type Format string

const (
	FormatRSS  Format = "rss"
	FormatAtom Format = "atom"
	FormatJSON Format = "json"
)

// Formats lists the supported formats in the order a UI should offer them.
var Formats = []Format{FormatRSS, FormatAtom, FormatJSON}

// Valid reports whether f is a format this package can render.
func Valid(f Format) bool {
	for _, known := range Formats {
		if f == known {
			return true
		}
	}
	return false
}

// GUIDMode selects how an item's identity is derived.
type GUIDMode string

const (
	// GUIDLink uses the item URL. The default, and the only mode every reader
	// agrees on.
	GUIDLink GUIDMode = "link"
	// GUIDStable hashes the title and date, so the identifier survives a change
	// to the URL. Useful for feeds whose links carry tracking or session
	// parameters.
	GUIDStable GUIDMode = "stable"
	// GUIDTitle uses the title alone.
	GUIDTitle GUIDMode = "title"
)

// Spec describes the feed to build.
type Spec struct {
	Title       string
	Link        string
	Description string
	Language    string

	// SelfLink is the URL this feed is served from, written as rel="self" so a
	// reader can rediscover it. Empty omits it, which is right for a feed built
	// by a library with no HTTP layer.
	SelfLink string
	// SelfType is the media type of the document at SelfLink, required on an
	// Atom enclosure and harmless elsewhere.
	SelfType string

	// Items are the entries. They may be in any order; they are sorted by date
	// when any of them carries one, because a reader that shows items in the
	// order they arrived reads a reversed feed as a broken one.
	Items []listpage.Item

	// Content holds the full article HTML per item link, when the user asked
	// for full text. Items without an entry fall back to their summary.
	Content map[string]string

	// FullText requests the fetched article body in the item description
	// rather than the listing teaser.
	FullText bool

	// FullTextMaxRunes caps the rendered body. Zero means no cap.
	FullTextMaxRunes int

	// SummaryMaxRunes caps the plain-text description when there is no full
	// text. Zero means DefaultSummaryRunes.
	SummaryMaxRunes int

	// GUID selects the identity scheme.
	GUID GUIDMode

	// TTL is the feed's caching hint, in minutes. Zero omits it.
	TTL int

	// Updated overrides the feed-level update time. Zero derives it from the
	// newest item, then from the current time.
	Updated time.Time
	// Now supplies the current time, so a build is reproducible in a test.
	Now time.Time
}

// DefaultSummaryRunes is how much teaser text a description holds when the user
// did not ask for full text and gave no limit.
const DefaultSummaryRunes = 500

// Entry is one rendered item, in a form the output writers share.
type Entry struct {
	Title       string
	Link        string
	GUID        string
	Published   time.Time
	Updated     time.Time
	Author      string
	Summary     string // plain text, for readers that ignore HTML
	ContentHTML string // escaped-by-the-writer article body, may be empty
	Image       string
}

// Feed is the rendered-agnostic result.
type Feed struct {
	Title       string
	Link        string
	Description string
	Language    string

	// SelfLink is the URL this feed is served from, written as rel="self" so a
	// reader can rediscover it. It mirrors Spec.SelfLink so that a Feed can be
	// re-rendered without carrying the Spec around.
	SelfLink string
	// SelfType is the media type of the document at SelfLink.
	SelfType string

	Updated time.Time
	TTL     int
	Entries []Entry
}

// Build turns a Spec into a Feed, applying the sorting, GUID and fallback rules
// that must behave identically in every output format.
func Build(spec Spec) Feed {
	now := spec.Now
	if now.IsZero() {
		now = time.Now()
	}
	summaryMax := spec.SummaryMaxRunes
	if summaryMax <= 0 {
		summaryMax = DefaultSummaryRunes
	}

	f := Feed{
		Title:       strings.TrimSpace(spec.Title),
		Link:        spec.Link,
		Description: strings.TrimSpace(spec.Description),
		Language:    spec.Language,
		SelfLink:    strings.TrimSpace(spec.SelfLink),
		SelfType:    strings.TrimSpace(spec.SelfType),
		TTL:         spec.TTL,
	}

	items := make([]listpage.Item, len(spec.Items))
	copy(items, spec.Items)
	if sortByDate(items) {
		sort.SliceStable(items, func(a, b int) bool {
			return items[a].Date.After(items[b].Date)
		})
	}

	f.Entries = make([]Entry, 0, len(items))
	for _, it := range items {
		e := Entry{
			Title:   title(it),
			Link:    it.Link,
			GUID:    guid(it, spec.GUID),
			Author:  strings.TrimSpace(it.Author),
			Updated: it.Date,
			Image:   it.Image,
		}
		if !it.Date.IsZero() {
			e.Published = it.Date
		}
		body := ""
		if spec.FullText {
			body = spec.Content[urlx.Normalize(it.Link)]
		}
		if body == "" {
			// No full text was fetched or the fetch failed. The listing teaser
			// is better than an empty description: a feed with blank entries
			// reads as broken, and the user can always follow the link.
			e.Summary = domx.TruncateRunes(domx.NormSpace(it.Summary), summaryMax)
			if e.Summary == "" {
				e.Summary = domx.TruncateRunes(domx.NormSpace(it.Title), summaryMax)
			}
		} else {
			if spec.FullTextMaxRunes > 0 {
				body = domx.TruncateRunes(body, spec.FullTextMaxRunes)
			}
			e.ContentHTML = body
			e.Summary = domx.TruncateRunes(domx.NormSpace(stripTags(body)), summaryMax)
		}
		if e.Summary == "" && e.ContentHTML == "" {
			// An item with neither text nor body still needs a description
			// placeholder, or some readers discard the entry entirely.
			e.Summary = title(it)
		}
		f.Entries = append(f.Entries, e)
	}

	f.Updated = spec.Updated
	if f.Updated.IsZero() {
		for _, e := range f.Entries {
			if !e.Published.IsZero() && e.Published.After(f.Updated) {
				f.Updated = e.Published
			}
		}
	}
	if f.Updated.IsZero() {
		f.Updated = now
	}
	if f.Title == "" {
		f.Title = "Feed"
	}
	return f
}

// title prefers the item's own title and falls back to something derived from
// the link, because an entry with an empty title is shown as a bare URL by
// every reader.
func title(it listpage.Item) string {
	if t := domx.NormSpace(it.Title); t != "" {
		return t
	}
	if slug := urlx.SlugOf(it.Link); slug != "" {
		return strings.ReplaceAll(slug, "-", " ")
	}
	return it.Link
}

// sortByDate reports whether reordering is safe. Items with no date keep their
// input position at the end rather than being scattered, and a page where
// nothing has a date is left untouched.
func sortByDate(items []listpage.Item) bool {
	dated := 0
	for _, it := range items {
		if !it.Date.IsZero() {
			dated++
		}
	}
	return dated > 0
}

// guid derives an item's stable identifier.
func guid(it listpage.Item, mode GUIDMode) string {
	norm := urlx.Normalize(it.Link)
	switch mode {
	case GUIDTitle:
		return title(it)
	case GUIDStable:
		// A change to the URL is common on sites that rotate article paths. The
		// date and title survive it, so hashing those keeps the identifier
		// recognisable to a reader across a redirect.
		return "sha256:" + shortHash(it.Title+"|"+it.Date.UTC().Format(time.RFC3339))
	default:
		if norm == "" {
			return title(it)
		}
		return norm
	}
}

// Render writes the feed in the requested format.
func Render(f Feed, format Format, w io.Writer) error {
	switch format {
	case FormatRSS:
		return writeRSS(f, w)
	case FormatAtom:
		return writeAtom(f, w)
	case FormatJSON:
		return writeJSON(f, w)
	default:
		return fmt.Errorf("feed: unknown format %q", format)
	}
}

// Bytes renders the feed and returns it, which is what a test and a HEAD
// response want.
func Bytes(f Feed, format Format) ([]byte, error) {
	var b strings.Builder
	if err := Render(f, format, &b); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// stripTags reduces HTML to plain text for the summary field, which is what a
// reader shows when it does not render markup.
func stripTags(s string) string {
	if s == "" {
		return ""
	}
	// PlainText rather than TextOf: a description is read by a person, and
	// "CepatOleh" instead of "Cepat Oleh" is a visible defect.
	return domx.PlainText(domx.Fragment(s))
}

// contentSummary is used by the JSON and Atom writers, which need a single
// plain-text field for entries that carry no body.
func contentSummary(e Entry) string {
	if e.Summary != "" {
		return e.Summary
	}
	return domx.NormSpace(stripTags(e.ContentHTML))
}

// dateString renders a time for a format, or "" when the time is unknown.
// An unknown date is omitted rather than written as the zero time, which readers
// would show as the year 1.
func dateString(t time.Time, format string) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(format)
}

// shortHash is the digest used by the "stable" GUID mode. Twenty hex characters
// is far more than enough to keep identifiers distinct within one feed, and a
// full SHA-256 makes an unreadable feed URL.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:20]
}
