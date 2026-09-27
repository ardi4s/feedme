// Package feedurl parses the stateless feed URL into a plan for building one.
//
// Every parameter a feed needs travels in the query string, so a feed is fully
// described by its own address: bookmark it, share it, subscribe to it from
// another device, and nothing is lost. The cost of that choice is that this
// package is the program's trust boundary, and it validates every value it
// accepts before any of it reaches the fetcher.
//
// Parameter names follow the vocabulary FiveFilter established, so an existing
// feed URL from another tool is close to working here.
package feedurl

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"feedme/internal/feed"
	"feedme/internal/filter"
	"feedme/internal/listpage"
)

// Spec is a validated plan: everything needed to fetch a page, decide which
// links are items, narrow them, and render a feed.
type Spec struct {
	// URL is the listing page to read. Either URL or Feeds must be given; both
	// may be, in which case their items are merged.
	URL string

	// Feeds are existing RSS, Atom, or JSON Feed documents to read and merge
	// into the output. Their entries are filtered, capped, and (with fulltext)
	// upgraded exactly like scraped ones.
	Feeds []string

	// List controls item extraction.
	List listpage.Options

	// Rules narrow the extracted items.
	Rules filter.Rules

	// Feed describes the output document.
	Feed feed.Spec

	// Format is the requested output format.
	Format feed.Format

	// FullText requests the article body for each item instead of the
	// listing teaser.
	FullText bool

	// FullTextMax is how many items a full-text request may fetch article
	// bodies for. It is set even when FullText is off, but only consulted then,
	// because one fetch per item is what makes full text expensive.
	FullTextMax int

	// RenderJS allows the headless browser to be used for this request. It is
	// off unless asked for, because it costs far more than a plain fetch.
	RenderJS bool

	// Fresh bypasses the server's own feed cache so that every request rebuilds
	// from the source. It is part of the feed's identity, unlike the transient
	// refresh parameter, so a reader subscribed to a fresh=1 URL never sees a
	// cached document.
	Fresh bool

	// HTMLCleanup enables the aggressive sanitizer pass.
	HTMLCleanup bool

	// Fetch carries the per-request overrides a user may set.
	Fetch FetchOptions
}

// FetchOptions are the request-level settings a feed URL may override.
type FetchOptions struct {
	UserAgent string
	Referer   string
}

// Error names the parameter at fault, so a handler can point the user at the
// exact field instead of saying "invalid request".
type Error struct {
	Field   string
	Value   string
	Message string
}

func (e *Error) Error() string {
	if e.Value == "" {
		return "feedurl: " + e.Field + ": " + e.Message
	}
	return fmt.Sprintf("feedurl: %s: %q: %s", e.Field, e.Value, e.Message)
}

func errorf(field, value, format string, args ...any) *Error {
	return &Error{Field: field, Value: value, Message: fmt.Sprintf(format, args...)}
}

// FieldName lets a handler group this package's errors with the equivalent
// errors raised deeper in the pipeline.
func (e *Error) FieldName() string { return e.Field }

// Defaults applied when a parameter is absent.
const (
	// DefaultMaxItems is the feed length when max is not given. Fifty is
	// enough for a reader to have history without making the response enormous.
	DefaultMaxItems = 50

	// MaxAllowedItems caps what a caller may request, so one URL cannot ask
	// the server to crawl a thousand articles and hold every one in memory.
	MaxAllowedItems = 200
)

