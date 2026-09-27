package extract

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"feedme/internal/domx"

	"feedme/internal/urlx"
)

// dropTags are removed outright along with their content.
var dropTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"form": true, "button": true, "select": true, "option": true, "textarea": true,
	"svg": true, "canvas": true, "object": true, "embed": true, "applet": true,
	"link": true, "meta": true, "base": true, "input": true, "label": true,
	"fieldset": true, "dialog": true, "map": true, "area": true, "audio": true,
	"video": true, "source": true, "track": true,
}

// keepTags is the allow-list for the pruned pass. When prune is on we drop any
// element outside this set, which removes a lot of presentational cruft.
var keepTags = map[string]bool{
	"p": true, "br": true, "hr": true, "div": true, "span": true, "section": true,
	"article": true, "main": true, "aside": true, "header": true, "footer": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,
	"strong": true, "b": true, "em": true, "i": true, "u": true, "s": true,
	"small": true, "sub": true, "sup": true, "mark": true, "abbr": true,
	"cite": true, "q": true, "blockquote": true, "pre": true, "code": true,
	"kbd": true, "samp": true, "var": true, "time": true, "a": true,
	"img": true, "picture": true, "source": true, "figure": true, "figcaption": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
	"th": true, "td": true, "caption": true, "colgroup": true, "col": true,
	"iframe": true,
}

// junkClassRe matches the id/class of chrome that should never appear in an
// article body: share bars, related-article rails, newsletter modals, cookie
// notices, ad slots, comment forms and social embeds.
var junkClassRe = regexp.MustCompile(`(?i)(^|[\s_-])(share|sharing|social|sharedaddy|addtoany|related|recommend|recirc|promo|advert|advertisement|\bads?\b|adslot|adbox|sponsor|sponsored|newsletter|signup|subscri|cookie|consent|gdpr|paywall|comment|disqus|livefyre|sidebar|side-bar|widget|nav|navbar|menu|breadcrumb|pagination|pager|tag-list|tagcloud|author-box|byline-social|meta-share|jp-related|newsletter-form|trending|most-popular|read-next|continue-reading|skip-link|back-to-top|banner|header-ad|footer-ad|outbrain|taboola|newsletter-signup|login|modal|overlay|lightbox)([\s_-]|$)`)

// lazyImageAttrs are the attributes lazy-loading scripts stash the real image
// URL in, in rough order of how often they are used.
var lazyImageAttrs = []string{
	"data-src", "data-original", "data-lazy-src", "data-actualsrc", "data-hi-res-src",
	"data-lazy", "data-image", "data-img", "data-url", "data-echo", "data-full-src",
	"data-srcset",
}

// trackingParams are stripped from link targets so feed readers do not record
// per-subscriber analytics.
var trackingParams = map[string]bool{
	"utm_source": true, "utm_medium": true, "utm_campaign": true, "utm_term": true,
	"utm_content": true, "utm_id": true, "utm_name": true, "utm_reader": true,
	"utm_brand": true, "utm_social": true, "utm_social-type": true,
	"fbclid": true, "gclid": true, "dclid": true, "msclkid": true, "twclid": true,
	"igshid": true, "mc_cid": true, "mc_eid": true, "yclid": true, "_ga": true,
	"ref_src": true, "ref_url": true, "spm": true, "cmpid": true, "ncid": true,
	"smid": true, "sr_share": true, "__twitter_impression": true,
}

// sanitizeOptions controls the cleanup pass.
type sanitizeOptions struct {
	// BaseURL is used to absolutise relative links and images.
	BaseURL string
	// Prune drops elements outside the allow-list.
	Prune bool
	// ExtraStrips are site-config selector results to remove.
	ExtraStrips []*html.Node
	// StripClasses are additional id/class substrings to strip.
	StripClasses []string
}

// sanitize cleans an extracted content node in place and returns the HTML.
//
// The output is fed to feed readers, which may render it, so the result is
// treated as untrusted: scripts, event handlers and javascript: URLs are
// removed regardless of what the source page contained.
func sanitize(n *html.Node, opts sanitizeOptions) string {
	if n == nil {
		return ""
	}
	for _, extra := range opts.ExtraStrips {
		removeNode(extra)
	}
	pruneTree(n, opts)
	fixupTree(n, opts)
	// Unwrap containers that ended up holding a single child; they add nothing.
	unwrapRedundant(n)
	cleanupEmpty(n)
	// Collapse runs of blank text left behind by the removals.
	return domx.InnerHTML(n)
}

func removeNode(n *html.Node) {
	if n == nil || n.Parent == nil {
		return
	}
	n.Parent.RemoveChild(n)
}

