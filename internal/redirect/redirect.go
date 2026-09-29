// Package redirect opens click-through links: the wrapper URLs that news
// aggregators put in their feeds so that a reader's click can be counted.
//
// Only wrappers that carry their target in their own query string live here,
// because those cost nothing to open: the publisher URL is read, not fetched, so
// the item can be published with a direct link. A wrapper that needs a request to
// be understood is deliberately not in this package — that request belongs to
// whoever can afford it, and only for the purpose of fetching the article body.
// Google News is the case in point; it is handled by the gnews package.
package redirect

import (
	"net/url"
	"strings"
)

// maxUnwrapDepth bounds the chain a link may be wrapped in. A wrapper pointing
// at another wrapper happens in the wild; a link that never settles does not,
// and must not spin.
const maxUnwrapDepth = 4

// wrapper describes one click-through form: the hosts that serve it, the path
// that identifies it, and the query parameters that carry the target.
type wrapper struct {
	host func(host string) bool
	path func(path string) bool
	// params are the query parameters that hold the wrapped URL, in the order
	// they are tried. A host that names the target differently in different
	// places needs more than one.
	params []string
}

// wrappers are tried in order. Each one has to be right about both the host and
// the path, because "q=" or "url=" is far too common a parameter name to match
// on its own.
var wrappers = []wrapper{
	{
		// Google's click-through is https://www.google.com/url?...&<param>=<target>
		// with rct, sa, ct, cd and usg around it. Google Alerts writes the
		// target under "url", and the plain search-result click-through writes
		// it under "q", so both are read rather than one guessed at. The path
		// is the identifying part: "google" as a label of the host is too broad
		// to be worth matching on, and a country domain is covered for free by
		// requiring the path to be the same.
		host:   hasLabel("google"),
		path:   exactPath("/url"),
		params: []string{"url", "q"},
	},
	{
		// Bing News, in the form
		// https://www.bing.com/news/apiclick.aspx?...&url=<target>.
		host:   hasLabel("bing"),
		path:   hasSuffix("/apiclick.aspx"),
		params: []string{"url"},
	},
}

// Unwrap returns the URL a click-through link points at, and reports whether the
// input was one. A link that is already direct, or one whose layers never reach
// a direct URL, comes back unchanged with false, so a caller may replace an
// item's link with the result unconditionally and know that a true means the
// reader will land on the publisher.
func Unwrap(rawURL string) (string, bool) {
	start := strings.TrimSpace(rawURL)
	cur := start
	for i := 0; i < maxUnwrapDepth; i++ {
		next, ok := unwrapOnce(cur)
		if !ok {
			break
		}
		cur = next
	}
	if cur == start {
		return start, false
	}
	// Layers that end in another wrapper have not been opened, only peeled, and
	// the last one may not even have a usable target left. The caller asked for a
	// destination, so report the failure rather than hand back a link that still
	// redirects.
	if isWrapped(cur) {
		return start, false
	}
	return cur, true
}

// isWrapped reports whether a URL is one of the wrapper forms, whether or not
// its target can be read.
func isWrapped(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	_, ok := match(u)
	return ok
}

// unwrapOnce reads the target out of one wrapper link.
func unwrapOnce(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	w, ok := match(u)
	if !ok {
		return "", false
	}
	q := u.Query()
	for _, param := range w.params {
		if target := strings.TrimSpace(q.Get(param)); plausibleTarget(target, raw) {
			return target, true
		}
	}
	return "", false
}

// match finds the wrapper a URL belongs to. Host and path must both agree,
// because a parameter name on its own is far too weak a signal.
func match(u *url.URL) (wrapper, bool) {
	host := u.Hostname()
	for _, w := range wrappers {
		if w.host(host) && w.path(u.Path) {
			return w, true
		}
	}
	return wrapper{}, false
}

// plausibleTarget reports whether a candidate wrapped URL is something a
// request could follow. A target that is not absolute, is not http, or is the
// wrapper itself would be a loop rather than a destination.
func plausibleTarget(target, wrapper string) bool {
	if target == "" {
		return false
	}
	t, err := url.Parse(target)
	if err != nil || t.Host == "" {
		return false
	}
	switch strings.ToLower(t.Scheme) {
	case "http", "https":
	default:
		return false
	}
	return target != wrapper
}

// hasLabel matches a host that carries the given name as one of its labels,
// which is how www.google.com, google.co.id and news.google.com all answer to
// one rule without a suffix list to maintain.
func hasLabel(name string) func(string) bool {
	return func(host string) bool {
		for _, label := range strings.Split(strings.ToLower(host), ".") {
			if label == name {
				return true
			}
		}
		return false
	}
}

// exactPath matches a path exactly.
func exactPath(path string) func(string) bool {
	return func(got string) bool { return got == path }
}

// hasSuffix matches the end of a path. The aggregators that use this form have
// moved it between prefixes more than once, so the tail is the part that has
// stayed stable.
func hasSuffix(suffix string) func(string) bool {
	return func(got string) bool { return strings.HasSuffix(got, suffix) }
}