// Parse turns a query string into a validated Spec.
func Parse(values url.Values) (Spec, error) {
	s := Spec{
		Format:      feed.Format(strings.ToLower(strings.TrimSpace(values.Get("format")))),
		FullText:    truthy(values, "fulltext"),
		RenderJS:    truthy(values, "render_js"),
		Fresh:       truthy(values, "fresh"),
		HTMLCleanup: !explicitFalse(values, "html_cleanup"),
	}
	if s.Format == "" {
		s.Format = feed.FormatRSS
	}
	if !feed.Valid(s.Format) {
		return s, errorf("format", string(s.Format), "want one of rss, atom, json")
	}

	raw := strings.TrimSpace(values.Get("url"))
	if raw != "" {
		checked, err := checkURL("url", raw)
		if err != nil {
			return s, err
		}
		s.URL = checked
	}

	// Feeds to merge. "feed" and "feeds" are both accepted, bare or bracketed,
	// because every tool spells the repeated key differently.
	s.Feeds = append(list(values, "feeds"), list(values, "feed")...)
	for i, f := range s.Feeds {
		checked, err := checkURL("feeds", f)
		if err != nil {
			return s, err
		}
		s.Feeds[i] = checked
	}
	if s.URL == "" && len(s.Feeds) == 0 {
		return s, errorf("url", "", "required: the page to build a feed from, or one or more feeds")
	}

	forceHost, err := hostParam(values.Get("force_host"))
	if err != nil {
		return s, err
	}
	s.List = listpage.Options{
		BaseURL:         raw,
		Item:            list(values, "item"),
		URL:             list(values, "item_url"),
		Title:           list(values, "item_title"),
		Date:            list(values, "item_date"),
		Image:           list(values, "item_image"),
		Summary:         list(values, "item_desc"),
		Exclude:         list(values, "remove"),
		StripCSS:        list(values, "strip_css"),
		KeepQueryParams: strings.TrimSpace(values.Get("keep_qs_params")),
		AllowCrossHost:  truthy(values, "allow_cross_host"),
		ForceHost:       forceHost,
	}

	// id_or_class is the simple mode: a bare class name or id instead of a
	// full selector. It is what a user can read off a page without knowing
	// CSS, and it covers most listings.
	if idc := strings.TrimSpace(values.Get("id_or_class")); idc != "" {
		if len(s.List.Item) > 0 {
			return s, errorf("id_or_class", idc, "cannot be combined with item")
		}
		sel, err := simpleSelector(idc)
		if err != nil {
			return s, err
		}
		s.List.Item = []string{sel}
	}

	s.Rules = filter.Rules{
		URLContains:     list(values, "url_contains"),
		URLNotContains:  list(values, "strip_if_url"),
		URIMatches:      list(values, "uri_matches"),
		TextContains:    list(values, "text_contains"),
		TextNotContains: list(values, "strip_if_text"),
		Domain:          list(values, "domain"),
		DateAfter:       strings.TrimSpace(values.Get("date_after")),
		DateBefore:      strings.TrimSpace(values.Get("date_before")),
	}
	// A date bound is validated here rather than at render time, so a typo is
	// reported instead of producing a feed that is quietly unfiltered.
	if v, err := filter.NormalizeBound(s.Rules.DateAfter, "date_after"); err != nil {
		return s, wrapFilter(err)
	} else {
		s.Rules.DateAfter = v
	}
	if v, err := filter.NormalizeBound(s.Rules.DateBefore, "date_before"); err != nil {
		return s, wrapFilter(err)
	} else {
		s.Rules.DateBefore = v
	}

	maxItems, err := intParam(values, "max", DefaultMaxItems, 1, MaxAllowedItems)
	if err != nil {
		return s, err
	}
	// The cap is applied after filtering, so filtering first means a narrow
	// rule set returns its own full set rather than being cut to the cap while
	// the wider matches are still competing for the same slots.
	s.Rules.MaxItems = 0
	s.List.MaxItems = maxItems

	ttl, err := intParam(values, "ttl", 0, 0, 10080)
	if err != nil {
		return s, err
	}

	guidMode, err := guidParam(values.Get("guid"))
	if err != nil {
		return s, err
	}

	s.Fetch = FetchOptions{
		UserAgent: strings.TrimSpace(values.Get("user_agent")),
		Referer:   strings.TrimSpace(values.Get("referer")),
	}

	s.Feed = feed.Spec{
		Title:       strings.TrimSpace(values.Get("title")),
		Link:        raw,
		Description: strings.TrimSpace(values.Get("description")),
		Language:    strings.TrimSpace(values.Get("lang")),
		FullText:    s.FullText,
		GUID:        guidMode,
		TTL:         ttl,
	}
	if summary, err := intParam(values, "summary_chars", 0, 0, 20000); err != nil {
		return s, err
	} else {
		s.Feed.SummaryMaxRunes = summary
	}
	if full, err := intParam(values, "fulltext_chars", 0, 0, 2000000); err != nil {
		return s, err
	} else {
		s.Feed.FullTextMaxRunes = full
	}

	// Full text costs one fetch per item. Asking for it together with a large
	// max is the combination that turns a cheap request into a crawl, so it has
	// its own, tighter cap. fulltext_max raises that cap when a caller really
	// wants more article bodies, up to the same ceiling as max.
	fullTextMax, err := intParam(values, "fulltext_max", fullTextItemLimit, 1, MaxAllowedItems)
	if err != nil {
		return s, err
	}
	s.FullTextMax = fullTextMax
	if s.FullText && maxItems > fullTextMax {
		s.List.MaxItems = fullTextMax
	}

	if err := s.validate(); err != nil {
		return s, err
	}
	return s, nil
}

// fullTextItemLimit is the default bound on a full-text request. Twenty
// articles is already a substantial amount of fetching for a single feed load,
// so it is the default rather than the ceiling: fulltext_max raises it, up to
// MaxAllowedItems.
const fullTextItemLimit = 20

// wrapFilter turns a filter package error into one that carries a field name,
// so a handler has a single error shape to deal with.
func wrapFilter(err error) error {
	if fe, ok := err.(*filter.FieldError); ok {
		return &Error{Field: fe.Field, Value: fe.Value, Message: fe.Err.Error()}
	}
	return err
}

