package listpage

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"

	"feedme/internal/domx"
	"feedme/internal/urlx"
)

// chromeTags hold page furniture. Links inside these are menu entries, not
// articles, so they must not seed a detected pattern.
//
// This is a policy list rather than generic DOM knowledge, which is why it
// lives here and not in domx: a future caller harvesting, say, citation links
// would want a different set.
var chromeTags = map[string]bool{
	"nav": true, "header": true, "footer": true, "aside": true,
	"form": true, "select": true, "menu": true, "dialog": true,
}

// chromeClasses are the class and id fragments that mark page furniture even
// inside a <div> soup, which is what modern sites actually ship.
var chromeClasses = []string{
	"nav", "navbar", "menu", "sidebar", "side-bar", "footer", "header",
	"masthead", "breadcrumb", "banner", "advert", "ads", "promo", "widget",
	"social", "share", "subscribe", "newsletter", "comment", "related",
	"recommend", "trending", "popular", "tag-list", "pagination", "pager",
}

// Candidate is a detected URL pattern together with the evidence for it.
type Candidate struct {
	// Selector is a CSS selector that matches the item containers for this
	// pattern, suitable for storing as a feed parameter or site config.
	Selector string
	// Pattern is the URL shape, for example
	// "kontan.co.id/news/{n}/{n}/{n}/{slug}".
	Pattern string
	// Count is how many links on the page matched.
	Count int
	// DistinctSlugs is how many different trailing segments matched, which
	// separates a list of articles from several links to the same page.
	DistinctSlugs int
	// Depth is the number of path segments in the pattern.
	Depth int
	// Host is the hostname the pattern was observed on.
	Host string
}

// Result is the outcome of automatic detection.
type Result struct {
	// Selector is the winning candidate, ready to pass to FromSelectors.
	Selector string
	// Confidence is "high", "medium" or "low". Below "medium" the caller
	// should offer the alternatives rather than silently use the guess.
	Confidence string
	// Candidates is every pattern found, best first, so a UI can present
	// the runner-ups as a choice.
	Candidates []Candidate
	// Reason explains the decision in a form worth showing a user.
	Reason string
	// Items are the extracted entries for Selector, ready to use. They are
	// derived from the winning URL pattern's own anchors, so a page whose
	// navigation shares the same markup cannot contaminate them.
	Items []Item
}

// Detect finds the item list on a page without any selector being given.
//
// The approach is to collect every link that could plausibly be an article,
// group them by the shape of their URL, and rank the groups. It is a heuristic,
// so it always returns its alternatives and a confidence rather than a bare
// answer: a wrong guess silently produces a feed full of navigation links.
func Detect(doc *html.Node, opts Options) (Result, error) {
	if len(opts.Item) > 0 {
		return Result{}, fmt.Errorf("listpage: Detect called with an item selector set")
	}
	refs := linkRefs(doc, opts)
	if len(refs) == 0 {
		return Result{}, ErrNoItems
	}
	links := make([]string, 0, len(refs))
	for _, r := range refs {
		links = append(links, r.url)
	}

	groups := urlx.GroupPatterns(links)
	if len(groups) == 0 {
		return Result{}, ErrNoItems
	}

	minItems := opts.MinItems
	if minItems <= 0 {
		minItems = DefaultMinItems
	}

	candidates := make([]Candidate, 0, len(groups))
	for _, g := range groups {
		if g.Count < minItems {
			continue
		}
		distinct := distinctCount(g.Slugs)
		// A pattern whose matches all point at the same page is a link that
		// happens to repeat, not a list.
		if distinct < 2 {
			continue
		}
		candidates = append(candidates, Candidate{
			Selector:      containerSelector(g.Raw, refs),
			Pattern:       g.Raw,
			Count:         g.Count,
			DistinctSlugs: distinct,
			Depth:         g.Depth,
			Host:          g.Host,
		})
	}
	if len(candidates) == 0 {
		return Result{}, fmt.Errorf("%w: no URL pattern had at least %d links with differing slugs", ErrNoItems, minItems)
	}

	best := candidates[0]
	conf, reason := grade(best, candidates, minItems)
	return Result{
		Selector:   best.Selector,
		Confidence: conf,
		Candidates: candidates,
		Reason:     reason,
		Items:      itemsForPattern(best.Pattern, refs, opts),
	}, nil
}

// itemsForPattern extracts one item per container of a detected URL pattern.
// Because it starts from the pattern's own anchors, it never picks up menu or
// footer links that merely happen to share the container's tag or class.
func itemsForPattern(pattern string, refs []linkRef, opts Options) []Item {
	anchors := matchingAnchors(pattern, refs)
	if len(anchors) == 0 {
		return nil
	}
	counts := anchorSubtreeCounts(anchors)
	seenNode := map[*html.Node]bool{}
	seenLink := map[string]bool{}
	out := make([]Item, 0, len(anchors))
	for _, a := range anchors {
		container := singleAnchorContainer(a, counts)
		if container == nil || seenNode[container] {
			continue
		}
		seenNode[container] = true
		it, ok := itemFromNode(container, opts)
		if !ok {
			continue
		}
		key := urlx.PathKey(it.Link)
		if seenLink[key] {
			continue
		}
		seenLink[key] = true
		out = append(out, it)
	}
	return out
}

// linkRef keeps a harvested link together with the node it came from, because
// the node is what identifies the item container later.
type linkRef struct {
	url  string
	node *html.Node
}

