// Package filter narrows a set of feed items down to what the user asked for.
//
// The rules are the ones FiveFilter made familiar, and each one is a separate,
// independently testable predicate:
//
//   - URLContains / URLNotContains and URIMatches keep or drop items by link.
//   - TextContains / TextNotContains work on the item's title, summary and link.
//   - Domain keeps only one site's items, which is what makes a multi-site feed
//     usable.
//   - DateAfter / DateBefore cut the feed by publication date.
//
// Keeping and dropping are separate lists rather than one list with signs,
// because "show only" and "hide" are different intentions. When both are
// present the keep rules are applied first, so a user who writes both gets the
// narrower set.
package filter

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"feedme/internal/dates"
	"feedme/internal/listpage"
	"feedme/internal/urlx"
)

// Rules is a complete filter specification. Every field is optional; an empty
// Rules keeps everything.
type Rules struct {
	// URLContains keeps items whose link contains any of these substrings,
	// compared case-insensitively.
	URLContains []string
	// URLNotContains drops items whose link contains any of these.
	URLNotContains []string
	// URIMatches keeps items whose link matches any of these regular
	// expressions. A pattern that does not compile is returned as an error
	// rather than silently ignored.
	URIMatches []string

	// TextContains keeps items whose title, summary or link contains any of
	// these.
	TextContains []string
	// TextNotContains drops items matching any of these.
	TextNotContains []string

	// Domain keeps only items on these hosts, including subdomains.
	Domain []string

	// DateAfter drops items published before this date and DateBefore drops
	// items published after it. Both are compared by calendar day in UTC, so a
	// timezone cannot move an item across the boundary unnoticed.
	DateAfter  string
	DateBefore string

	// MaxItems caps the result. Zero means no cap.
	MaxItems int
}

// DropReason names the rule that removed an item.
type DropReason string

// The reasons a single item can be dropped. They are stable strings because
// they are shown to the user in the feed builder.
const (
	ReasonURLContains     DropReason = "url_contains"
	ReasonURLNotContains  DropReason = "url_not_contains"
	ReasonURIMatches      DropReason = "uri_matches"
	ReasonTextContains    DropReason = "text_contains"
	ReasonTextNotContains DropReason = "text_not_contains"
	ReasonDomain          DropReason = "domain"
	ReasonDate            DropReason = "date"
)

// Result is the outcome of filtering.
type Result struct {
	// Items are the survivors, in their original order.
	Items []listpage.Item
	// Dropped counts how many items each rule removed.
	Dropped map[DropReason]int
	// Examined is how many items were considered before MaxItems was applied.
	Examined int
}

// TotalDropped sums the per-rule counts.
func (r Result) TotalDropped() int {
	n := 0
	for _, c := range r.Dropped {
		n += c
	}
	return n
}

// Reasons lists the drop reasons in a stable order, for display.
func (r Result) Reasons() []DropReason {
	seen := make([]DropReason, 0, len(r.Dropped))
	for reason, n := range r.Dropped {
		if n > 0 {
			seen = append(seen, reason)
		}
	}
	sort.Slice(seen, func(i, j int) bool { return seen[i] < seen[j] })
	return seen
}

// Apply runs the rules over items.
//
// The drop counts are returned because an empty feed is the most common way
// this goes wrong: a user who sees "no items" with no explanation has no way to
// tell a typo in url_contains from a page that genuinely published nothing.
func Apply(items []listpage.Item, rules Rules) (Result, error) {
	res := Result{Items: []listpage.Item{}, Examined: len(items), Dropped: map[DropReason]int{}}

	include, err := compileAll(rules.URIMatches)
	if err != nil {
		return res, err
	}
	after, err := NormalizeBound(rules.DateAfter, "date_after")
	if err != nil {
		return res, err
	}
	before, err := NormalizeBound(rules.DateBefore, "date_before")
	if err != nil {
		return res, err
	}

	for _, it := range items {
		reason, keep := keepItem(it, rules, include, after, before)
		if !keep {
			res.Dropped[reason]++
			continue
		}
		res.Items = append(res.Items, it)
	}
	if rules.MaxItems > 0 && len(res.Items) > rules.MaxItems {
		res.Items = res.Items[:rules.MaxItems]
	}
	return res, nil
}

