package extract

import (
	"strings"

	"golang.org/x/net/html"

	"feedme/internal/domx"
)

// semanticResult is the outcome of the structural strategy.
type semanticResult struct {
	Node  *html.Node
	Chars int
}

// extractSemantic looks for containers the publisher explicitly marked as the
// article body: <article>, microdata itemprops, <main>, or role="main".
//
// A page with several <article> elements is a list, not an article, so this
// strategy deliberately returns nothing in that case and lets Readability or
// the density scorer handle it.
func extractSemantic(doc *html.Node) *semanticResult {
	// 1. Microdata is the most explicit signal there is.
	if n := domx.FindByAnyAttr(doc, "itemprop", "itemtype"); n != nil {
		if body := findItempropBody(doc); body != nil {
			if chars := textLen(body); chars >= 200 {
				return &semanticResult{Node: body, Chars: chars}
			}
		}
	}

	articles := domx.FindElements(doc, "article")
	switch len(articles) {
	case 0:
		// Fall through to the generic containers below.
	case 1:
		if n := bestWithin(articles[0]); n != nil {
			if chars := textLen(n); chars >= 200 {
				return &semanticResult{Node: n, Chars: chars}
			}
		}
	default:
		// Multiple <article> elements means we are probably looking at an
		// index page. Pick the densest one and require it to be clearly
		// longer than the others, otherwise treat the page as a list.
		best := articles[0]
		bestLen := textLen(best)
		for _, a := range articles[1:] {
			if l := textLen(a); l > bestLen {
				best, bestLen = a, l
			}
		}
		if bestLen >= 500 {
			return &semanticResult{Node: best, Chars: bestLen}
		}
		return nil
	}

	// 2. <main> and role="main" are the standard page-level wrappers.
	for _, n := range append(domx.FindElements(doc, "main"),
		findByRole(doc, "main")...) {
		if inner := bestWithin(n); inner != nil {
			if chars := textLen(inner); chars >= 200 {
				return &semanticResult{Node: inner, Chars: chars}
			}
		}
	}
	return nil
}

// findItempropBody locates a microdata articleBody container.
func findItempropBody(doc *html.Node) *html.Node {
	var found *html.Node
	for _, n := range allElements(doc) {
		ip := strings.ToLower(domx.Attr(n, "itemprop"))
		if ip == "articlebody" || ip == "text" {
			if textLen(n) >= 200 {
				if found == nil || textLen(n) > textLen(found) {
					found = n
				}
			}
		}
	}
	return found
}

// findByRole returns elements with the given ARIA role.
func findByRole(doc *html.Node, role string) []*html.Node {
	var out []*html.Node
	for _, n := range allElements(doc) {
		if strings.EqualFold(domx.Attr(n, "role"), role) {
			out = append(out, n)
		}
	}
	return out
}

func allElements(root *html.Node) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// containerTags are the elements worth descending into when narrowing a
// wrapper down to the part holding the prose.
var containerTags = map[string]bool{
	"div": true, "section": true, "td": true, "blockquote": true, "form": true,
}

// bestWithin narrows a wrapper element to the most specific descendant that
// still holds the bulk of its text. This is what turns <main> wrapping a
// header, an article, and a sidebar into just the article.
func bestWithin(n *html.Node) *html.Node {
	total := textLen(n)
	if total == 0 {
		return n
	}
	best := n
	bestLen := total
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || !domx.IsVisible(c) {
				continue
			}
			if containerTags[c.Data] {
				if l := textLen(c); l > bestLen*3/4 && l > 200 {
					best, bestLen = c, l
					walk(c)
				}
			}
		}
	}
	walk(n)
	return best
}

// textLen returns the length of an element's text content.
func textLen(n *html.Node) int {
	if n == nil {
		return 0
	}
	return len([]rune(domx.NormSpace(domx.TextOf(n))))
}

// countBlocks counts paragraph-level descendants, used as a tiebreaker.
func countBlocks(n *html.Node) int {
	count := 0
	for _, tag := range []string{"p", "pre", "blockquote", "li"} {
		count += len(domx.FindElements(n, tag))
	}
	return count
}
