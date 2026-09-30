// Package listpage turns a listing page into a list of feed items.
//
// Two modes share one extraction path. A hand-written or site-configured
// selector names the item containers directly. When no selector is given, the
// package detects the list by harvesting every plausible link, grouping them by
// the shape of their URL, and picking the group that looks most like a list of
// articles. Detection reports a confidence and the runners-up so a caller can
// ask the user instead of guessing.
package listpage

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"

	"feedme/internal/dates"
	"feedme/internal/domx"
	"feedme/internal/urlx"
)

// Item is one entry in a listing.
type Item struct {
	Link    string
	Title   string
	Author  string
	Date    time.Time
	Image   string
	Summary string
	// GUID is a globally unique identifier for the item, from the feed's
	// <guid>, <id>, or equivalent field. It is used for deduplication
	// when merging feeds, because links may differ by tracking params while
	// the GUID remains stable.
	GUID string
}

// ErrNoItems is returned when nothing usable was found. It is an ordinary
// outcome for a page that is not a listing, and carries no wrapped detail.
var ErrNoItems = errors.New("listpage: no items found")

// Options controls item extraction. The selector fields map one-to-one onto the
// keys accepted in a site config, so the same values work in either place.
type Options struct {
	// BaseURL resolves relative links. Required for correct output on any
	// page that uses relative hrefs.
	BaseURL string

	// Item selects the item containers. When empty, Detect is used instead.
	Item    []string
	URL     []string
	Title   []string
	Author  []string
	Date    []string
	Image   []string
	Summary []string

	// Exclude drops whole subtrees before any item is collected. It is the
	// escape hatch for page furniture that no heuristic can recognise.
	Exclude []string

	// StripCSS empties the text of matching elements but keeps the elements
	// themselves. It differs from Exclude on purpose: use Exclude to remove a
	// block, and StripCSS to keep a wrapper while taking its wording out, which
	// is how a "Read more" strapline or an ad label is silenced without
	// changing the page's structure.
	StripCSS []string

	// KeepQueryParams filters each link's query string. See urlx.KeepQueryParams.
	KeepQueryParams string

	// AllowCrossHost keeps links pointing at other hosts. Off by default:
	// social buttons and partner links would otherwise join the feed.
	AllowCrossHost bool

	// ForceHost rewrites every item link and image onto this host, keeping the
	// scheme, path, and query. It is for a site whose links point at a host
	// that cannot be fetched (a blocked subdomain) while the same article is
	// served from one that can. Applied before AllowCrossHost is checked, so a
	// rewritten link counts as same-host.
	ForceHost string

	// MinItems is how many links a detected pattern needs before it is
	// accepted. Zero means DefaultMinItems.
	MinItems int

	// MaxItems caps the result. Zero means no cap.
	MaxItems int
}

// DefaultMinItems is the smallest group of same-shaped links that can be
// called a listing. One or two links is a guess; three is a pattern.
const DefaultMinItems = 3

// MaxCandidateLinks bounds how many links detection will consider. A portal
// page can carry tens of thousands of anchors, and grouping all of them costs
// memory without improving the answer.
const MaxCandidateLinks = 4000

// FromSelectors extracts items using the configured selectors. It is the mode
// used whenever a site config or the user's id_or_class parameter supplies an
// item selector.
func FromSelectors(doc *html.Node, opts Options) ([]Item, error) {
	containers := domx.SelectAll(doc, opts.Item)
	if len(containers) == 0 {
		return nil, fmt.Errorf("%w: item selector %q matched nothing", ErrNoItems, strings.Join(opts.Item, ", "))
	}
	// Stripping happens before item collection so an emptied wrapper stops
	// contributing text to titles and summaries.
	stripText(doc, opts.StripCSS)
	if len(opts.Exclude) > 0 {
		// The excluded subtrees are found from the document root, not from the
		// first item: an excluded wrapper such as <div class="promo"> encloses
		// several items and is never inside one.
		containers = dropExcluded(doc, containers, opts.Exclude)
	}
	items := make([]Item, 0, len(containers))
	seen := map[string]bool{}
	for _, c := range containers {
		it, ok := itemFromNode(c, opts)
		if !ok {
			continue
		}
		if seen[urlx.Normalize(it.Link)] {
			continue
		}
		seen[urlx.Normalize(it.Link)] = true
		items = append(items, it)
	}
	return finish(items, opts)
}