// pruneTree removes junk and, when enabled, anything outside the allow-list.
func pruneTree(n *html.Node, opts sanitizeOptions) {
	for _, c := range childrenOf(n) {
		if c.Type == html.CommentNode {
			removeNode(c)
			continue
		}
		if c.Type != html.ElementNode {
			continue
		}
		if dropTags[c.Data] {
			removeNode(c)
			continue
		}
		if isJunkElement(c, opts.StripClasses) {
			removeNode(c)
			continue
		}
		if !domx.IsVisible(c) {
			removeNode(c)
			continue
		}
		if opts.Prune && !keepTags[c.Data] {
			// Unknown element: keep its children (drop the wrapper) unless it
			// carries text of its own.
			if strings.TrimSpace(domx.TextOf(c)) == "" {
				unwrapNode(c)
			} else {
				removeNode(c)
			}
			continue
		}
		pruneTree(c, opts)
	}
}

// isJunkElement reports whether an element is site chrome.
func isJunkElement(n *html.Node, extraClasses []string) bool {
	id := domx.Attr(n, "id")
	classes := strings.Join(domx.ClassAttr(n), " ")
	hay := id + " " + classes
	if strings.TrimSpace(hay) == "" {
		return false
	}
	if junkClassRe.MatchString(hay) {
		return true
	}
	for _, frag := range extraClasses {
		if frag != "" && strings.Contains(hay, frag) {
			return true
		}
	}
	// role="navigation" and aria-hidden landmarks are chrome by definition.
	switch strings.ToLower(domx.Attr(n, "role")) {
	case "navigation", "banner", "search", "complementary", "contentinfo", "menubar", "toolbar":
		return true
	}
	if strings.EqualFold(domx.Attr(n, "aria-hidden"), "true") {
		return true
	}
	return false
}

// unwrapNode replaces a node with its children, preserving their order.
//
// The children must be explicitly detached first: x/net/html's InsertBefore
// panics if the node being inserted still has a parent, and RemoveChild only
// clears the parent of the node it removes, not of that node's children.
func unwrapNode(n *html.Node) {
	if n == nil || n.Parent == nil {
		return
	}
	parent := n.Parent
	var children []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		children = append(children, c)
	}
	for _, c := range children {
		c.Parent = nil
		c.PrevSibling = nil
		c.NextSibling = nil
		parent.InsertBefore(c, n)
	}
	parent.RemoveChild(n)
}

// childrenOf snapshots a node's direct children. Every mutating pass works
// from a snapshot: removing a node clears its sibling pointers, so iterating
// the live list while mutating silently skips nodes.
func childrenOf(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
}

// fixupTree rewrites URLs, resolves lazy images and strips presentational
// attributes.
func fixupTree(n *html.Node, opts sanitizeOptions) {
	if n.Type == html.ElementNode {
		fixElement(n, opts)
	}
	for _, c := range childrenOf(n) {
		fixupTree(c, opts)
	}
}

func fixElement(n *html.Node, opts sanitizeOptions) {
	switch n.Data {
	case "img":
		fixImage(n, opts)
	case "a":
		fixLink(n, opts)
	case "source":
		if srcset := domx.Attr(n, "srcset"); srcset != "" {
			if best := pickLargestFromSrcset(srcset); best != "" {
				setAttr(n, "src", urlx.Resolve(best, opts.BaseURL))
			}
		}
		for _, key := range lazyImageAttrs {
			if v := strings.TrimSpace(domx.Attr(n, key)); v != "" && !isPlaceholder(v) {
				setAttr(n, "src", urlx.Resolve(v, opts.BaseURL))
				break
			}
		}
	case "time":
		if dt := domx.Attr(n, "datetime"); dt != "" {
			if parsed := parseDate(dt); !parsed.IsZero() {
				setAttr(n, "datetime", parsed.UTC().Format("2006-01-02T15:04:05Z07:00"))
			}
		}
	}
	stripPresentational(n)
}

// fixImage resolves lazy-loaded sources to a real absolute URL.
func fixImage(n *html.Node, opts sanitizeOptions) {
	src := domx.Attr(n, "src")
	if isPlaceholder(src) {
		src = ""
	}
	// srcset wins when present, since it holds the highest-resolution option.
	if srcset := domx.Attr(n, "srcset"); srcset != "" {
		if best := pickLargestFromSrcset(srcset); best != "" {
			src = best
		}
	}
	for _, key := range lazyImageAttrs {
		if v := strings.TrimSpace(domx.Attr(n, key)); v != "" {
			if isPlaceholder(v) {
				continue
			}
			if key == "data-srcset" {
				if best := pickLargestFromSrcset(v); best != "" {
					src = best
					break
				}
				continue
			}
			src = v
			break
		}
	}
	if src == "" {
		// A data: URI placeholder with no real source anywhere: the image is
		// unrecoverable, so drop the element rather than ship a blank.
		removeNode(n)
		return
	}
	setAttr(n, "src", urlx.Resolve(src, opts.BaseURL))
	removeAttr(n, "srcset")
	for _, key := range lazyImageAttrs {
		removeAttr(n, key)
	}
	if alt := domx.Attr(n, "alt"); alt == "" {
		// Decorative images should not be announced; article images should
		// have had alt text, and this only fills the gap.
		setAttr(n, "alt", "")
	}
}