func linkRefs(doc *html.Node, opts Options) []linkRef {
	var refs []linkRef
	domx.Walk(doc, func(n *html.Node) bool {
		if len(refs) >= MaxCandidateLinks {
			return false
		}
		if n.Type != html.ElementNode {
			return true
		}
		// Descending into furniture would let menu links into the counts, so
		// the walk stops here rather than filtering afterwards.
		if isChrome(n) {
			return false
		}
		if n.Data != "a" {
			return true
		}
		href := forceHost(domx.ResolveURL(domx.Attr(n, "href"), opts.BaseURL), opts)
		if !acceptable(href, opts) {
			return true
		}
		if !domx.IsVisible(n) {
			return true
		}
		refs = append(refs, linkRef{url: href, node: n})
		return true
	})
	return refs
}

// isChrome reports whether a node is page furniture. Both the tag and the
// class/id vocabulary are checked, because a nav bar is sometimes a <div>.
func isChrome(n *html.Node) bool {
	if chromeTags[n.Data] {
		return true
	}
	id := strings.ToLower(domx.Attr(n, "id"))
	if id != "" {
		for _, c := range chromeClasses {
			if strings.Contains(id, c) {
				return true
			}
		}
	}
	for _, class := range domx.ClassAttr(n) {
		class = strings.ToLower(class)
		for _, c := range chromeClasses {
			if strings.Contains(class, c) {
				return true
			}
		}
	}
	return false
}

func distinctCount(slugs []string) int {
	seen := map[string]bool{}
	for _, s := range slugs {
		if s != "" {
			seen[strings.ToLower(s)] = true
		}
	}
	return len(seen)
}

// containerSelector builds a CSS selector for the elements that each hold one
// item of a pattern, or "" when no safe selector can be derived.
//
// The item container is the highest ancestor of an anchor whose subtree holds
// that one anchor and no other item's anchor. On a card grid that is the card
// itself; on a plain list it is the <li>; when the anchor wraps the whole card
// it is the anchor. Taking the highest single-item ancestor — rather than the
// child of the links' common ancestor — is what keeps a multi-section portal
// from collapsing to one wrapper that holds every headline.
func containerSelector(pattern string, refs []linkRef) string {
	anchors := matchingAnchors(pattern, refs)
	if len(anchors) == 0 {
		return ""
	}
	counts := anchorSubtreeCounts(anchors)
	// The selector has to match every item, so the containers must agree on
	// their element name and class. When they do not, the most common shape
	// wins.
	sig := map[string]int{}
	for _, a := range anchors {
		if sel := selectorFor(singleAnchorContainer(a, counts)); sel != "" {
			sig[sel]++
		}
	}
	bestSig, bestN := "", -1
	for sel, n := range sig {
		// Ties break on the selector name so the result does not depend on map
		// iteration order.
		if n > bestN || (n == bestN && sel < bestSig) {
			bestSig, bestN = sel, n
		}
	}
	return bestSig
}

// anchorSubtreeCounts counts, for every node, how many of the pattern's anchors
// lie in its subtree. One walk to the root per anchor is enough.
func anchorSubtreeCounts(anchors []*html.Node) map[*html.Node]int {
	counts := make(map[*html.Node]int, len(anchors)*4)
	for _, a := range anchors {
		for n := a; n != nil; n = n.Parent {
			counts[n]++
		}
	}
	return counts
}

// singleAnchorContainer returns the highest ancestor of the anchor that still
// contains only this one anchor. Its parent holds a second item, so this node
// represents exactly one item.
func singleAnchorContainer(anchor *html.Node, counts map[*html.Node]int) *html.Node {
	best := anchor
	for n := anchor; n != nil; n = n.Parent {
		if counts[n] != 1 {
			break
		}
		best = n
	}
	return best
}

func matchingAnchors(pattern string, refs []linkRef) []*html.Node {
	var out []*html.Node
	for _, r := range refs {
		p, ok := urlx.PatternOf(r.url)
		if ok && p.Raw == pattern {
			out = append(out, r.node)
		}
	}
	return out
}

// selectorFor names one element, preferring an id and then a class over a bare
// tag, which is rarely a usable selector.
func selectorFor(n *html.Node) string {
	if n == nil {
		return ""
	}
	if id := domx.Attr(n, "id"); id != "" {
		return "#" + id
	}
	for _, class := range domx.ClassAttr(n) {
		if class != "" {
			return "." + class
		}
	}
	tag := n.Data
	if tag == "body" || tag == "html" {
		return ""
	}
	return tag
}

// grade turns the winning candidate's numbers into a confidence and a sentence
// a user can act on.
func grade(best Candidate, all []Candidate, minItems int) (string, string) {
	var second int
	if len(all) > 1 {
		second = all[1].Count
	}
	// A pattern with no runner-up is not high confidence. Nothing on the page
	// argues against it, but nothing argues for it either, and presenting a
	// lone guess as certain is how a feed ends up full of navigation.
	switch {
	case len(all) < 2:
		return "medium", fmt.Sprintf(
			"%d links share the pattern %s, and it is the only pattern on the page. Worth checking before saving.",
			best.Count, best.Pattern)
	case best.Count >= maxInt(2*minItems, 10) && best.Count >= 2*second:
		return "high", fmt.Sprintf(
			"%d links share the pattern %s, well clear of the next candidate (%d).",
			best.Count, best.Pattern, second)
	case best.Count >= minItems && best.Count > second:
		return "medium", fmt.Sprintf(
			"%d links share the pattern %s; the next candidate has %d.",
			best.Count, best.Pattern, second)
	default:
		return "low", fmt.Sprintf(
			"best guess is %s with only %d links, close to the %d-link alternative. Check the selector before saving.",
			best.Pattern, best.Count, second)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
