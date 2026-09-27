package feed

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"feedme/internal/dates"
)

// Date layouts required by each format.
const (
	rssDateLayout  = time.RFC1123Z
	atomDateLayout = time.RFC3339
)

// rss2 is the RSS 2.0 document.
//
// A struct with xml tags is used rather than string assembly so that titles
// containing "&" or "<" cannot produce a feed every reader rejects. The Content
// module namespace is declared on the channel because content:encoded is the
// only way to carry a full article body in RSS 2.0.
type rss2 struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	NSC     string   `xml:"xmlns:content,attr"`
	NSAtom  string   `xml:"xmlns:atom,attr"`
	NSDC    string   `xml:"xmlns:dc,attr"`
	NSMedia string   `xml:"xmlns:media,attr,omitempty"`
	Channel rssChan  `xml:"channel"`
}

type rssChan struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Language    string `xml:"language,omitempty"`
	LastBuild   string `xml:"lastBuildDate,omitempty"`
	TTL         int    `xml:"ttl,omitempty"`
	// Self points at this feed's own URL. Readers and aggregators use it to
	// discover the canonical location, so it is written as a proper link with
	// attributes rather than as element text.
	Self  *atomLink  `xml:"atom:link,omitempty"`
	Items []rssEntry `xml:"item"`
}

type rssEntry struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	GUID        rssGUID   `xml:"guid"`
	PubDate     string    `xml:"pubDate,omitempty"`
	Author      string    `xml:"dc:creator,omitempty"`
	Description string    `xml:"description"`
	Encoded     string    `xml:"content:encoded,omitempty"`
	Thumbnail   *rssMedia `xml:"media:thumbnail,omitempty"`
}

// rssMedia is one Media RSS element.
//
// The thumbnail is carried by Media RSS rather than by <enclosure> because RSS
// 2.0's <enclosure> requires a length in bytes and a MIME type. A listing page
// gives us a URL and nothing else, and inventing length="0" is a lie that some
// readers use to decide whether to fetch the image at all. Media RSS exists for
// exactly this case: a media URL with no size.
type rssMedia struct {
	URL    string `xml:"url,attr"`
	Medium string `xml:"medium,attr,omitempty"`
	Type   string `xml:"type,attr,omitempty"`
}

// rssGUID is the <guid> element.
//
// isPermaLink has to be an attribute of <guid> itself, so the element needs a
// struct of its own. Declaring the field on rssEntry would put it on <item>,
// which is meaningless to a reader; and writing it into the value would make
// the identifier itself read "... isPermaLink=true".
type rssGUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink string `xml:"isPermaLink,attr,omitempty"`
}