// validate checks the values that cannot be validated by parsing alone.
//
// Selectors are not checked for parseability here. Whether a selector compiles
// is left to domx, which skips entries it cannot use so that one bad selector
// in a site config costs that field rather than the whole feed. Blank values
// have already been discarded when the parameters were read.
func (s Spec) validate() error {
	if kq := strings.TrimSpace(s.List.KeepQueryParams); kq != "" {
		switch kq {
		case "0", "1":
			// The two documented modes.
		default:
			// Anything else is a comma-separated parameter name list, which
			// cannot be checked here. A "=" or a space inside it means the user
			// meant a query fragment rather than a list of names.
			if strings.ContainsAny(kq, "= ") {
				return errorf("keep_qs_params", kq, "want 0, 1, or a comma-separated list of parameter names")
			}
		}
	}
	return nil
}

// hostParam validates a bare hostname used by force_host: no scheme, no path,
// no spaces. An empty value means the parameter was not given.
func hostParam(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.ContainsAny(raw, "/\\ \t") || strings.Contains(raw, "://") {
		return "", errorf("force_host", raw, "want a bare host, e.g. www.example.com")
	}
	parsed, err := url.Parse("https://" + raw)
	if err != nil || parsed.Host != raw || parsed.Path != "" || parsed.Hostname() == "" {
		return "", errorf("force_host", raw, "not a valid host")
	}
	return raw, nil
}

// checkURL validates one source URL and returns it trimmed.
func checkURL(field, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errorf(field, "", "required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errorf(field, raw, "not a valid URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", errorf(field, raw, "want an http or https URL")
	}
	if parsed.Host == "" {
		return "", errorf(field, raw, "no host")
	}
	return raw, nil
}

// simpleSelector turns a bare class name or id into a CSS selector. A value that
// already looks like a selector is passed through, since users paste both.
func simpleSelector(v string) (string, error) {
	if v == "" {
		return "", errorf("id_or_class", v, "empty")
	}
	// Already a selector: leave it alone.
	if strings.ContainsAny(v, " .#[]>:>+~") {
		return v, nil
	}
	// A leading dot or hash is a class or id selector already.
	if strings.HasPrefix(v, ".") || strings.HasPrefix(v, "#") {
		return v, nil
	}
	// Digits cannot start a CSS class, so an all-digit value is an id.
	if isDigits(v) {
		return "#" + v, nil
	}
	return "." + v, nil
}