// itemFromNode reads one item container. ok is false when the container has no
// usable link, which happens when a selector is a little too greedy.
func itemFromNode(c *html.Node, opts Options) (Item, bool) {
	anchor := itemAnchor(c, opts)
	link := ""
	if anchor != nil {
		link = domx.Attr(anchor, "href")
		// A fragment-only href points at this page, not at an article, and it
		// must be rejected before it is resolved: after resolution it looks
		// like a valid absolute URL.
		if isFragmentOnly(link) {
			return Item{}, false
		}
		link = domx.ResolveURL(link, opts.BaseURL)
	}
	if len(opts.URL) > 0 {
		// An explicit URL selector overrides the first-link heuristic.
		if v := domx.SelectAttr(c, opts.URL, "href", opts.BaseURL); v != "" {
			link, anchor = v, nil
		}
	}
	// Rewriting onto ForceHost happens before the cross-host check, so a link
	// that pointed at a sibling subdomain becomes a same-host link and is kept.
	link = forceHost(link, opts)
	if !acceptable(link, opts) {
		return Item{}, false
	}
	// The base URL itself is the listing we are standing on, never an item.
	if opts.BaseURL != "" && urlx.Normalize(link) == urlx.Normalize(opts.BaseURL) {
		return Item{}, false
	}
	link = urlx.KeepQueryParams(link, opts.KeepQueryParams)

	return Item{
		Link:    link,
		Title:   itemTitle(c, anchor, link, opts),
		Author:  itemAuthor(c, opts),
		Date:    itemDate(c, opts),
		Image:   itemImage(c, opts),
		Summary: itemSummary(c, opts),
	}, true
}

// itemAnchor finds the anchor that represents the item: the URL selector's
// first match if one is configured, otherwise the first link in the container.
func itemAnchor(c *html.Node, opts Options) *html.Node {
	if len(opts.URL) > 0 {
		if n := domx.SelectOne(c, opts.URL); n != nil {
			return n
		}
		return nil
	}
	for _, a := range domx.FindElements(c, "a") {
		if href := domx.Attr(a, "href"); href != "" && !isFragmentOnly(href) {
			return a
		}
	}
	return nil
}

// isFragmentOnly reports whether a href is "#" or "#section", an in-page jump.
func isFragmentOnly(href string) bool {
	return strings.HasPrefix(strings.TrimSpace(href), "#")
}

// acceptable rejects links that can never be a feed item: non-navigational
// schemes, in-page anchors, and off-site links unless they were allowed.
func acceptable(link string, opts Options) bool {
	if link == "" {
		return false
	}
	lower := strings.ToLower(link)
	for _, scheme := range []string{"javascript:", "mailto:", "tel:", "data:", "sms:", "whatsapp:"} {
		if strings.HasPrefix(lower, scheme) {
			return false
		}
	}
	if strings.HasPrefix(link, "#") {
		return false
	}
	if opts.BaseURL != "" && !opts.AllowCrossHost && !sameSite(link, opts.BaseURL) {
		return false
	}
	return true
}

// sameSite accepts the base host and any of its subdomains, so a listing on
// surabaya.kompas.com keeps links to kompas.com and its other regions.
func sameSite(link, base string) bool {
	if urlx.SameHost(link, base) {
		return true
	}
	// Same registrable domain: a listing on www.example.com keeps links to
	// news.example.com and sport.example.com, its own siblings. Comparing the
	// registrable domain rather than the full host is what stops a portal whose
	// articles live on subdomains from collapsing to a single item.
	site := urlx.Site(base)
	return site != "" && site == urlx.Site(link)
}

