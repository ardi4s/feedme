package domx

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"

	"feedme/internal/urlx"
)

// XPathPrefix marks a selector that should be read as XPath rather than CSS.
// It matches the convention the site-config format already uses, so one set of
// selectors can be written either way.
const XPathPrefix = "xpath:"

// SelectAll resolves a list of selectors to nodes, in selector order. A
// selector may be CSS, or XPath when prefixed with "xpath:". Entries that
// match nothing, or that fail to parse, are skipped rather than failing the
// whole call: site configs are written by hand and a typo in one field should
// not blank out the rest of the feed.
func SelectAll(doc *html.Node, selectors []string) []*html.Node {
	var out []*html.Node
	for _, sel := range selectors {
		sel = strings.TrimSpace(sel)
		if sel == "" || sel == "0" {
			continue
		}
		if rest, ok := strings.CutPrefix(sel, XPathPrefix); ok {
			rest = strings.TrimSpace(rest)
			if rest == "" {
				continue
			}
			nodes, err := htmlquery.QueryAll(doc, rest)
			if err != nil {
				continue
			}
			out = append(out, nodes...)
			continue
		}
		dq := goquery.NewDocumentFromNode(doc)
		dq.Find(sel).Each(func(_ int, s *goquery.Selection) {
			out = append(out, s.Nodes...)
		})
	}
	return out
}

// SelectOne returns the first node matched by the selectors, or nil.
func SelectOne(doc *html.Node, selectors []string) *html.Node {
	nodes := SelectAll(doc, selectors)
	if len(nodes) == 0 {
		return nil
	}
	return nodes[0]
}

// SelectText returns the normalised text of the first match, or "".
func SelectText(doc *html.Node, selectors []string) string {
	n := SelectOne(doc, selectors)
	if n == nil {
		return ""
	}
	return NormSpace(TextOf(n))
}

// SelectPlain returns the visible text of the first match with block
// boundaries preserved, or "".
//
// Use it for prose — a teaser, a byline, a caption. Use SelectText for a name or
// a title, where an inline element must not gain a space.
func SelectPlain(doc *html.Node, selectors []string) string {
	n := SelectOne(doc, selectors)
	if n == nil {
		return ""
	}
	return PlainText(n)
}

// SelectAttr returns the first non-empty attribute value among the matches.
// When the value looks like a URL it is resolved against base, because item
// links on a listing page are almost always relative.
func SelectAttr(doc *html.Node, selectors []string, attrName, base string) string {
	for _, n := range SelectAll(doc, selectors) {
		v := strings.TrimSpace(Attr(n, attrName))
		if v == "" {
			continue
		}
		return urlx.Resolve(v, base)
	}
	return ""
}

// ResolveURL makes a possibly relative href absolute against base. The order
// is (href, base).
func ResolveURL(href, base string) string { return urlx.Resolve(href, base) }

// Fragment wraps an HTML string in a container element and returns that
// container, so string-sourced markup (a JSON-LD articleBody, for example)
// flows through the same handling as DOM-sourced markup.
func Fragment(s string) *html.Node {
	doc, err := html.Parse(strings.NewReader("<div>" + s + "</div>"))
	if err != nil {
		return &html.Node{Type: html.ElementNode, Data: "div"}
	}
	if divs := FindElements(doc, "div"); len(divs) > 0 {
		return divs[0]
	}
	return doc
}

// ParseFragment parses an HTML fragment and returns the fragment root. The
// root is a <html> node; use FindElements to get inside it.
func ParseFragment(s string) *html.Node {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return &html.Node{}
	}
	return doc
}

// Body returns the document's <body>, or the document itself when the markup
// has no body element.
func Body(doc *html.Node) *html.Node {
	if b := FindElements(doc, "body"); len(b) > 0 {
		return b[0]
	}
	return doc
}
