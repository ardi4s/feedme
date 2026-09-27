// Package feedread parses an existing RSS, Atom, or JSON Feed into the same
// item shape the rest of the pipeline works with, so that an existing feed can
// be filtered, merged, and upgraded to full text exactly like a scraped page.
package feedread

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	stdhtml "html"
	"strings"
	"time"

	"golang.org/x/net/html"

	"feedme/internal/dates"
	"feedme/internal/domx"
	"feedme/internal/listpage"
	"feedme/internal/urlx"
)

// Document is a parsed feed: its own title and link, and its entries.
type Document struct {
	// Title is the feed's title, which names a merged feed when the request
	// does not pick one.
	Title string
	// Link is the feed's home page.
	Link string
	// Format is the vocabulary the document was written in, for diagnostics.
	Format string
	// Items are the entries, in the order the document listed them.
	Items []listpage.Item
}

// Parse reads a feed document. baseURL resolves relative links and is normally
// the feed's own URL.
func Parse(body []byte, baseURL string) (*Document, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errors.New("feedread: empty document")
	}
	if trimmed[0] == '{' {
		return parseJSON(trimmed, baseURL)
	}
	root, err := rootName(trimmed)
	if err != nil {
		return nil, fmt.Errorf("feedread: not a feed: %w", err)
	}
	switch root {
	case "rss":
		return parseRSS(trimmed, baseURL)
	case "feed":
		return parseAtom(trimmed, baseURL)
	case "rdf":
		return parseRDF(trimmed, baseURL)
	default:
		return nil, fmt.Errorf("feedread: unsupported feed root %q", root)
	}
}

// rootName returns the local name of the document's root element, lowercased.
func rootName(b []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		if se, ok := tok.(xml.StartElement); ok {
			return strings.ToLower(se.Name.Local), nil
		}
	}
}

// ---------------------------------------------------------------------------
// RSS 2.0 and RSS 1.0 (RDF)

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	DCDate      string `xml:"http://purl.org/dc/elements/1.1/ date"`
	Description string `xml:"description"`
	Content     string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Author      string `xml:"author"`
	Creator     string `xml:"http://purl.org/dc/elements/1.1/ creator"`
	Enclosure   *struct {
		URL  string `xml:"url,attr"`
		Type string `xml:"type,attr"`
	} `xml:"enclosure"`
	Thumbnail *struct {
		URL string `xml:"url,attr"`
	} `xml:"http://search.yahoo.com/mrss/ thumbnail"`
}

type rssDoc struct {
	Channel struct {
		Title string    `xml:"title"`
		Link  string    `xml:"link"`
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

func parseRSS(b []byte, base string) (*Document, error) {
	var d rssDoc
	if err := xml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("feedread: parse RSS: %w", err)
	}
	return &Document{
		Title:  textOf(d.Channel.Title),
		Link:   urlx.Resolve(d.Channel.Link, base),
		Format: "rss",
		Items:  rssItems(d.Channel.Items, base),
	}, nil
}

type rdfDoc struct {
	Channel struct {
		Title string `xml:"title"`
		Link  string `xml:"link"`
	} `xml:"channel"`
	Items []struct {
		rssItem
		About string `xml:"http://www.w3.org/1999/02/22-rdf-syntax-ns# about,attr"`
	} `xml:"item"`
}

func parseRDF(b []byte, base string) (*Document, error) {
	var d rdfDoc
	if err := xml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("feedread: parse RSS 1.0: %w", err)
	}
	items := make([]listpage.Item, 0, len(d.Items))
	for _, it := range d.Items {
		if it.Link == "" {
			it.Link = it.About
		}
		items = append(items, rssItemToItem(it.rssItem, base))
	}
	return &Document{
		Title:  textOf(d.Channel.Title),
		Link:   urlx.Resolve(d.Channel.Link, base),
		Format: "rdf",
		Items:  items,
	}, nil
}

func rssItems(in []rssItem, base string) []listpage.Item {
	out := make([]listpage.Item, 0, len(in))
	for _, it := range in {
		out = append(out, rssItemToItem(it, base))
	}
	return out
}

func rssItemToItem(it rssItem, base string) listpage.Item {
	link := urlx.Resolve(it.Link, base)
	if link == "" && looksLikeURL(it.GUID) {
		link = urlx.Resolve(it.GUID, base)
	}
	summary := textOf(it.Description)
	if summary == "" {
		summary = textOf(it.Content)
	}
	author := strings.TrimSpace(it.Author)
	if author == "" {
		author = strings.TrimSpace(it.Creator)
	}
	return listpage.Item{
		Link:    link,
		Title:   textOf(it.Title),
		Author:  author,
		Date:    parseDate(firstNonEmpty(it.PubDate, it.DCDate)),
		Summary: summary,
		Image:   rssImage(it),
	}
}

func rssImage(it rssItem) string {
	if it.Thumbnail != nil && it.Thumbnail.URL != "" {
		return it.Thumbnail.URL
	}
	if it.Enclosure != nil && strings.HasPrefix(it.Enclosure.Type, "image/") {
		return it.Enclosure.URL
	}
	return ""
}