// keepItem reports whether an item survives, and the first reason it did not.
// The first reason is used rather than the most severe so the message matches
// the order the rules are written in, which is how the user is reading them.
func keepItem(it listpage.Item, rules Rules, include []*regexp.Regexp, after, before string) (DropReason, bool) {
	if len(rules.URLContains) > 0 && !containsAny(it.Link, rules.URLContains) {
		return ReasonURLContains, false
	}
	if containsAny(it.Link, rules.URLNotContains) {
		return ReasonURLNotContains, false
	}
	if len(include) > 0 && !matchesAny(it.Link, include) {
		return ReasonURIMatches, false
	}

	if text := searchableText(it); text != "" {
		if len(rules.TextContains) > 0 && !containsAny(text, rules.TextContains) {
			return ReasonTextContains, false
		}
		if containsAny(text, rules.TextNotContains) {
			return ReasonTextNotContains, false
		}
	}

	if len(rules.Domain) > 0 && !matchesDomain(it.Link, rules.Domain) {
		return ReasonDomain, false
	}

	if after != "" || before != "" {
		// An item with no date cannot satisfy a date window. Dropping it is the
		// honest behaviour: keeping it makes a date-filtered feed look like it
		// is working while containing undated items from any date.
		if it.Date.IsZero() {
			return ReasonDate, false
		}
		day := it.Date.UTC().Format("2006-01-02")
		if after != "" && day < after {
			return ReasonDate, false
		}
		if before != "" && day > before {
			return ReasonDate, false
		}
	}
	return "", true
}

// searchableText is what the text rules see.
//
// The link is included so a text rule can match a URL slug, which is how users
// filter on a word the summary happens not to use.
func searchableText(it listpage.Item) string {
	parts := make([]string, 0, 3)
	for _, s := range []string{it.Title, it.Summary, it.Link} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

func containsAny(s string, needles []string) bool {
	lower := strings.ToLower(s)
	for _, n := range needles {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			if strings.Contains(lower, n) {
				return true
			}
		}
	}
	return false
}

func matchesAny(s string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func compileAll(patterns []string) ([]*regexp.Regexp, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, &FieldError{Field: "uri_matches", Value: p, Err: err}
		}
		out = append(out, re)
	}
	return out, nil
}

// matchesDomain accepts the host itself and any subdomain, so a rule naming
// "kompas.com" also keeps surabaya.kompas.com.
func matchesDomain(link string, domains []string) bool {
	host := urlx.Host(link)
	if host == "" {
		return false
	}
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// dayFormat is the only date format the boundaries accept literally.
const dayFormat = "2006-01-02"

// NormalizeBound reads a YYYY-MM-DD bound and returns it in that form.
//
// A full timestamp is accepted and truncated to its day, because a date picker
// often submits one. A day-first numeric date whose first component is 12 or
// less is refused: 05-09-2026 could be May or September, and guessing wrong
// leaves the user with a feed that is silently missing months. Anything else is
// rejected with the field name, because a mistyped bound that was ignored would
// leave the feed unfiltered.
//
// It is exported so the URL parser can validate a user's parameters at the
// boundary, instead of the failure surfacing later as an empty feed.
func NormalizeBound(v, field string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if _, err := time.Parse(dayFormat, v); err == nil {
		return v, nil
	}
	// A full timestamp is fine. A day-first numeric date whose first component
	// is 12 or less is refused: 05-09-2026 could be May or September, and
	// guessing wrong leaves the user with a feed that is silently empty.
	if t, ok := dates.ParseUnambiguous(v); ok {
		return t.UTC().Format(dayFormat), nil
	}
	return "", &FieldError{Field: field, Value: v, Err: fmt.Errorf("want YYYY-MM-DD")}
}

// FieldError names the offending field so an HTTP handler can return a useful
// message instead of a bare "invalid parameter".
type FieldError struct {
	Field string
	Value string
	Err   error
}

func (e *FieldError) Error() string {
	return "filter: " + e.Field + ": " + quote(e.Value) + ": " + e.Err.Error()
}

func (e *FieldError) Unwrap() error { return e.Err }

// quote wraps a value in double quotes for an error message. The value came
// from a form field and may contain anything, so it is not escaped further.
func quote(s string) string { return `"` + s + `"` }

// Param is one query-string key/value pair.
type Param struct {
	Key   string
	Value string
}

// EncodeQuery builds a query string from ordered pairs, keeping the order the
// user sees in the feed URL.
//
// url.Values.Encode is not used because it sorts the keys, which turns a
// readable saved feed URL into an alphabetical jumble. Duplicate keys are
// preserved, which repeated parameters such as url_contains[] depend on.
func EncodeQuery(params []Param) string {
	var b strings.Builder
	for _, p := range params {
		if p.Key == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p.Key))
		if p.Value != "" {
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(p.Value))
		}
	}
	return b.String()
}

// HasParam reports whether a query string mentions the key at all, regardless
// of value. A handler uses it to tell "the user did not ask for a filter" from
// "the user asked for an empty filter", which are different requests.
func HasParam(rawQuery, key string) bool {
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		name := pair
		if i := strings.IndexByte(pair, '='); i >= 0 {
			name = pair[:i]
		}
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = decoded
		}
		if name == key {
			return true
		}
	}
	return false
}
