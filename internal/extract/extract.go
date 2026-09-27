// Package extract turns an HTML page into a clean article: title, author,
// date, and body HTML with the site chrome removed.
package extract

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/net/html"

	"feedme/internal/config"

	"feedme/internal/domx"

	"feedme/internal/urlx"
)

// Article is the result of extraction.
type Article struct {
	URL         string
	Canonical   string
	Title       string
	Byline      string
	Published   time.Time
	Modified    time.Time
	ContentHTML string
	TextContent string
	Excerpt     string
	SiteName    string
	Image       string
	Lang        string
	WordCount   int
	// Strategy names the cascade step that produced the body.
	Strategy string
	// Attempts records every strategy tried, for diagnostics.
	Attempts []Attempt
}

// Attempt is one tried strategy and its outcome.
type Attempt struct {
	Name   string
	OK     bool
	Reason string
	Chars  int
}

// minBodyChars is the shortest body we accept. Below this, a "successful"
// extraction is almost always a navigation menu rather than an article.
const minBodyChars = 250

// ErrNoContent means no strategy produced a usable article body.
var ErrNoContent = errors.New("no article content could be extracted")

// Options configures extraction.
type Options struct {
	// PageURL is the URL the HTML came from. Required for link resolution.
	PageURL string
	// Site is an optional per-host override.
	Site *config.Site
	// MaxElements caps parsed DOM size.
	MaxElements int
	// Logger receives strategy diagnostics.
	Logger *slog.Logger
	// Prune enables the allow-list pass. Defaults to true.
	Prune bool
}

