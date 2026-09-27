// Package domx holds the low-level HTML helpers shared by every part of
// feedme that touches a parsed document: attribute access, text extraction,
// selector lookup and word counting.
//
// It exists so that the list-page parser, the article extractor and the feed
// renderer all agree on what "the text of a node" or "a CSS selector" means.
// Nothing in here knows about feeds, articles or sites; it is purely a
// vocabulary layer over golang.org/x/net/html.
package domx

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Attr returns an attribute value, or "" when absent. Lookups are
// case-insensitive, because real-world markup mixes "itemProp" and "itemprop".
func Attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// HasAttr reports whether an attribute is present at all, regardless of value.
// A bare `hidden` and `hidden=""` mean the same thing to a browser.
func HasAttr(n *html.Node, key string) bool {
	if n == nil {
		return false
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}

// ClassAttr returns the class attribute split into tokens.
func ClassAttr(n *html.Node) []string {
	return strings.Fields(Attr(n, "class"))
}

// TextOf returns the concatenated rendered text of a node's subtree.
//
// Subtrees whose tags are inert are not descended into. Article containers
// routinely hold inline scripts, ad tags and JSON-LD blocks; including them
// would put JavaScript source into the article text and inflate the word
// count. This is also what stops a literal <title> embedded in a page's own
// JavaScript from being read as the article's title.
func TextOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && IsInert(n.Data) {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// RawTextOf returns every text node beneath n, including those inside inert
// tags such as <script> and <style>.
//
// This is the counterpart to TextOf, and the two are not interchangeable.
// TextOf answers "what does the reader see", which is what article extraction
// and word counting need. RawTextOf answers "what is written in this element",
// which is how a JSON-LD block is read: the payload is inert markup that
// happens to be a script tag, and skipping it would find no article at all.
func RawTextOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// NormSpace collapses runs of whitespace into single spaces and trims.
func NormSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// IsVisible reports whether a node is not explicitly hidden. This only catches
// attribute-level hiding; CSS-class hiding is left to caller-side heuristics
// because the class names that mean "hidden" are site-specific.
func IsVisible(n *html.Node) bool {
	if n == nil || n.Type == html.CommentNode {
		return false
	}
	if style := Attr(n, "style"); style != "" {
		s := strings.ToLower(strings.ReplaceAll(style, " ", ""))
		if strings.Contains(s, "display:none") || strings.Contains(s, "visibility:hidden") {
			return false
		}
	}
	if HasAttr(n, "hidden") {
		return false
	}
	if Attr(n, "aria-hidden") == "true" {
		return false
	}
	return true
}

// InnerHTML serialises a node's children.
func InnerHTML(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&b, c)
	}
	return b.String()
}

// RenderHTML serialises a node itself, not just its children.
func RenderHTML(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	_ = html.Render(&b, n)
	return b.String()
}

// IsInert reports whether a tag's contents are script, style or template text
// rather than rendered document text. Walking into these picks up literal
// <title> and <meta> tags that pages embed in their JavaScript.
func IsInert(tag string) bool {
	switch tag {
	case "script", "style", "template", "noscript", "svg", "math":
		return true
	}
	return false
}

// FindElements returns all descendant elements with the given tag name, in
// document order.
func FindElements(root *html.Node, tag string) []*html.Node {
	var out []*html.Node
	Walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && (tag == "" || n.Data == tag) {
			out = append(out, n)
		}
		return true
	})
	return out
}

// FindByClassToken returns all elements whose class list contains token.
// Matching whole tokens avoids the classic "col" matching "column" mistake.
func FindByClassToken(root *html.Node, token string) []*html.Node {
	var out []*html.Node
	Walk(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		for _, c := range ClassAttr(n) {
			if c == token {
				out = append(out, n)
				break
			}
		}
		return true
	})
	return out
}

// FindByID returns the first element carrying the given id attribute. The
// id_or_class feed parameter and several site configs need this by name, and
// an id lookup must not be spelled as a CSS selector with quoting rules.
func FindByID(root *html.Node, id string) *html.Node {
	if id == "" {
		return nil
	}
	var out *html.Node
	Walk(root, func(n *html.Node) bool {
		if out != nil {
			return false
		}
		if n.Type == html.ElementNode && Attr(n, "id") == id {
			out = n
			return false
		}
		return true
	})
	return out
}

// FindWithClassLike returns elements whose class list contains a token starting
// with prefix. Site builds routinely hash or namespace their class names
// (".berita-abc123"), and a prefix match survives that churn where an exact
// match does not.
func FindWithClassLike(root *html.Node, prefix string) []*html.Node {
	if prefix == "" {
		return nil
	}
	var out []*html.Node
	Walk(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		for _, c := range ClassAttr(n) {
			if strings.HasPrefix(c, prefix) {
				out = append(out, n)
				break
			}
		}
		return true
	})
	return out
}

