package extract

import (
	"strings"
	"time"

	"golang.org/x/net/html"

	"feedme/internal/dates"
	"feedme/internal/domx"
)

// Metadata is everything we can learn about an article from the document head
// and other non-content signals.
type Metadata struct {
	Title       string
	Byline      string
	Published   time.Time
	Modified    time.Time
	Image       string
	SiteName    string
	Lang        string
	Canonical   string
	Description string
	OgType      string

	// titleCandidates and h1 are raw signals; pickTitle arbitrates between
	// them rather than trusting a fixed precedence.
	titleCandidates []titleCandidate
	h1              string
}

// addTitle records a title candidate, ignoring duplicates.
func (m *Metadata) addTitle(value, source string) {
	v := domx.NormSpace(value)
	if v == "" {
		return
	}
	for _, existing := range m.titleCandidates {
		if existing.value == v {
			return
		}
	}
	m.titleCandidates = append(m.titleCandidates, titleCandidate{value: v, source: source})
}

// TitleCandidates returns the collected title signals.
func (m *Metadata) TitleCandidates() []titleCandidate { return m.titleCandidates }

// H1 returns the document's first h1, if any.
func (m *Metadata) H1() string { return m.h1 }

// readMetadata pulls head-level signals off a parsed document.
//
// Title/meta/canonical are read from <head> only. Pages routinely embed markup
// fragments inside inline scripts and templates, and those fragments contain
// literal <title> and <meta> tags for social widgets ("Instagram", tracking
// pixels, and so on). Walking the whole document picks those up and they
// outrank the real og:title.
func readMetadata(doc *html.Node) *Metadata {
	m := &Metadata{}

	// lang lives on <html>, which is an ancestor of <head>.
	for _, h := range domx.FindElements(doc, "html") {
		if l := domx.Attr(h, "lang"); l != "" {
			m.Lang = l
			break
		}
	}

	var walk func(n *html.Node, inInert bool)
	walk = func(n *html.Node, inInert bool) {
		if inInert {
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				m.addTitle(domx.TextOf(n), "title")
			case "link":
				rel := strings.ToLower(domx.Attr(n, "rel"))
				href := strings.TrimSpace(domx.Attr(n, "href"))
				// Inline data: URIs are placeholders, not fetchable images.
				if href == "" || strings.HasPrefix(strings.ToLower(href), "data:") {
					break
				}
				if rel == "canonical" && m.Canonical == "" {
					m.Canonical = href
				}
				if rel == "icon" || strings.Contains(rel, "shortcut") ||
					rel == "apple-touch-icon" {
					if m.Image == "" {
						m.Image = href
					}
				}
			case "meta":
				key := strings.ToLower(strings.TrimSpace(
					domx.Attr(n, "property") + " " + domx.Attr(n, "name")))
				content := domx.Attr(n, "content")
				if content == "" {
					break
				}
				assignMeta(m, key, content)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			childInert := inInert
			if c.Type == html.ElementNode && isInertTag(c.Data) {
				childInert = true
			}
			walk(c, childInert)
		}
	}

	// Head metadata is read from <head> only. Pages routinely embed markup
	// fragments inside inline scripts and templates, and those fragments
	// contain literal <title> and <meta> tags for social widgets
	// ("Instagram", tracking pixels, and so on) that outrank the real
	// og:title if the whole document is scanned.
	if head := domx.FindElements(doc, "head"); len(head) > 0 {
		walk(head[0], false)
	} else {
		// Malformed documents may omit <head>; fall back to the whole tree,
		// skipping script, style and template contents.
		walk(doc, false)
	}
	// The h1 lives in the body by design, so it is collected separately.
	collectH1(doc, m)
	return m
}

// collectH1 records the first non-empty h1 anywhere in the document.
func collectH1(doc *html.Node, m *Metadata) {
	var walk func(n *html.Node, inInert bool)
	walk = func(n *html.Node, inInert bool) {
		if m.h1 != "" {
			return
		}
		if n.Type == html.ElementNode {
			if isInertTag(n.Data) {
				inInert = true
			} else if n.Data == "h1" {
				if t := strings.TrimSpace(domx.TextOf(n)); t != "" {
					m.h1 = t
					return
				}
			}
		}
		if inInert {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, false)
		}
	}
	walk(doc, false)
}

// isInertTag reports elements whose text is script, style or template content
// rather than rendered document text.
func isInertTag(tag string) bool {
	switch tag {
	case "script", "style", "template", "noscript", "svg", "math", "head":
		return true
	}
	return false
}