// itemTitle picks the headline.
//
// An explicit selector wins. Otherwise the item anchor's own text is used,
// falling back to a heading, then image alt text, then the URL slug.
//
// Reading the whole container's text is deliberately avoided: it also picks up
// the date, the category and the teaser, so the title would arrive as
// "26 September 2026 Ekonomi Ringkasan artikel ..." in every card.
func itemTitle(c *html.Node, anchor *html.Node, link string, opts Options) string {
	if len(opts.Title) > 0 {
		if t := domx.SelectText(c, opts.Title); t != "" {
			return t
		}
	}
	if anchor != nil {
		if t := domx.NormSpace(domx.TextOf(anchor)); t != "" {
			return t
		}
	}
	for _, tag := range []string{"h1", "h2", "h3", "h4"} {
		if nodes := domx.FindElements(c, tag); len(nodes) > 0 {
			if t := domx.NormSpace(domx.TextOf(nodes[0])); t != "" {
				return t
			}
		}
	}
	for _, img := range domx.FindElements(c, "img") {
		if alt := domx.NormSpace(domx.Attr(img, "alt")); alt != "" {
			return alt
		}
	}
	// Last resort: a readable form of the final path segment.
	if slug := urlx.SlugOf(link); slug != "" {
		return strings.ReplaceAll(slug, "-", " ")
	}
	return ""
}

// bylineLabel is a label a listing page puts before a reporter's name.
type bylineLabel struct {
	// text is the label, lowercase and without a separator.
	text string
	// needsSep says whether a space alone is enough to trust the match. "Oleh "
	// is distinctive enough on its own; "by " is not, because "by 2030" is
	// ordinary English prose, so it needs a colon or a dash.
	needsSep bool
}

// bylineLabels are the labels Indonesian and English listings use.
var bylineLabels = []bylineLabel{
	{"oleh", false},
	{"penulis", false},
	{"writer", true},
	{"author", true},
	{"by", true},
}

// separators are the characters a byline label is followed by.
const bylineSeps = ":-–—"

// itemAuthor reads a byline.
//
// A listing card rarely marks the byline up, so the label is the reliable
// signal: "Oleh: Redaksi" is recognisable while the bare name of a reporter is
// not. Without it a full-text feed has no dc:creator, which some readers use to
// group items.
func itemAuthor(c *html.Node, opts Options) string {
	if len(opts.Author) > 0 {
		if t := domx.SelectText(c, opts.Author); t != "" {
			return trimAuthorLabel(t)
		}
	}
	for _, sel := range []string{`[rel="author"]`, ".author", ".byline", ".penulis"} {
		if n := domx.SelectOne(c, []string{sel}); n != nil {
			if t := domx.NormSpace(domx.TextOf(n)); t != "" {
				if name := trimAuthorLabel(t); name != "" {
					return name
				}
			}
		}
	}
	// Fall back to looking for a labelled byline among the card's own text
	// nodes.
	return findBylineIn(c)
}

// findBylineIn returns the name that follows a byline label in a card.
//
// The text nodes are examined one by one rather than as one flattened string,
// because a flattened string has lost the element boundaries that tell a byline
// apart from the last word of a headline.
func findBylineIn(n *html.Node) string {
	for _, node := range domx.TextNodes(n) {
		if name := findByline(node.Data); name != "" {
			return name
		}
	}
	return ""
}

// findByline returns the name that follows a byline label in a piece of text.
//
// Two shapes are accepted: a text node that starts with a label, which is a
// byline element such as <span>Oleh: Rina</span>, and a label followed by a
// strong separator anywhere in the node, which covers "ditulis oleh: Rina".
// A bare "by" in prose is not enough, because "by 2030" is an ordinary phrase.
func findByline(text string) string {
	if text == "" {
		return ""
	}
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, label := range bylineLabels {
		// The node begins with the label.
		if strings.HasPrefix(lower, label.text) {
			if name, ok := bylineValue(text[len(label.text):], label.needsSep); ok {
				return name
			}
		}
		// The label appears inside the node, which requires a separator to
		// count. "oleh: Rina" qualifies, "by 2030" does not.
		for from := 0; ; {
			i := strings.Index(lower[from:], label.text)
			if i < 0 {
				break
			}
			i += from
			from = i + len(label.text)
			if !atWordStart(lower, i) {
				continue
			}
			if !strings.ContainsRune(bylineSeps, rune(text[from])) {
				continue
			}
			if name, ok := bylineValue(text[from:], true); ok {
				return name
			}
		}
	}
	return ""
}