func isPlaceholder(src string) bool {
	s := strings.ToLower(strings.TrimSpace(src))
	return s == "" ||
		strings.HasPrefix(s, "data:image/svg") ||
		strings.HasPrefix(s, "data:") ||
		strings.Contains(s, "placeholder") ||
		strings.Contains(s, "blank.gif") ||
		strings.Contains(s, "spacer.gif") ||
		strings.Contains(s, "1x1") ||
		strings.Contains(s, "transparent")
}

// pickLargestFromSrcset returns the highest-width candidate from a srcset.
func pickLargestFromSrcset(srcset string) string {
	bestURL := ""
	bestWidth := -1.0
	for _, part := range strings.Split(srcset, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		url := fields[0]
		width := 0.0
		if len(fields) > 1 {
			desc := fields[1]
			if strings.HasSuffix(desc, "w") {
				width = parseFloatPrefix(strings.TrimSuffix(desc, "w"))
			} else if strings.HasSuffix(desc, "x") {
				width = parseFloatPrefix(strings.TrimSuffix(desc, "x")) * 1000
			}
		}
		if width > bestWidth {
			bestURL, bestWidth = url, width
		}
	}
	return bestURL
}

func parseFloatPrefix(s string) float64 {
	var f float64
	var seenDot bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' && !seenDot {
			seenDot = true
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		f = f*10 + float64(c-'0')
	}
	return f
}

func fixLink(n *html.Node, opts sanitizeOptions) {
	href := domx.Attr(n, "href")
	if href == "" {
		return
	}
	lower := strings.ToLower(strings.TrimSpace(href))
	if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "data:") {
		// Not navigable content: unwrap so the link text survives as plain text.
		unwrapNode(n)
		return
	}
	if strings.HasPrefix(lower, "#") {
		return
	}
	setAttr(n, "href", urlx.Resolve(cleanTracking(href), opts.BaseURL))
	// Outbound links open in a new context in most readers; make that explicit
	// and stop referrer leakage.
	if !domx.HasAttr(n, "rel") {
		setAttr(n, "rel", "noopener noreferrer")
	}
}

// cleanTracking removes analytics parameters from a URL.
func cleanTracking(href string) string {
	if href == "" {
		return href
	}
	// A fragment only addresses a position inside one page, which a feed
	// reader cannot honour, so it never survives into a feed item.
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	if !strings.Contains(href, "?") {
		return href
	}
	base, query, _ := strings.Cut(href, "?")
	parts := strings.Split(query, "&")
	kept := parts[:0]
	for _, p := range parts {
		if p == "" {
			continue
		}
		key := p
		if i := strings.IndexByte(p, '='); i >= 0 {
			key = p[:i]
		}
		if trackingParams[strings.ToLower(key)] {
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		return base
	}
	return base + "?" + strings.Join(kept, "&")
}

// stripPresentational removes attributes that only mattered for the original
// page's rendering, plus anything executable.
func stripPresentational(n *html.Node) {
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		switch {
		case key == "style" || key == "class" || key == "id":
			// Site-config selectors run before this point, so dropping class
			// and id here is safe and shrinks the payload noticeably.
			removeAttr(n, a.Key)
		case strings.HasPrefix(key, "on"):
			removeAttr(n, a.Key)
		case strings.HasPrefix(key, "data-"), key == "srcset", key == "sizes",
			key == "loading", key == "width", key == "height", key == "align",
			key == "border", key == "vspace", key == "hspace", key == "role",
			key == "aria-hidden", key == "target", key == "allowfullscreen",
			key == "frameborder", key == "fetchpriority", key == "decoding",
			key == "referrerpolicy", key == "usemap", key == "longdesc":
			removeAttr(n, a.Key)
		}
	}
}

// unwrapRedundant collapses <div><div>…</div></div> chains that add no value.
func unwrapRedundant(n *html.Node) {
	for _, c := range childrenOf(n) {
		if c.Type == html.ElementNode && (c.Data == "div" || c.Data == "span") {
			// Only unwrap when the wrapper holds exactly one element child and
			// no text of its own.
			elemChildren, text := 0, strings.TrimSpace(domx.TextOf(c))
			for _, g := range childrenOf(c) {
				if g.Type == html.ElementNode {
					elemChildren++
				}
			}
			if elemChildren == 1 && text == "" {
				unwrapNode(c)
				continue
			}
		}
		unwrapRedundant(c)
	}
}

// cleanupEmpty removes elements left with nothing inside them.
func cleanupEmpty(n *html.Node) {
	for _, c := range childrenOf(n) {
		cleanupEmpty(c)
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "div", "span", "p", "section", "ul", "ol", "figure", "figcaption":
			if c.FirstChild == nil {
				removeNode(c)
			}
		}
	}
}

func setAttr(n *html.Node, key, val string) {
	for i := range n.Attr {
		if strings.EqualFold(n.Attr[i].Key, key) {
			n.Attr[i].Key = key
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func removeAttr(n *html.Node, key string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			continue
		}
		out = append(out, a)
	}
	n.Attr = out
}