// FromHTML extracts an article from an HTML document.
func FromHTML(body []byte, opts Options) (*Article, error) {
	if opts.MaxElements <= 0 {
		opts.MaxElements = 100000
	}
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	base := opts.PageURL
	if base == "" {
		base = "https://invalid.local/"
	}
	// A <base href> overrides the request URL for relative resolution.
	if b := domx.FindElements(doc, "base"); len(b) > 0 {
		if href := domx.Attr(b[0], "href"); href != "" {
			base = urlx.Resolve(href, opts.PageURL)
		}
	}

	art := &Article{URL: opts.PageURL, Lang: readLang(doc)}
	meta := readMetadata(doc)
	ld := extractJSONLD(doc)

	// Metadata precedence: JSON-LD is the publisher's own assertion, so it
	// outranks meta tags, which outrank Readability's inference.
	art.SiteName = firstNonEmpty(ld.SiteName, meta.SiteName)
	cands := meta.TitleCandidates()
	if ld.Name != "" {
		cands = append(cands, titleCandidate{value: ld.Name, source: "jsonld-name"})
	}
	if ld.Headline != "" {
		cands = append(cands, titleCandidate{value: ld.Headline, source: "jsonld-headline"})
	}
	if meta.H1() != "" {
		cands = append(cands, titleCandidate{value: meta.H1(), source: "h1"})
	}
	art.Title = pickTitle(cands, art.SiteName)
	art.Byline = firstNonEmpty(ld.Byline, meta.Byline)
	art.Published = firstNonZero(ld.Published, meta.Published, bestTimeIn(doc))
	art.Modified = firstNonZero(ld.Modified, meta.Modified)
	art.Image = firstNonEmpty(ld.Image, meta.Image)
	art.Canonical = urlx.Resolve(meta.Canonical, base)
	if art.Canonical == "" {
		art.Canonical = opts.PageURL
	}

	var (
		chosenNode *html.Node
		strategy   string
	)
	prune := opts.Prune
	if opts.Site != nil {
		prune = opts.Site.PruneEnabled()
	}

	// 1. Site config, when present, is an explicit instruction and wins.
	if opts.Site != nil && len(opts.Site.Body) > 0 {
		if nodes := domx.SelectAll(doc, opts.Site.Body); len(nodes) > 0 {
			n := nodes[0]
			chars := textLen(n)
			art.Attempts = append(art.Attempts, Attempt{
				Name: "siteconfig", OK: chars >= minBodyChars,
				Chars: chars, Reason: reasonFor(chars >= minBodyChars, chars)})
			if chars >= minBodyChars {
				chosenNode, strategy = n, "siteconfig"
			}
		} else {
			art.Attempts = append(art.Attempts, Attempt{
				Name: "siteconfig", OK: false, Reason: "no node matched the configured body selector"})
		}
	}

	// 2. JSON-LD articleBody: the publisher already told us the text.
	if chosenNode == nil && ld.Body != "" {
		chars := len([]rune(domx.NormSpace(ld.Body)))
		art.Attempts = append(art.Attempts, Attempt{
			Name: "jsonld", OK: chars >= minBodyChars, Chars: chars,
			Reason: reasonFor(chars >= minBodyChars, chars)})
		if chars >= minBodyChars {
			chosenNode, strategy = domx.Fragment(ld.Body), "jsonld"
		}
	}

	// 3. Explicit structural containers.
	if chosenNode == nil {
		if r := extractSemantic(doc); r != nil {
			art.Attempts = append(art.Attempts, Attempt{
				Name: "semantic", OK: true, Chars: r.Chars, Reason: "matched a declared content container"})
			chosenNode, strategy = r.Node, "semantic"
		} else {
			art.Attempts = append(art.Attempts, Attempt{
				Name: "semantic", OK: false, Reason: "no <article>, itemprop, <main> or role=main of sufficient length"})
		}
	}

	// 4. Readability: the general-purpose heuristic.
	if chosenNode == nil {
		if r := extractReadability(doc, opts.PageURL, opts.MaxElements, opts.Logger); r != nil {
			art.Attempts = append(art.Attempts, Attempt{
				Name: "readability", OK: r.Chars >= minBodyChars, Chars: r.Chars,
				Reason: reasonFor(r.Chars >= minBodyChars, r.Chars)})
			if r.Chars >= minBodyChars {
				chosenNode, strategy = r.Node, "readability"
				art.Title = firstNonEmpty(art.Title, r.Title)
				art.Byline = firstNonEmpty(art.Byline, r.Byline)
				art.SiteName = firstNonEmpty(art.SiteName, r.SiteName)
				art.Excerpt = firstNonEmpty(art.Excerpt, r.Excerpt)
			}
		} else {
			reason := "readability found no candidate"
			if !probablyReaderable(doc) {
				reason = "page failed the readerable pre-check (likely no server-rendered text)"
			}
			art.Attempts = append(art.Attempts, Attempt{Name: "readability", OK: false, Reason: reason})
		}
	}

	// 5. Link-density scoring: catches pages the cascade above rejected.
	if chosenNode == nil {
		if r := extractDensity(doc); r != nil {
			art.Attempts = append(art.Attempts, Attempt{
				Name: "density", OK: r.Chars >= minBodyChars, Chars: r.Chars,
				Reason: reasonFor(r.Chars >= minBodyChars, r.Chars)})
			if r.Chars >= minBodyChars {
				chosenNode, strategy = r.Node, "density"
			}
		} else {
			art.Attempts = append(art.Attempts, Attempt{
				Name: "density", OK: false, Reason: "no block held enough low-link-density prose"})
		}
	}

	if chosenNode == nil {
		return art, ErrNoContent
	}

	stripNodes := domx.SelectAll(doc, siteStrips(opts.Site))
	art.ContentHTML = sanitize(chosenNode, sanitizeOptions{
		BaseURL:      base,
		Prune:        prune,
		ExtraStrips:  stripNodes,
		StripClasses: siteStripClasses(opts.Site),
	})

	// Derive text and counts from the sanitised output, not the raw node, so
	// the reported word count matches what a reader will actually see.
	art.TextContent = domx.PlainText(domx.ParseFragment(art.ContentHTML))
	art.WordCount = domx.WordCount(art.TextContent)
	art.Strategy = strategy
	art.Excerpt = firstNonEmpty(art.Excerpt, meta.Description,
		domx.Excerpt(art.TextContent, 280))

	if art.Image != "" {
		art.Image = urlx.Resolve(cleanTracking(art.Image), base)
	}
	// A page with a date far in the future is a template bug, not news.
	if !art.Published.IsZero() && art.Published.After(time.Now().Add(48*time.Hour)) {
		art.Published = time.Time{}
	}
	return art, nil
}

func siteStrips(s *config.Site) []string {
	if s == nil {
		return nil
	}
	return append(append([]string{}, s.Strip...), s.Item...)
}

func siteStripClasses(s *config.Site) []string {
	if s == nil {
		return nil
	}
	return s.StripIDOrClass
}

func reasonFor(ok bool, chars int) string {
	if ok {
		return fmt.Sprintf("%d characters of content", chars)
	}
	return fmt.Sprintf("only %d characters, below the %d minimum", chars, minBodyChars)
}

func readLang(doc *html.Node) string {
	if h := domx.FindElements(doc, "html"); len(h) > 0 {
		if l := domx.Attr(h[0], "lang"); l != "" {
			return l
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstNonZero(vals ...time.Time) time.Time {
	for _, v := range vals {
		if !v.IsZero() {
			return v
		}
	}
	return time.Time{}
}