// assignMeta maps a normalised meta key onto a field. Keys arrive space-
// separated because a tag may carry both property and name.
func assignMeta(m *Metadata, key, content string) {
	for _, k := range strings.Fields(key) {
		switch k {
		case "og:title":
			m.addTitle(content, "og")
		case "twitter:title":
			m.addTitle(content, "twitter")
		case "title", "dc.title", "dcterms.title":
			m.addTitle(content, "meta")
		case "og:site_name", "application-name":
			if m.SiteName == "" {
				m.SiteName = content
			}
		case "author", "article:author", "og:article:author", "dc.creator",
			"dcterms.creator", "twitter:creator", "parsely-author":
			if m.Byline == "" {
				m.Byline = content
			}
		case "article:published_time", "og:article:published_time", "datepublished",
			"dc.date", "dcterms.date", "pubdate", "publish-date", "date",
			"sailthru.date", "parsely-pub-date", "citation_publication_date":
			if m.Published.IsZero() {
				m.Published = parseDate(content)
			}
		case "article:modified_time", "og:updated_time", "datemodified",
			"dc.date.issued", "lastmod", "parsely-post-id":
			if m.Modified.IsZero() {
				m.Modified = parseDate(content)
			}
		case "og:image", "twitter:image", "twitter:image:src", "image", "thumbnail":
			if m.Image == "" {
				m.Image = content
			}
		case "og:description", "twitter:description", "description", "dc.description":
			if m.Description == "" {
				m.Description = content
			}
		case "og:type":
			if m.OgType == "" {
				m.OgType = content
			}
		}
	}
}

// titleCandidate is one possible title, tagged with where it came from.
type titleCandidate struct {
	value  string
	source string
}

// pickTitle chooses the best title from competing signals.
//
// No single source is reliable. Publishers put descriptions in JSON-LD
// `headline` (Wikipedia does), leave the site name glued onto og:title, and
// <title> is almost always "Headline | Site". So every candidate is scored on
// how much it looks like an actual headline rather than a description or a
// stamped <title>, and consensus between sources is rewarded.
func pickTitle(cands []titleCandidate, siteName string) string {
	type scored struct {
		text  string
		score float64
	}
	var best *scored
	for _, c := range cands {
		v := domx.NormSpace(c.value)
		if v == "" {
			continue
		}
		if len([]rune(v)) > 250 {
			// Far too long to be a headline; keep it only as a last resort.
			if best == nil {
				best = &scored{text: domx.TruncateRunes(v, 200), score: -100}
			}
			continue
		}
		score := 0.0
		lower := strings.ToLower(v)
		switch c.source {
		case "jsonld-name":
			score += 2
		case "jsonld-headline":
			score += 1
		case "og", "twitter":
			score += 1
		case "title":
			score += 0.5
		case "h1":
			score += 1
		}
		// A full sentence is a description, not a headline.
		if strings.HasSuffix(v, ".") || strings.HasSuffix(v, "!") {
			score -= 4
		}
		if len([]rune(v)) > 120 {
			score -= 3
		}
		// Penalise a site-name stamp, but only if we know the site name.
		if siteName != "" {
			seps := []string{" - ", " | ", " – ", " — ", " :: ", " » ", " • "}
			for _, sep := range seps {
				if strings.HasSuffix(v, sep+siteName) {
					score -= 2.5
				}
			}
			if strings.Contains(lower, strings.ToLower(siteName)) {
				score -= 0.5
			}
		}
		// Reward agreement between independent sources.
		for _, other := range cands {
			if other.value != c.value && strings.EqualFold(domx.NormSpace(other.value), v) {
				score += 1.5
				break
			}
		}
		// Reward a candidate that is a strict prefix of a longer one, which
		// usually means the shorter is the clean title and the longer appended
		// the site name.
		for _, other := range cands {
			o := domx.NormSpace(other.value)
			if len(o) > len(v) && strings.HasPrefix(o, v) {
				score += 0.75
				break
			}
		}
		if best == nil || score > best.score {
			best = &scored{text: v, score: score}
		}
	}
	if best == nil {
		return ""
	}
	return best.text
}

// stripSiteSuffix removes a trailing site name from a title.
func stripSiteSuffix(title, siteName string) string {
	if siteName == "" {
		return title
	}
	seps := []string{" - ", " | ", " – ", " — ", " :: ", " » ", " • "}
	for _, sep := range seps {
		if strings.HasSuffix(title, sep+siteName) {
			return strings.TrimSuffix(title, sep+siteName)
		}
	}
	return title
}

func parseDate(s string) time.Time { return dates.ParseTime(s) }