// ---------------------------------------------------------------------------
// Atom 1.0

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	Links     []atomLink `xml:"link"`
	ID        string     `xml:"id"`
	Updated   string     `xml:"updated"`
	Published string     `xml:"published"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
	Author    struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Thumbnail *struct {
		URL string `xml:"url,attr"`
	} `xml:"http://search.yahoo.com/mrss/ thumbnail"`
}

type atomDoc struct {
	Title   string      `xml:"title"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

func parseAtom(b []byte, base string) (*Document, error) {
	var d atomDoc
	if err := xml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("feedread: parse Atom: %w", err)
	}
	items := make([]listpage.Item, 0, len(d.Entries))
	for _, e := range d.Entries {
		link := atomEntryLink(e, base)
		if link == "" && looksLikeURL(e.ID) {
			link = urlx.Resolve(e.ID, base)
		}
		summary := textOf(e.Content)
		if summary == "" {
			summary = textOf(e.Summary)
		}
		items = append(items, listpage.Item{
			Link:    link,
			Title:   textOf(e.Title),
			Author:  strings.TrimSpace(e.Author.Name),
			Date:    parseDate(firstNonEmpty(e.Published, e.Updated)),
			Summary: summary,
			Image:   atomImage(e, base),
		})
	}
	return &Document{
		Title:  textOf(d.Title),
		Link:   atomFeedLink(d.Links, base),
		Format: "atom",
		Items:  items,
	}, nil
}

func atomEntryLink(e atomEntry, base string) string {
	for _, l := range e.Links {
		if l.Rel == "" || l.Rel == "alternate" {
			return urlx.Resolve(l.Href, base)
		}
	}
	if len(e.Links) > 0 {
		return urlx.Resolve(e.Links[0].Href, base)
	}
	return ""
}

func atomFeedLink(links []atomLink, base string) string {
	for _, l := range links {
		if l.Rel == "" || l.Rel == "alternate" {
			return urlx.Resolve(l.Href, base)
		}
	}
	return ""
}

func atomImage(e atomEntry, base string) string {
	if e.Thumbnail != nil && e.Thumbnail.URL != "" {
		return urlx.Resolve(e.Thumbnail.URL, base)
	}
	for _, l := range e.Links {
		if l.Rel == "enclosure" && strings.HasPrefix(l.Type, "image/") {
			return urlx.Resolve(l.Href, base)
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// JSON Feed 1.1

type jsonAuthor struct {
	Name string `json:"name"`
}

type jsonItem struct {
	ID            string       `json:"id"`
	URL           string       `json:"url"`
	Title         string       `json:"title"`
	ContentHTML   string       `json:"content_html"`
	ContentText   string       `json:"content_text"`
	Summary       string       `json:"summary"`
	Image         string       `json:"image"`
	DatePublished string       `json:"date_published"`
	DateModified  string       `json:"date_modified"`
	Authors       []jsonAuthor `json:"authors"`
	Author        *jsonAuthor  `json:"author"`
}

type jsonDoc struct {
	Version     string     `json:"version"`
	Title       string     `json:"title"`
	HomePageURL string     `json:"home_page_url"`
	Items       []jsonItem `json:"items"`
}

func parseJSON(b []byte, base string) (*Document, error) {
	var d jsonDoc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("feedread: parse JSON Feed: %w", err)
	}
	items := make([]listpage.Item, 0, len(d.Items))
	for _, it := range d.Items {
		link := urlx.Resolve(it.URL, base)
		if link == "" && looksLikeURL(it.ID) {
			link = urlx.Resolve(it.ID, base)
		}
		summary := strings.TrimSpace(it.ContentText)
		if summary == "" {
			summary = textOf(it.ContentHTML)
		}
		if summary == "" {
			summary = textOf(it.Summary)
		}
		items = append(items, listpage.Item{
			Link:    link,
			Title:   textOf(it.Title),
			Author:  jsonAuthorName(it),
			Date:    parseDate(firstNonEmpty(it.DatePublished, it.DateModified)),
			Summary: domx.NormSpace(summary),
			Image:   strings.TrimSpace(it.Image),
		})
	}
	return &Document{
		Title:  domx.NormSpace(d.Title),
		Link:   strings.TrimSpace(d.HomePageURL),
		Format: "json",
		Items:  items,
	}, nil
}

func jsonAuthorName(it jsonItem) string {
	if len(it.Authors) > 0 {
		return strings.TrimSpace(it.Authors[0].Name)
	}
	if it.Author != nil {
		return strings.TrimSpace(it.Author.Name)
	}
	return ""
}

// ---------------------------------------------------------------------------

// textOf turns a possibly-HTML fragment into plain text. Feeds put markup in
// every text field, and a reader showing raw tags is a bug, not a feature.
func textOf(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.ContainsAny(s, "<&") {
		return domx.NormSpace(stdhtml.UnescapeString(s))
	}
	node, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return domx.NormSpace(s)
	}
	return domx.NormSpace(domx.PlainText(node))
}

// feedDateLayouts are the date formats feeds actually use, most of which the
// page-oriented parser in internal/dates does not carry. Tried first, because
// RFC 1123 with a numeric offset is the canonical RSS pubDate and must not be
// misread as a bare date.
var feedDateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	time.RFC3339,
	time.RFC3339Nano,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700",
}

// parseDate reads a feed date, then falls back to the general page parser.
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range feedDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return dates.ParseTime(s)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func looksLikeURL(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
