package extract

import (
	"strings"

	"golang.org/x/net/html"

	"feedme/internal/domx"
)

// densityResult is the outcome of the link-density fallback.
type densityResult struct {
	Node  *html.Node
	Chars int
}

// blockTags are the elements that can act as an article container.
var blockTags = map[string]bool{
	"article": true, "section": true, "div": true, "main": true, "td": true,
	"blockquote": true, "pre": true, "form": true, "body": true,
}

// extractDensity is the last-resort scorer. It scores every block element by
// the amount of prose it holds relative to how much of it is inside links, and
// keeps the subtree of the winner. This is the same core idea jusText and
// trafilatura use, and it catches pages that Readability's candidate ranking
// rejects.
func extractDensity(doc *html.Node) *densityResult {
	root := doc
	if b := domx.FindElements(doc, "body"); len(b) > 0 {
		root = b[0]
	}

	type scored struct {
		node  *html.Node
		score float64
	}
	var best *scored

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && blockTags[n.Data] && domx.IsVisible(n) {
			text := textLen(n)
			if text >= 200 {
				linkText := linkTextLen(n)
				// Penalise link-heavy blocks: navigation and sidebars are
				// mostly anchors, articles are mostly prose.
				density := float64(text)
				if linkText > 0 {
					density = float64(text) * (1 - 0.6*float64(linkText)/float64(text))
				}
				// Reward block structure a little: prose without <p> tags is
				// usually a nav list.
				blocks := countBlocks(n)
				if blocks > 0 {
					density *= 1.0
				} else {
					density *= 0.85
				}
				if best == nil || density > best.score {
					best = &scored{node: n, score: density}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	if best == nil || best.score < 200 {
		return nil
	}
	// Trim the winner down to the part actually holding the prose.
	node := bestWithin(best.node)
	return &densityResult{Node: node, Chars: textLen(node)}
}

// linkTextLen returns how many characters of an element's text sit inside
// anchors.
func linkTextLen(n *html.Node) int {
	total := 0
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur.Type == html.ElementNode && cur.Data == "a" {
			total += len([]rune(domx.NormSpace(domx.TextOf(cur))))
			return
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return total
}

// isJunkText reports whether a short string is navigation chrome rather than
// prose. Used to reject containers that are mostly menu items.
func isJunkText(s string) bool {
	t := domx.NormSpace(s)
	if t == "" {
		return true
	}
	junk := []string{
		"read more", "read this", "continue reading", "learn more", "more info",
		"next", "previous", "prev", "home", "menu", "search", "share this",
		"subscribe", "sign up", "log in", "advertisement", "sponsored",
		"click here", "download", "follow us", "related articles", "comments",
	}
	lower := strings.ToLower(t)
	for _, j := range junk {
		if lower == j {
			return true
		}
	}
	return false
}
