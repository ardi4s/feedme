// Package urlx holds the URL manipulation that feedme needs in more than one
// place: resolving relative references, normalising links for comparison,
// recognising tracking parameters, and deriving the repeating pattern out of a
// set of article URLs.
//
// It is kept separate from the HTML layer because every caller here is working
// with strings, not documents.
package urlx

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// TrackingParams are the query-string parameters that identify a marketing
// campaign rather than an article. They are stripped from feed item links
// because they change per click and would make every item look distinct.
var TrackingParams = map[string]bool{
	"utm_source": true, "utm_medium": true, "utm_campaign": true, "utm_term": true,
	"utm_content": true, "utm_id": true, "utm_name": true, "utm_reader": true,
	"utm_brand": true, "utm_social": true, "utm_social-type": true,
	"fbclid": true, "gclid": true, "dclid": true, "msclkid": true, "twclid": true,
	"igshid": true, "mc_cid": true, "mc_eid": true, "yclid": true, "_ga": true,
	"ref_src": true, "ref_url": true, "spm": true, "cmpid": true, "ncid": true,
	"smid": true, "sr_share": true, "__twitter_impression": true,
}

// Resolve turns a possibly relative href into an absolute URL. The order is
// (href, base): the reference being resolved comes first, the document it
// appears in second. A base of "" or a malformed reference yields the input
// unchanged rather than an error, because one bad link must not fail a page.
func Resolve(href, base string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	// Protocol-relative: the scheme comes from the base.
	if strings.HasPrefix(href, "//") {
		if strings.HasPrefix(base, "https:") {
			return "https:" + href
		}
		return "http:" + href
	}
	if strings.Contains(href, "://") {
		return href
	}
	if base == "" {
		return href
	}
	b, err := url.Parse(base)
	if err != nil {
		return href
	}
	r, err := url.Parse(href)
	if err != nil {
		return href
	}
	return b.ResolveReference(r).String()
}

// SetHost rewrites a URL's host while keeping its scheme, path, query, and
// fragment. It exists for sites whose own links point at a host that cannot be
// reached — a blocked CDN, or a sibling subdomain — while the same article is
// served from a reachable host.
//
// A malformed URL or an empty host is returned unchanged, so one bad link
// cannot fail a page.
func SetHost(raw, host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return raw
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return raw
	}
	u.Host = host
	return u.String()
}

// Site returns the registrable domain (eTLD+1) of a URL: "nasional.kontan.co.id"
// and "www.kontan.co.id" both give "kontan.co.id". It is the general notion of
// "same site", so a listing that links across its own subdomains still counts
// as one site. A host that is not a domain (an IP, localhost) is returned
// unchanged.
func Site(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return RegistrableDomain(u.Hostname())
}

// RegistrableDomain is Site for a bare hostname.
func RegistrableDomain(host string) string {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return ""
	}
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return d
	}
	return host
}

// PathKey identifies a URL by site and path, ignoring the query. Two links that
// differ only in a campaign or source parameter are the same page, which is how
// a homepage that links the same article from two sections is deduplicated.
func PathKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return RegistrableDomain(u.Hostname()) + strings.ToLower(u.EscapedPath())
}

// Normalize produces the comparison form of a URL: lower-cased scheme and host,
// no default port, no fragment, no tracking parameters, and query parameters
// sorted. Two links that differ only in tracking noise compare equal, so the
// same article is not stored twice.
func Normalize(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	// A default port is the same origin as none.
	if u.Scheme == "http" && u.Port() == "80" {
		u.Host = strings.TrimSuffix(u.Host, ":80")
	}
	if u.Scheme == "https" && u.Port() == "443" {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	}
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	u.RawFragment = ""
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			if TrackingParams[strings.ToLower(k)] {
				q.Del(k)
			}
		}
		// Re-encoding sorts the parameters, which is what makes the result
		// stable for comparison.
		u.RawQuery = q.Encode()
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