// FindByAnyAttr returns the first descendant carrying one of the given
// attributes, breadth-first so the nearest node wins.
func FindByAnyAttr(root *html.Node, names ...string) *html.Node {
	queue := []*html.Node{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.Type == html.ElementNode {
			for _, name := range names {
				if HasAttr(n, name) {
					return n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			queue = append(queue, c)
		}
	}
	return nil
}

// Walk visits every node in the subtree, including the root, stopping early if
// fn returns false.
func Walk(root *html.Node, fn func(*html.Node) bool) {
	if root == nil {
		return
	}
	var stack []*html.Node
	stack = append(stack, root)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !fn(n) {
			continue
		}
		// Pushed in reverse so children come out in document order.
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
}

// WalkElements visits element nodes only.
func WalkElements(root *html.Node, fn func(*html.Node) bool) {
	Walk(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		return fn(n)
	})
}

// Ancestors returns a node's ancestors, closest first.
func Ancestors(n *html.Node) []*html.Node {
	var out []*html.Node
	for p := n.Parent; p != nil; p = p.Parent {
		out = append(out, p)
	}
	return out
}

// HasAncestorTag reports whether any ancestor of n has one of the given tags.
func HasAncestorTag(n *html.Node, tags ...string) bool {
	for _, a := range Ancestors(n) {
		if a.Type != html.ElementNode {
			continue
		}
		for _, t := range tags {
			if a.Data == t {
				return true
			}
		}
	}
	return false
}

// WordCount counts whitespace-separated words, treating CJK ideographs as one
// word each so Chinese and Japanese articles do not read as a single word.
func WordCount(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		if IsCJK(r) {
			n++
			inWord = false
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v' {
			inWord = false
			continue
		}
		if !inWord {
			n++
			inWord = true
		}
	}
	return n
}

// IsCJK reports whether r is a CJK ideograph, kana or hangul syllable.
func IsCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK unified ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // extension A
		(r >= 0x3040 && r <= 0x30FF) || // kana
		(r >= 0xAC00 && r <= 0xD7AF) // hangul syllables
}

// TruncateRunes shortens s to at most n runes of content, respecting rune
// boundaries so multi-byte text is never cut mid-character.
//
// The limit counts content only: a truncated result carries a trailing
// ellipsis and so is n+1 runes long. Callers that need a hard total width must
// reserve one rune for it.
func TruncateRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:n])) + "…"
}

// Excerpt builds a plain-text summary of at most n runes.
func Excerpt(text string, n int) string {
	return TruncateRunes(NormSpace(text), n)
}

// blockElements are the tags that end a line in rendered text. Turning them
// into a whitespace boundary is what keeps a heading from running into the
// paragraph beneath it when HTML is flattened to plain text.
var blockElements = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"br": true, "dd": true, "div": true, "dl": true, "dt": true,
	"fieldset": true, "figcaption": true, "figure": true, "footer": true,
	"form": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "header": true, "hr": true, "li": true, "main": true,
	"nav": true, "ol": true, "p": true, "pre": true, "section": true,
	"table": true, "tbody": true, "td": true, "tfoot": true, "th": true,
	"thead": true, "tr": true, "ul": true,
}

// PlainText returns the visible text of n with block boundaries preserved as
// whitespace.
//
// It differs from TextOf on purpose. TextOf answers "what does a reader see",
// where <h1>Title</h1><p>Body</p> is two visual lines but the concatenated
// string is "TitleBody" — fine for comparing or searching, wrong for a feed
// description that a person will read. PlainText is for the second case.
func PlainText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if IsInert(n.Data) {
				return
			}
			if blockElements[n.Data] {
				b.WriteByte('\n')
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockElements[n.Data] {
			b.WriteByte('\n')
		}
	}
	walk(n)
	return NormSpace(b.String())
}

// TextNodes returns the visible text nodes beneath n, skipping inert subtrees.
//
// It exists because flattened text loses element boundaries, and some checks
// depend on them. A byline label written as
// `<a>Judul</a><span>Oleh: Rina</span>` flattens to "JudulOleh: Rina", which
// no amount of string matching can tell apart from the single word "juduloleh".
func TextNodes(n *html.Node) []*html.Node {
	if n == nil {
		return nil
	}
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && IsInert(n.Data) {
			return
		}
		if n.Type == html.TextNode && strings.TrimSpace(n.Data) != "" {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}