// bylineValue reads the name after a label, reporting false when the label is
// not followed by something that looks like a separator.
func bylineValue(rest string, needsSep bool) (string, bool) {
	rest = strings.TrimLeft(rest, " \t\n\r")
	if rest == "" {
		return "", false
	}
	if !strings.ContainsRune(bylineSeps, rune(rest[0])) {
		if needsSep {
			return "", false
		}
		// "Oleh Rina" with no separator at all: the space is already consumed
		// above, so any remaining text is the name.
	}
	rest = strings.TrimLeft(rest, bylineSeps+" \t\n\r")
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false
	}
	// A byline is a name, not a sentence. Two words is enough for "Redaksi
	// kami"; more than that means this matched prose that happened to contain
	// the word, and a wrong author is worse than none.
	name := fields[0]
	if len(fields) > 1 && isNameWord(fields[1]) {
		name += " " + fields[1]
	}
	return strings.Trim(name, ".,;:"), true
}

// atWordStart reports whether index i begins a word, so that "oby" does not
// match the label "by".
func atWordStart(lower string, i int) bool {
	if i == 0 {
		return true
	}
	prev, size := utf8.DecodeLastRuneInString(lower[:i])
	switch prev {
	case ' ', '\t', '\n', '\r', '>', '"', '(', '[', '|', ',', '•', '·', '–', '—':
		return true
	}
	return size == 0
}

// trimAuthorLabel strips a leading label from a byline element's text, for the
// case where the page marked the byline up but included its label.
// The separator rule is relaxed here. It exists to stop "by 2030" in prose from
// being read as a byline, but a node the page itself marked as the author is not
// prose, so "By Rina" inside <span class="author"> is a name with a label.
func trimAuthorLabel(s string) string {
	trimmed := strings.TrimSpace(s)
	lower := strings.ToLower(trimmed)
	for _, label := range bylineLabels {
		if strings.HasPrefix(lower, label.text) {
			if name, ok := bylineValue(trimmed[len(label.text):], false); ok {
				return name
			}
		}
	}
	return trimmed
}

// isNameWord reports whether a second word plausibly belongs to a name.
func isNameWord(w string) bool {
	w = strings.Trim(w, ".,;:")
	if w == "" {
		return false
	}
	// A capitalised word is a surname or an initial.
	r := []rune(w)[0]
	return unicode.IsUpper(r) || strings.HasPrefix(w, ".")
}

// itemDate reads a publication date from a selector, a <time> element, or the
// first date-looking string in the container.
func itemDate(c *html.Node, opts Options) time.Time {
	if len(opts.Date) > 0 {
		for _, n := range domx.SelectAll(c, opts.Date) {
			for _, candidate := range dateCandidates(n) {
				if t := dates.ParseTime(candidate); !t.IsZero() {
					return t
				}
			}
		}
	}
	for _, t := range domx.FindElements(c, "time") {
		for _, candidate := range dateCandidates(t) {
			if parsed := dates.ParseTime(candidate); !parsed.IsZero() {
				return parsed
			}
		}
	}
	// Microdata cards carry the date in an itemprop element rather than a
	// <time> tag.
	for _, n := range domx.SelectAll(c, []string{`[itemprop*="date"]`, `[class*="date"]`, `[class*="time"]`}) {
		for _, candidate := range dateCandidates(n) {
			if parsed := dates.ParseTime(candidate); !parsed.IsZero() {
				return parsed
			}
		}
	}
	// Indonesian listings usually print the date as plain text beside the
	// headline rather than in a <time> element, and it is often glued to other
	// words in the same text node, so the whole container text is searched
	// rather than expecting a clean value to parse.
	if t, ok := dates.Find(domx.NormSpace(domx.TextOf(c))); ok {
		return t
	}
	return time.Time{}
}