// Host returns the lowercase hostname of a URL, or of a bare authority such as
// "e.example:8080". It returns "" when no host can be read.
func Host(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "//") {
		// Tolerate a scheme-less authority like "e.example/path".
		if i := strings.Index(raw, "/"); i >= 0 {
			raw = raw[:i]
		} else if strings.Contains(raw, "?") || strings.Contains(raw, "#") {
			return ""
		}
		if u, err := url.Parse("//" + raw); err == nil && u.Hostname() != "" {
			return strings.ToLower(u.Hostname())
		}
		return strings.ToLower(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// SameHost reports whether two URLs are on the same hostname.
func SameHost(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Hostname(), ub.Hostname())
}

// KeepQueryParams filters a URL's query string down to the selected
// parameters.
//
// mode is "1" to keep everything, "0" to keep nothing, or a comma-separated
// list of parameter names. This mirrors the keep_qs_params option in
// FiveFilter, which exists because some sites identify an article purely by a
// query parameter and dropping it would break every link.
//
// The site's original parameter order is preserved. Re-encoding through
// url.Values.Encode() would sort them, which is fine for comparing two links
// but wrong for the link published in a feed: the item URL should look like
// the site's own URL.
func KeepQueryParams(raw, mode string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	mode = strings.TrimSpace(mode)

	// "1" keeps every parameter but still drops campaign noise and fragments.
	if mode == "" || mode == "1" {
		u.RawQuery = filterRawQuery(u.RawQuery, nil, true)
		u.Fragment = ""
		u.RawFragment = ""
		return u.String()
	}
	// "0" drops the whole query string.
	if mode == "0" {
		u.RawQuery = ""
		u.Fragment = ""
		u.RawFragment = ""
		return u.String()
	}
	keep := map[string]bool{}
	for _, k := range strings.Split(mode, ",") {
		if k = strings.TrimSpace(k); k != "" {
			keep[k] = true
		}
	}
	u.RawQuery = filterRawQuery(u.RawQuery, keep, false)
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// filterRawQuery rebuilds a raw query string, keeping the pairs whose name is
// accepted. A nil keep map with dropTracking set removes campaign parameters
// instead.
func filterRawQuery(raw string, keep map[string]bool, dropTracking bool) string {
	if raw == "" {
		return ""
	}
	kept := make([]string, 0, 8)
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		name := pair
		if i := strings.IndexByte(pair, '='); i >= 0 {
			name = pair[:i]
		}
		decoded, err := url.QueryUnescape(name)
		if err != nil {
			decoded = name
		}
		if keep != nil {
			if keep[decoded] || keep[name] {
				kept = append(kept, pair)
			}
			continue
		}
		if !TrackingParams[strings.ToLower(decoded)] {
			kept = append(kept, pair)
		}
	}
	return strings.Join(kept, "&")
}

// StripTracking removes campaign parameters and any fragment, leaving a link
// that stays the same no matter how the reader arrived at it.
func StripTracking(raw string) string {
	return Normalize(raw)
}

// HostAndPrefix returns the scheme://host portion of a URL, which is the scope
// a pattern applies to. Listing pages sometimes mix "/news/" and "/sports/"
// paths, and the pattern comparison keys on this shared prefix.
func HostAndPrefix(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// Pattern is the shape a URL shares with its siblings. Article URLs differ
// only in their variable parts, so replacing those with placeholders groups
// them together.
type Pattern struct {
	// Raw is the pattern string, for example
	// "kontan.co.id/news/{n}/{n}/{n}/{slug}".
	Raw string
	// Host is the hostname the pattern was built from.
	Host string
	// Score counts how many links on the page matched, and is the main
	// ranking signal.
	Count int
	// Slugs holds the variable tail of each matched URL, in page order. A
	// pattern with several distinct slugs is a list of articles; one with a
	// single slug is probably a category page.
	Slugs []string
	// Depth is the number of path segments, used as a tie-breaker in favour
	// of links that look more like articles than navigation.
	Depth int
}

// Placeholders used in pattern strings.
const (
	phNum  = "{n}"
	phSlug = "{slug}"
)

// PatternOf reduces a URL to its repeating shape.
//
// Numbers collapse to {n}, a long trailing word to {slug}, and long alphabetic
// path segments to {a}. The trailing slug is left intact because news sites
// conventionally end article paths in a headline, and keeping it separate
// lets a list page be told apart from a single article.
func PatternOf(raw string) (Pattern, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return Pattern{}, false
	}
	segments := splitPath(u.Path)
	if len(segments) == 0 {
		return Pattern{}, false
	}
	out := make([]string, 0, len(segments))
	last := len(segments) - 1
	for i, seg := range segments {
		out = append(out, patternSegment(seg, i == last, len(segments)))
	}
	// The pattern's identity is the site (registrable domain) plus the path
	// shape, so a listing whose links live on sibling subdomains still forms
	// one pattern instead of one per host.
	return Pattern{
		Raw:   RegistrableDomain(u.Hostname()) + "/" + strings.Join(out, "/"),
		Host:  strings.ToLower(u.Hostname()),
		Depth: len(segments),
	}, true
}

// patternSegment classifies one path segment. isLast tells it whether this is
// the trailing segment.
//
// The trailing segment is the varying one on any list page, so it becomes
// {slug} whenever it holds a letter. An earlier version required a minimum
// length, which was wrong: short headlines such as "/berita" or "/aaa" then
// each formed their own pattern, and a listing page never grouped at all.
func patternSegment(seg string, isLast bool, depth int) string {
	if seg == "" {
		return seg
	}
	// Pure numbers are IDs or dates either way, trailing or not.
	if isAllDigits(seg) {
		return phNum
	}
	// A date-shaped segment such as 20260926 or 2026-09-26.
	if looksLikeDate(seg) {
		return phNum
	}
	// A month name, as in /world/2026/sep/26/. Without this, every month forms
	// its own pattern and an archive page fragments into a dozen groups.
	if isMonthName(seg) {
		return phNum
	}
	// The trailing segment carries the headline, so it is the varying part
	// even when it looks like an opaque identifier. Article paths are
	// essentially never a single segment, and treating "/tentang" or
	// "/kontak" as a slug would merge every static page on a site into one
	// spurious group.
	if isLast && depth >= 2 && hasLetter(seg) {
		return phSlug
	}
	// Middle segments are left alone: a topic name there is meaningful, and
	// collapsing it would merge unrelated sections.
	return seg
}

// isMonthName recognises the English and Indonesian month names that appear in
// date-based URL paths.
func isMonthName(s string) bool { return months[strings.ToLower(s)] }

var months = map[string]bool{
	"jan": true, "feb": true, "mar": true, "apr": true, "may": true, "jun": true,
	"jul": true, "aug": true, "sep": true, "oct": true, "nov": true, "dec": true,
	"januari": true, "februari": true, "maret": true, "mei": true, "juni": true,
	"juli": true, "agustus": true, "september": true, "oktober": true,
	"november": true, "desember": true,
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasLetter(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// looksLikeDate recognises 20260926, 2026-09-26 and 26-09-2026 style segments,
// which a bare digit test would otherwise treat as opaque identifiers.
func looksLikeDate(s string) bool {
	digits := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits++
		} else if r != '-' && r != '_' && r != '/' {
			return false
		}
	}
	return digits == 8
}

// SlugOf returns the trailing path segment of a URL, which for a news article
// is the headline. It is used to tell a list of articles from a single one.
func SlugOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	segs := splitPath(u.Path)
	if len(segs) == 0 {
		return ""
	}
	return segs[len(segs)-1]
}

// GroupPatterns buckets URLs by their pattern, keeping page order. The
// returned slice is sorted by count, then by depth, then alphabetically, so the
// ranking is deterministic and the winner does not depend on map iteration.
func GroupPatterns(urls []string) []Pattern {
	order := []string{}
	groups := map[string]*Pattern{}
	index := map[string]int{}

	for _, raw := range urls {
		p, ok := PatternOf(raw)
		if !ok {
			continue
		}
		g, seen := groups[p.Raw]
		if !seen {
			g = &Pattern{
				Raw:   p.Raw,
				Host:  p.Host,
				Depth: p.Depth,
			}
			groups[p.Raw] = g
			index[p.Raw] = len(order)
			order = append(order, p.Raw)
		}
		g.Count++
		if slug := SlugOf(raw); slug != "" {
			g.Slugs = append(g.Slugs, slug)
		}
	}

	out := make([]Pattern, 0, len(order))
	for _, key := range order {
		out = append(out, *groups[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Depth != out[j].Depth {
			return out[i].Depth > out[j].Depth
		}
		return out[i].Raw < out[j].Raw
	})
	return out
}

// FormatCount renders a count compactly, for log lines.
func FormatCount(n int) string { return strconv.Itoa(n) }