func isDigits(s string) bool {
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

// list reads a repeatable parameter, accepting both the bracketed and the bare
// spelling. Users copy feed URLs from several tools and not all of them append
// "[]" to repeated keys.
func list(values url.Values, key string) []string {
	out := append([]string{}, values[key]...)
	out = append(out, values[key+"[]"]...)

	// Indexed arrays: key[0], key[1], ... as the PHP-based tools (FiveFilter,
	// Feed Creator) write them. They are read too, so an existing feed URL works
	// without editing. Ordering by index keeps the result deterministic.
	type indexed struct {
		idx int
		val string
	}
	var extra []indexed
	prefix := key + "["
	for k, vals := range values {
		if !strings.HasPrefix(k, prefix) || !strings.HasSuffix(k, "]") {
			continue
		}
		mid := k[len(prefix) : len(k)-1]
		if mid == "" { // key[] is already handled above
			continue
		}
		n, err := strconv.Atoi(mid)
		if err != nil {
			continue
		}
		for _, v := range vals {
			extra = append(extra, indexed{idx: n, val: v})
		}
	}
	sort.SliceStable(extra, func(i, j int) bool { return extra[i].idx < extra[j].idx })
	for _, e := range extra {
		out = append(out, e.val)
	}

	cleaned := out[:0]
	for _, v := range out {
		if v = strings.TrimSpace(v); v != "" {
			cleaned = append(cleaned, v)
		}
	}
	return cleaned
}

// truthy reads a boolean parameter.
//
// A parameter that is present but has no value counts as false. Bare presence
// meaning true is a reasonable convention for a debug flag, but here the
// difference decides whether the server fetches twenty articles, and a
// hand-edited "?fulltext=" should not turn that on by accident.
func truthy(values url.Values, key string) bool {
	switch strings.ToLower(strings.TrimSpace(values.Get(key))) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// explicitFalse reports whether a parameter was present and set to false, which
// is different from being absent: html_cleanup defaults to on.
func explicitFalse(values url.Values, key string) bool {
	if _, present := values[key]; !present {
		return false
	}
	return !truthy(values, key)
}

func intParam(values url.Values, key string, def, min, max int) (int, error) {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def, errorf(key, raw, "want a whole number")
	}
	if n < min || n > max {
		return def, errorf(key, raw, "want between %d and %d", min, max)
	}
	return n, nil
}

func guidParam(raw string) (feed.GUIDMode, error) {
	switch mode := feed.GUIDMode(strings.ToLower(strings.TrimSpace(raw))); mode {
	case "":
		return feed.GUIDLink, nil
	case feed.GUIDLink, feed.GUIDStable, feed.GUIDTitle:
		return mode, nil
	default:
		return feed.GUIDLink, errorf("guid", string(mode), "want one of link, stable, title")
	}
}

// MaxAge is the cache lifetime this feed's responses should be stored for.
//
// It comes from the ttl parameter when given, and otherwise follows the rule a
// feed reader expects: a feed that updates often is worth refreshing, and one
// that does not can be cached for hours without anyone noticing.
func (s Spec) MaxAge(fallback time.Duration) time.Duration {
	if s.Feed.TTL > 0 {
		return time.Duration(s.Feed.TTL) * time.Minute
	}
	return fallback
}

// Query rebuilds the request parameters from a Spec.
//
// It exists so that a tool can show a spec as a URL, and so the check page can
// link to the feed the user just described. Only the fields that differ from
// the defaults are written, which keeps the resulting URL short and readable.
// A param that is dropped cannot be told apart from one that was never set,
// which is the same ambiguity the parser resolves by preferring explicit values.
func (s Spec) Query() url.Values {
	q := url.Values{}
	if s.URL != "" {
		q.Set("url", s.URL)
	}
	for _, f := range s.Feeds {
		q.Add("feeds[]", f)
	}
	if s.Format != "" && s.Format != feed.FormatRSS {
		q.Set("format", string(s.Format))
	}
	for _, set := range []struct {
		key  string
		vals []string
	}{
		{"item", s.List.Item},
		{"item_url", s.List.URL},
		{"item_title", s.List.Title},
		{"item_desc", s.List.Summary},
		{"item_date", s.List.Date},
		{"item_image", s.List.Image},
		{"remove", s.List.Exclude},
		{"strip_css", s.List.StripCSS},
		{"url_contains", s.Rules.URLContains},
		{"strip_if_url", s.Rules.URLNotContains},
		{"uri_matches", s.Rules.URIMatches},
		{"text_contains", s.Rules.TextContains},
		{"strip_if_text", s.Rules.TextNotContains},
		{"domain", s.Rules.Domain},
	} {
		for _, v := range set.vals {
			q.Add(set.key+"[]", v)
		}
	}
	if s.List.MaxItems > 0 && s.List.MaxItems != DefaultMaxItems {
		q.Set("max", strconv.Itoa(s.List.MaxItems))
	}
	if s.List.KeepQueryParams != "" {
		q.Set("keep_qs_params", s.List.KeepQueryParams)
	}
	if s.List.AllowCrossHost {
		q.Set("allow_cross_host", "1")
	}
	if s.List.ForceHost != "" {
		q.Set("force_host", s.List.ForceHost)
	}
	if !s.HTMLCleanup {
		q.Set("html_cleanup", "0")
	}
	if s.FullText {
		q.Set("fulltext", "1")
	}
	if s.FullText && s.FullTextMax > 0 && s.FullTextMax != fullTextItemLimit {
		q.Set("fulltext_max", strconv.Itoa(s.FullTextMax))
	}
	if s.RenderJS {
		q.Set("render_js", "1")
	}
	if s.Fresh {
		q.Set("fresh", "1")
	}
	if s.Fetch.UserAgent != "" {
		q.Set("user_agent", s.Fetch.UserAgent)
	}
	if s.Fetch.Referer != "" {
		q.Set("referer", s.Fetch.Referer)
	}
	if s.Feed.GUID != "" && s.Feed.GUID != feed.GUIDLink {
		q.Set("guid", string(s.Feed.GUID))
	}
	if s.Feed.TTL > 0 {
		q.Set("ttl", strconv.Itoa(s.Feed.TTL))
	}
	if s.Feed.Title != "" {
		q.Set("title", s.Feed.Title)
	}
	if s.Feed.Description != "" {
		q.Set("description", s.Feed.Description)
	}
	if s.Feed.Language != "" {
		q.Set("lang", s.Feed.Language)
	}
	if s.Feed.SummaryMaxRunes > 0 {
		q.Set("summary_chars", strconv.Itoa(s.Feed.SummaryMaxRunes))
	}
	if s.Feed.FullTextMaxRunes > 0 {
		q.Set("fulltext_chars", strconv.Itoa(s.Feed.FullTextMaxRunes))
	}
	if s.Rules.DateAfter != "" {
		q.Set("date_after", s.Rules.DateAfter)
	}
	if s.Rules.DateBefore != "" {
		q.Set("date_before", s.Rules.DateBefore)
	}
	return q
}