func writeRSS(f Feed, w io.Writer) error {
	doc := rss2{
		Version: "2.0",
		NSC:     "http://purl.org/rss/1.0/modules/content/",
		NSAtom:  "http://www.w3.org/2005/Atom",
		NSDC:    "http://purl.org/dc/elements/1.1/",
	}
	ch := rssChan{
		Title:       f.Title,
		Link:        f.Link,
		Description: f.Description,
		Language:    f.Language,
		TTL:         f.TTL,
		LastBuild:   dates.FormatRFC1123Z(f.Updated),
		Items:       make([]rssEntry, 0, len(f.Entries)),
	}
	if f.SelfLink != "" {
		ch.Self = &atomLink{Href: f.SelfLink, Rel: "self", Type: f.SelfType}
	}
	if ch.Description == "" {
		// A channel description is required by the specification. A feed with
		// the title repeated is odd but valid, where a missing element is not.
		ch.Description = f.Title
	}
	for _, e := range f.Entries {
		entry := rssEntry{
			Title:       e.Title,
			Link:        e.Link,
			GUID:        rssGUID{Value: e.GUID},
			PubDate:     dateString(e.Published, rssDateLayout),
			Author:      e.Author,
			Description: contentSummary(e),
			Encoded:     e.ContentHTML,
		}
		// isPermaLink tells a reader whether the GUID is a URL it can fetch.
		// Only the link-derived default qualifies: a hashed or title-based
		// identifier is not dereferenceable.
		if strings.HasPrefix(e.GUID, "http") {
			entry.GUID.IsPermaLink = "true"
		}
		if e.Image != "" {
			entry.Thumbnail = &rssMedia{URL: e.Image, Medium: "image", Type: imageMediaType(e.Image)}
			doc.NSMedia = mediaNS
		}
		ch.Items = append(ch.Items, entry)
	}
	doc.Channel = ch

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// atom1 is the Atom 1.0 document. Atom requires both id and updated on every
// entry, and id must be a URI, which is why the GUID is used rather than the
// title.
type atom1 struct {
	XMLName  xml.Name   `xml:"http://www.w3.org/2005/Atom feed"`
	NSMedia  string     `xml:"xmlns:media,attr,omitempty"`
	Title    string     `xml:"title"`
	Subtitle string     `xml:"subtitle,omitempty"`
	ID       string     `xml:"id"`
	Updated  string     `xml:"updated"`
	Links    []atomLink `xml:"link"`
	Author   *atomAuth  `xml:"author,omitempty"`
	Entries  []atomEnt  `xml:"entry"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
	// Type is required on rel="enclosure" and meaningless elsewhere.
	Type string `xml:"type,attr,omitempty"`
}

type atomAuth struct {
	Name string `xml:"name"`
}

type atomEnt struct {
	Title   string `xml:"title"`
	ID      string `xml:"id"`
	Updated string `xml:"updated"`
	// Published is omitted when the date is unknown rather than written as the
	// zero time, which a reader would render as the year 1.
	Published string `xml:"published,omitempty"`
	// Links carries the alternate page and, when the media type is knowable, an
	// enclosure. Atom allows more than one link, and rel is what distinguishes
	// them.
	Links []atomLink `xml:"link"`
	// Thumbnail is the Media RSS fallback for an image whose type is unknown.
	Thumbnail *rssMedia `xml:"media:thumbnail,omitempty"`
	Summary   string    `xml:"summary,omitempty"`
	Content   *atomCont `xml:"content,omitempty"`
}

type atomCont struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

func writeAtom(f Feed, w io.Writer) error {
	doc := atom1{
		Title:    f.Title,
		Subtitle: f.Description,
		ID:       atomID(f.Link, f.Updated),
		Updated:  dates.FormatISO(f.Updated),
		Links:    []atomLink{{Href: f.Link, Rel: "alternate"}},
		Entries:  make([]atomEnt, 0, len(f.Entries)),
	}
	if f.SelfLink != "" {
		doc.Links = append(doc.Links, atomLink{Href: f.SelfLink, Rel: "self", Type: f.SelfType})
	}
	for _, e := range f.Entries {
		// Atom requires <updated> on every entry. An undated entry borrows the
		// feed's own update time, which is when the feed was built. That is a
		// real timestamp and, unlike the Unix epoch, it does not claim the item
		// was published in 1970.
		updated := pickUpdated(e)
		if updated.IsZero() {
			updated = f.Updated
		}
		if updated.IsZero() {
			updated = fallbackTime
		}
		ent := atomEnt{
			Title:     e.Title,
			ID:        e.GUID,
			Updated:   dates.FormatISO(updated),
			Links:     []atomLink{{Href: e.Link, Rel: "alternate"}},
			Summary:   contentSummary(e),
			Published: dateString(e.Published, atomDateLayout),
		}
		if e.Image != "" {
			// Atom's rel="enclosure" demands a type. When the extension is
			// recognisable it is used; otherwise the Media RSS thumbnail carries
			// the URL, which needs no type and no length.
			if mt := imageMediaType(e.Image); mt != "" {
				ent.Links = append(ent.Links, atomLink{Href: e.Image, Rel: "enclosure", Type: mt})
			} else {
				ent.Thumbnail = &rssMedia{URL: e.Image, Medium: "image"}
				doc.NSMedia = mediaNS
			}
		}
		if e.ContentHTML != "" {
			ent.Content = &atomCont{Type: "html", Body: e.ContentHTML}
		}
		doc.Entries = append(doc.Entries, ent)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// pickUpdated falls back to the publication date because Atom's updated field is
// mandatory and the zero time is not a valid timestamp for a reader.
// pickUpdated returns the entry's own update time, or the zero time when it has
// none. It deliberately does not invent the Unix epoch: a field that is omitted
// reads as "unknown", while 1970 reads as a real publication date, and a reader
// that sorts on it will bury the item forever.
func pickUpdated(e Entry) time.Time {
	if !e.Updated.IsZero() {
		return e.Updated
	}
	return e.Published
}

// atomID derives the feed-level id. The link is preferred; a feed with no link
// still needs a stable identifier.
func atomID(link string, updated time.Time) string {
	if link != "" {
		return link
	}
	return "urn:feedme:" + dates.FormatISO(updated)
}

// jsonFeed is the JSON Feed 1.1 document.
type jsonFeed struct {
	Version     string      `json:"version"`
	Title       string      `json:"title"`
	HomePageURL string      `json:"home_page_url,omitempty"`
	FeedURL     string      `json:"feed_url,omitempty"`
	Description string      `json:"description,omitempty"`
	Language    string      `json:"language,omitempty"`
	Authors     []jsonNamed `json:"authors,omitempty"`
	Items       []jsonItem  `json:"items"`
}

type jsonNamed struct {
	Name string `json:"name"`
}

type jsonItem struct {
	ID            string      `json:"id"`
	URL           string      `json:"url,omitempty"`
	Title         string      `json:"title,omitempty"`
	ContentHTML   string      `json:"content_html,omitempty"`
	ContentText   string      `json:"content_text,omitempty"`
	Summary       string      `json:"summary,omitempty"`
	Image         string      `json:"image,omitempty"`
	DatePublished string      `json:"date_published,omitempty"`
	DateModified  string      `json:"date_modified,omitempty"`
	Authors       []jsonNamed `json:"authors,omitempty"`
	Tags          []string    `json:"tags,omitempty"`
}

func writeJSON(f Feed, w io.Writer) error {
	doc := jsonFeed{
		Version:     "https://jsonfeed.org/version/1.1",
		Title:       f.Title,
		HomePageURL: f.Link,
		Description: f.Description,
		Language:    f.Language,
		Items:       make([]jsonItem, 0, len(f.Entries)),
	}
	for _, e := range f.Entries {
		item := jsonItem{
			ID:            e.GUID,
			URL:           e.Link,
			Title:         e.Title,
			ContentHTML:   e.ContentHTML,
			ContentText:   domxNorm(e.ContentHTML, e.Summary),
			Summary:       e.Summary,
			Image:         e.Image,
			DatePublished: dateString(e.Published, atomDateLayout),
			DateModified:  dateString(pickUpdated(e), atomDateLayout),
		}
		if e.Author != "" {
			item.Authors = []jsonNamed{{Name: e.Author}}
		}
		doc.Items = append(doc.Items, item)
	}
	// Items must never serialise as null: JSON Feed requires an array, and some
	// readers reject the document outright when items is missing.
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("feed: encode json: %w", err)
	}
	return nil
}

// domxNorm prefers the rendered body's plain text and falls back to the
// already-computed summary.
func domxNorm(bodyHTML, summary string) string {
	if bodyHTML == "" {
		return summary
	}
	if t := stripTags(bodyHTML); t != "" {
		return t
	}
	return summary
}

// fallbackTime is used only when a hand-built Feed carries no update time at
// all and Atom still demands one. Build always sets Updated, so this exists for
// callers who construct a Feed literal.
var fallbackTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// mediaNS is the Media RSS namespace. It is declared only when an entry needs
// it, so a feed without images carries no unused namespace.
const mediaNS = "http://search.yahoo.com/mrss/"

// imageMediaType guesses a MIME type from a URL's file extension.
//
// It returns "" when the extension is missing or unrecognised, and callers are
// expected to treat that as "unknown" rather than defaulting to something. A
// wrong type is worse than no type: readers use it to decide whether to fetch
// the image at all.
func imageMediaType(rawURL string) string {
	// Strip the query and fragment first. A CDN URL almost always carries both,
	// and "?format=png" is not a file extension.
	if i := strings.IndexAny(rawURL, "?#"); i >= 0 {
		rawURL = rawURL[:i]
	}
	slash := strings.LastIndex(rawURL, "/")
	if slash < 0 {
		return ""
	}
	ext := strings.ToLower(rawURL[slash+1:])
	if dot := strings.LastIndex(ext, "."); dot >= 0 {
		ext = ext[dot+1:]
	} else {
		return ""
	}
	switch ext {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "avif":
		return "image/avif"
	case "svg":
		return "image/svg+xml"
	default:
		return ""
	}
}