// dateCandidates lists the strings worth trying on a node, most reliable first.
func dateCandidates(n *html.Node) []string {
	var out []string
	if v := domx.Attr(n, "datetime"); v != "" {
		out = append(out, v)
	}
	if v := domx.Attr(n, "content"); v != "" {
		out = append(out, v)
	}
	if v := domx.PlainText(n); v != "" {
		out = append(out, v)
	}
	return out
}

// itemImage finds a thumbnail from a selector, a data attribute, or the first
// usable image in the container.
func itemImage(c *html.Node, opts Options) string {
	if len(opts.Image) > 0 {
		if v := domx.SelectAttr(c, opts.Image, "src", opts.BaseURL); v != "" {
			return forceHost(v, opts)
		}
	}
	for _, img := range domx.FindElements(c, "img") {
		for _, name := range []string{"src", "data-src", "data-original", "data-lazy-src"} {
			if v := domx.Attr(img, name); v != "" && !strings.HasPrefix(v, "data:") {
				return forceHost(domx.ResolveURL(v, opts.BaseURL), opts)
			}
		}
	}
	return ""
}

// forceHost applies opts.ForceHost to an already-resolved URL.
func forceHost(link string, opts Options) string {
	if link == "" || opts.ForceHost == "" {
		return link
	}
	return urlx.SetHost(link, opts.ForceHost)
}

// itemSummary is the short teaser text. It reuses the title's text when no
// distinct summary element is configured, so a card with a single text block
// still gets something to show.
func itemSummary(c *html.Node, opts Options) string {
	if len(opts.Summary) > 0 {
		if t := domx.SelectPlain(c, opts.Summary); t != "" {
			return t
		}
		return ""
	}
	// The whole card, flattened with block boundaries kept, so that a teaser
	// does not read "Judul26 September 2026Ringkasan...".
	if t := domx.PlainText(c); t != "" {
		return domx.TruncateRunes(t, SummaryRunes)
	}
	return ""
}

// SummaryRunes is how much teaser text an item keeps when no summary selector
// is configured. Roughly a paragraph, which is what feed readers show.
const SummaryRunes = 300

// stripText empties every element matching the selectors, keeping the elements
// in place. Children are detached rather than deleted one at a time, which is
// the same trap the sanitizer had: a node cannot be reparented while it is
// still attached.
func stripText(doc *html.Node, selectors []string) {
	if len(selectors) == 0 {
		return
	}
	for _, n := range domx.SelectAll(doc, selectors) {
		for child := n.FirstChild; child != nil; {
			next := child.NextSibling
			n.RemoveChild(child)
			child = next
		}
	}
}

// dropExcluded removes excluded subtrees and returns only the surviving
// containers.
func dropExcluded(root *html.Node, containers []*html.Node, exclude []string) []*html.Node {
	if len(exclude) == 0 {
		return containers
	}
	excluded := map[*html.Node]bool{}
	for _, sel := range exclude {
		for _, n := range domx.SelectAll(root, []string{sel}) {
			excluded[n] = true
		}
	}
	if len(excluded) == 0 {
		return containers
	}
	out := containers[:0]
	for _, c := range containers {
		if !within(c, excluded) {
			out = append(out, c)
		}
	}
	return out
}

// within reports whether n is, or sits inside, any marked node.
func within(n *html.Node, marked map[*html.Node]bool) bool {
	for cur := n; cur != nil; cur = cur.Parent {
		if marked[cur] {
			return true
		}
	}
	return false
}

// finish applies the shared limits and rejects an empty result.
func finish(items []Item, opts Options) ([]Item, error) {
	if opts.MaxItems > 0 && len(items) > opts.MaxItems {
		items = items[:opts.MaxItems]
	}
	if len(items) == 0 {
		return nil, ErrNoItems
	}
	return items, nil
}
