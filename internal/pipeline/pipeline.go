// Package pipeline runs one feed request end to end: fetch the page, decide
// which links are items, narrow them, optionally fetch each article body, and
// render the feed.
//
// The stages are separated from the HTTP handler on purpose. A handler has to
// answer "is this a client error or a site that is down", and it cannot do that
// while it is also parsing selectors and rendering XML. Here, every stage
// reports what it did, so the same information can serve the feed response, a
// diagnostic page, and a log line.
package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"feedme/internal/domx"
	"feedme/internal/extract"
	"feedme/internal/feed"
	"feedme/internal/feedread"
	"feedme/internal/feedurl"
	"feedme/internal/fetch"
	"feedme/internal/filter"
	"feedme/internal/listpage"
	"feedme/internal/redirect"
	"feedme/internal/render"
	"feedme/internal/urlx"
)

// Fetcher retrieves a page. It is an interface so a test can supply pages
// without a network, and so the headless renderer can be added later as another
// implementation rather than a branch inside the pipeline.
type Fetcher interface {
	// Get returns the response for a URL. forceFresh asks the fetcher to
	// contact the origin rather than answer from a cached copy.
	Get(ctx context.Context, rawURL string, forceFresh bool) (*fetch.Response, error)
}

// Pipeline builds feeds.
type Pipeline struct {
	// Fetch retrieves the listing page and, for full text, each article.
	Fetch Fetcher
	// Sites supplies per-host configuration. It may be nil.
	Sites SiteLookup
	// Now supplies the current time, so a build is reproducible in a test.
	Now func() time.Time
	// Parallel bounds how many article bodies are fetched at once.
	Parallel int
	// Renderer loads a page in a headless browser. It is used only when the
	// request asks for render_js and the plain HTML yields no item list, so the
	// common case never pays for a browser. Nil means rendering is unavailable,
	// and a render_js request then fails with a clear message.
	Renderer render.Renderer
	// Links opens the click-through links an aggregator puts in its feed, so
	// that the article body comes from the publisher. It is nil when no such
	// links are in play, which is the ordinary case: a link is then used exactly
	// as the feed published it.
	Links LinkResolver
}

// LinkResolver opens a click-through item link — the wrapper URL an aggregator
// publishes so that a reader's click can be counted.
//
// It answers three things separately because a caller treats them differently.
// A link it does not recognise is used as it stands. A link it recognises but
// cannot open is a failure worth counting, not a link worth following: following
// it would only arrive back at the same wrapper, and an item with a teaser is
// better than an item whose body is a redirect notice.
type LinkResolver interface {
	Resolve(ctx context.Context, link string) (target string, recognized bool, err error)
}

// SiteLookup resolves per-host configuration.
type SiteLookup interface {
	// SiteFor returns the site config for a host, or nil when there is none.
	SiteFor(host string) *SiteConfig
}

// SiteConfig is the part of a site config the pipeline uses. It mirrors
// config.Site, and the conversion lives in the web layer so this package does
// not depend on the config package's file layout.
type SiteConfig struct {
	Item    []string
	URL     []string
	Title   []string
	Date    []string
	Image   []string
	Summary []string
	Strip   []string
	// ForceHost and AllowCrossHost handle sites whose links point at a host
	// that cannot be fetched. See listpage.Options.
	ForceHost      string
	AllowCrossHost bool
}

// Result is everything one run produced.
type Result struct {
	// Feed is the rendered document.
	Feed feed.Feed
	// Format is how to serialise Feed.
	Format feed.Format
	// Items are the entries that survived filtering.
	Items []listpage.Item
	// Filtered reports how many items each rule removed.
	Filtered filter.Result
	// Detection records how the item list was found, when it was detected
	// rather than configured.
	Detection *listpage.Result
	// PageStatus is the listing page's HTTP status.
	PageStatus int
	// FromCache reports whether the listing page came from the cache.
	FromCache bool
	// FullTextFetched counts the article bodies retrieved, and FullTextFailed
	// counts the ones that could not be. A partial result is normal: some
	// articles are always missing, and the feed is better with the teasers
	// than with a failed request.
	FullTextFetched int
	FullTextFailed  int
	// FeedFetched and FeedFailed count the merge sources that were read and the
	// ones that were not. One dead feed among several must not fail the merge.
	FeedFetched int
	FeedFailed  int
	// FetchedAt is when the listing page was retrieved.
	FetchedAt time.Time
}

// Run executes the whole pipeline for one parsed request.
//
// A request may name a listing page, one or more existing feeds, or both. The
// sources are collected, merged, filtered, optionally upgraded to full text,
// and rendered.
func (p *Pipeline) Run(ctx context.Context, spec feedurl.Spec) (Result, error) {
	now := p.now()
	res := Result{Format: spec.Format}

	var (
		doc       *html.Node
		items     []listpage.Item
		detection *listpage.Result
		feedTitle string
		feedLink  string
	)

	if spec.URL != "" {
		// The listing is always fetched fresh. The rendered feed is what
		// protects the site from being hit on every reader poll, so by the time
		// a rebuild reaches the fetcher the cached page would only make the feed
		// stale without saving a request.
		page, err := p.Fetch.Get(p.requestContext(ctx, spec, ""), spec.URL, true)
		if err != nil {
			return res, err
		}
		res.PageStatus = page.Status
		res.FromCache = page.FromCache
		res.FetchedAt = page.FetchedAt
		if res.FetchedAt.IsZero() {
			res.FetchedAt = now
		}

		// A URL that is already a feed states its own items, and reading it as a
		// listing page would find none: there is no article element and no main,
		// because there is no HTML. The document is checked before any selector
		// work rather than after it failing, which is also what keeps this free —
		// the body has already been fetched, so a feed costs no request that a
		// listing page does not.
		if fd, ok := feedFromPage(page); ok {
			items = fd.Items
			feedTitle, feedLink = fd.Title, fd.Link
			res.Detection = &listpage.Result{
				Selector:   "feed",
				Confidence: "high",
				Reason:     "the document is a feed, so its entries are the items",
			}
		} else {
			listOpts := spec.List
			// A site config fills in whatever the request left unset, which is what
			// makes a known site work with the shortest possible feed URL.
			if site := p.siteFor(spec.URL); site != nil {
				listOpts = applySite(listOpts, site)
			}

			doc, items, detection, err = p.collectItems(page, listOpts)
			if err != nil && spec.RenderJS {
				// Hybrid by design: the plain fetch is tried first and the browser is
				// only paid for when it is actually needed. A JS-heavy listing returns
				// a 200 shell that yields no items, which is exactly this case.
				rendered, rerr := p.renderPage(ctx, spec.URL)
				if rerr != nil {
					return res, rerr
				}
				page = rendered
				res.PageStatus = rendered.Status
				res.FromCache = false
				res.FetchedAt = rendered.FetchedAt
				doc, items, detection, err = p.collectItems(page, listOpts)
			}
			if err != nil {
				return res, err
			}
			res.Detection = detection
		}
	}

	if len(spec.Feeds) > 0 {
		merged, source, fetched, failed, err := p.collectFeeds(ctx, spec)
		if err != nil {
			return res, err
		}
		res.FeedFetched = fetched
		res.FeedFailed = failed
		feedTitle, feedLink = source.Title, source.Link
		items = append(items, merged...)
	}

	// Click-through links that name their destination in their own query string
	// are opened here, before deduplication and filtering, so that both see the
	// link the reader will actually land on. The wrappers that need a request to
	// be understood are deliberately not opened: a Google News link already works
	// for a reader, and spending a request per item to replace it with an
	// equivalent link is not a trade worth making.
	items = openItemLinks(items)

	items = dedupeItems(items)

	filtered, err := filter.Apply(items, spec.Rules)
	if err != nil {
		return res, err
	}
	res.Filtered = filtered
	res.Items = filtered.Items
	if maxItems := spec.List.MaxItems; maxItems > 0 && len(res.Items) > maxItems {
		res.Items = res.Items[:maxItems]
	}

	feedSpec := spec.Feed
	feedSpec.Items = res.Items
	feedSpec.Now = now
	if feedSpec.Title == "" {
		if doc == nil && feedTitle != "" {
			feedSpec.Title = feedTitle
		} else {
			feedSpec.Title = titleFor(spec, doc)
		}
	}
	if feedSpec.Link == "" {
		feedSpec.Link = feedLink
	}
	if feedSpec.Description == "" {
		feedSpec.Description = defaultDescription(spec, len(res.Items))
	}

	if spec.FullText && len(res.Items) > 0 {
		content, fetched, failed := p.fetchBodies(ctx, res.Items, spec)
		// A body shared by many items is a template, not an article. Dropping
		// it here means those items keep their teaser instead of shipping the
		// same wrong block, and it is counted as a failure because no usable
		// article was obtained.
		content, dropped := dropBoilerplateBodies(content)
		res.FullTextFetched = fetched - dropped
		res.FullTextFailed = failed + dropped
		feedSpec.Content = content
	}

	res.Feed = feed.Build(feedSpec)
	return res, nil
}

// feedSource summarizes a merge source: the first one that was read names the
// output when the request did not.
type feedSource struct {
	Title string
	Link  string
}

// collectFeeds fetches and parses each existing feed.
//
// A feed that cannot be read is counted and skipped rather than propagated: a
// merge of several sources is still useful when one is down. Only when every
// feed fails is the first error returned, because then there is nothing to
// merge and the reader deserves to know why.
func (p *Pipeline) collectFeeds(ctx context.Context, spec feedurl.Spec) ([]listpage.Item, feedSource, int, int, error) {
	var (
		items    []listpage.Item
		source   feedSource
		fetched  int
		failed   int
		firstErr error
	)
	for _, feedURL := range spec.Feeds {
		// A merged feed is a live document, so it is read fresh too.
		r, err := p.Fetch.Get(p.requestContext(ctx, spec, ""), feedURL, true)
		if err == nil && (r.Status < 200 || r.Status >= 300) {
			err = &fetch.StatusError{URL: feedURL, Code: r.Status}
		}
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		parsed, perr := feedread.Parse(r.Body, r.URL)
		if perr != nil {
			failed++
			if firstErr == nil {
				firstErr = perr
			}
			continue
		}
		if fetched == 0 {
			source = feedSource{Title: parsed.Title, Link: parsed.Link}
		}
		fetched++
		items = append(items, parsed.Items...)
	}
	if fetched == 0 && firstErr != nil {
		return nil, source, 0, failed, firstErr
	}
	return items, source, fetched, failed, nil
}

// openItemLinks replaces the click-through links that carry their destination in
// their own query string, so the item is published with a direct link to the
// publisher.
//
// A link that cannot be opened is left exactly as the feed published it. The
// reader can follow a wrapper perfectly well, and a link that reaches the
// publisher by a different route is no worse than one that does not.
func openItemLinks(items []listpage.Item) []listpage.Item {
	for i := range items {
		if target, ok := redirect.Unwrap(items[i].Link); ok {
			items[i].Link = target
		}
	}
	return items
}

// dedupeItems drops repeated links, which is common when two feeds carry the
// same syndicated story. The first occurrence wins, so the source order in the
// URL decides which copy is kept.
func dedupeItems(items []listpage.Item) []listpage.Item {
	seen := make(map[string]bool, len(items))
	out := items[:0]
	for _, it := range items {
		key := normalize(it.Link)
		if key == "" {
			key = "\x00" + it.Title + "|" + it.Date.String()
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	return out
}

// feedFromPage reports whether a fetched body is a feed document rather than a
// page, and returns it when it is.
//
// A feed URL is a legitimate thing to paste into the url field, and it is
// wherever anyone gets one from: a reader's "copy feed address", a site footer,
// a search result. Requiring the reader to know that such a URL belongs in
// feeds[] instead is a distinction the code can make and the person typing cannot
// see. So the body is asked what it is, rather than the request being taken at
// its word.
//
// The test is the parse itself. feedread only accepts an RSS, Atom, RDF or JSON
// Feed root, so an HTML page fails here and goes on to the selector path exactly
// as before — no content type is consulted, because publishers get that wrong in
// both directions and the body is the authority.
//
// A feed with no entries reports false, so it falls through to the listing path
// and fails there as an empty page. It is a rare shape, and the alternative is a
// second error message for a case with an accurate one already.
func feedFromPage(page *fetch.Response) (*feedread.Document, bool) {
	doc, err := feedread.Parse(page.Body, page.URL)
	if err != nil || len(doc.Items) == 0 {
		return nil, false
	}
	return doc, true
}

// collectItems runs the configured selectors, or detects the list when the
// request names none. It returns the document as well, because the page's own
// title is the best answer to what the feed should be called.
func (p *Pipeline) collectItems(page *fetch.Response, opts listpage.Options) (*html.Node, []listpage.Item, *listpage.Result, error) {
	doc, err := parseHTML(page.Body)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("pipeline: parse %s: %w", page.URL, err)
	}
	if len(opts.Item) > 0 {
		items, err := listpage.FromSelectors(doc, opts)
		if err != nil {
			return nil, nil, nil, err
		}
		return doc, items, nil, nil
	}
	detection, err := listpage.Detect(doc, opts)
	if err != nil {
		return nil, nil, nil, err
	}
	// The detection's own selector is used rather than the nodes it found, so
	// the same code path runs for a detected list and a configured one. If the
	// selector turns out not to work, the error names it.
	// Detection normally returns the items directly, derived from the winning
	// URL pattern. The selector path is a fallback for the rare page where the
	// containers cannot be read straight from the anchors.
	if len(detection.Items) > 0 {
		return doc, detection.Items, &detection, nil
	}
	detected := opts
	detected.Item = []string{detection.Selector}
	items, err := listpage.FromSelectors(doc, detected)
	if err != nil {
		return doc, nil, &detection, fmt.Errorf("pipeline: detected selector %q did not work: %w", detection.Selector, err)
	}
	// A detected selector can be a bare tag, which matches every anchor on the
	// page. Keeping only items whose URL matches a detected pattern drops the
	// navigation without needing a site-specific selector.
	items = keepDetectedPatterns(items, detection.Candidates)
	return doc, items, &detection, nil
}

// keepDetectedPatterns removes items whose link is not one of the URL shapes
// detection found. It is the filter that lets a bare-tag detected selector stay
// useful on a page whose navigation shares the same markup.
func keepDetectedPatterns(items []listpage.Item, candidates []listpage.Candidate) []listpage.Item {
	if len(candidates) == 0 {
		return items
	}
	patterns := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		patterns[c.Pattern] = true
	}
	out := items[:0]
	for _, it := range items {
		if p, ok := urlx.PatternOf(it.Link); ok && patterns[p.Raw] {
			out = append(out, it)
		}
	}
	return out
}

// fetchBodies retrieves each article body, bounded by Parallel.
//
// A failure is counted and skipped rather than propagated: a feed missing one
// article is useful, whereas a request that fails because one of forty links
// returned 404 is not.
func (p *Pipeline) fetchBodies(ctx context.Context, items []listpage.Item, spec feedurl.Spec) (map[string]string, int, int) {
	limit := p.Parallel
	if limit <= 0 {
		limit = 4
	}
	work := make(chan listpage.Item)
	bodies := map[string]string{}
	var mu sync.Mutex
	var fetched, failed int

	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for it := range work {
			body, ok := p.fetchBody(ctx, it.Link, spec)
			mu.Lock()
			if ok {
				bodies[normalize(it.Link)] = body
				fetched++
			} else {
				failed++
			}
			mu.Unlock()
		}
	}
	for i := 0; i < limit; i++ {
		wg.Add(1)
		go worker()
	}
	for _, it := range items {
		select {
		case work <- it:
		case <-ctx.Done():
			// Stop feeding work but let the in-flight fetches finish, so their
			// results are not lost.
			close(work)
			wg.Wait()
			return bodies, fetched, failed + len(items) - fetched - failed
		}
	}
	close(work)
	wg.Wait()
	return bodies, fetched, failed
}

// renderPage loads a page in the browser and presents it as a fetched
// response, so every stage after this point treats it exactly like a plain
// fetch and no code has to branch on how the HTML arrived.
func (p *Pipeline) renderPage(ctx context.Context, target string) (*fetch.Response, error) {
	if p.Renderer == nil {
		return nil, render.ErrNotConfigured
	}
	body, err := p.Renderer.Render(ctx, target)
	if err != nil {
		return nil, err
	}
	return &fetch.Response{
		URL:         target,
		Status:      http.StatusOK,
		Body:        body,
		ContentType: "text/html; charset=utf-8",
		FetchedAt:   p.now(),
	}, nil
}

// boilerplateMinRepeats is how many items must share an identical body before
// it is treated as a template rather than as article text. Two is too eager
// (a short feed could legitimately repeat a one-line notice), so three is the
// floor; in addition, a body shared by every item is always dropped.
const boilerplateMinRepeats = 3

// dropBoilerplateBodies removes extracted bodies that cannot be article text.
//
// The signal is exact repetition: real articles are never byte-identical. A
// body used by three or more items — or by every item, when there are only two
// — is a template: an ad slot, a paywall notice, a market-data widget. Those
// items fall back to their teaser.
func dropBoilerplateBodies(content map[string]string) (map[string]string, int) {
	if len(content) < 2 {
		return content, 0
	}
	total := len(content)
	counts := make(map[[32]byte]int, total)
	for _, body := range content {
		counts[sha256.Sum256([]byte(body))]++
	}
	dropped := 0
	for link, body := range content {
		n := counts[sha256.Sum256([]byte(body))]
		if n >= boilerplateMinRepeats || n == total {
			delete(content, link)
			dropped++
		}
	}
	return content, dropped
}

// fetchBody retrieves and extracts one article. The body is always read fresh:
// a full-text feed is rebuilt rarely, and a cached body would mean publishing an
// article the site has since corrected or withdrawn.
//
// The link is opened first when it is a click-through, so the body comes from
// the publisher. The item's own link is not changed: the resolver's answer is
// good enough to fetch an article with and is not good enough to publish, and a
// Google News link is a working link that a reader can follow as it stands.
func (p *Pipeline) fetchBody(ctx context.Context, link string, spec feedurl.Spec) (string, bool) {
	target := link
	if p.Links != nil {
		resolved, recognized, err := p.Links.Resolve(ctx, link)
		if recognized {
			if err != nil {
				// The wrapper was understood but could not be opened. Fetching
				// it anyway would return the wrapper's own page, so the item
				// keeps its teaser and the count of failures stays honest.
				return "", false
			}
			target = resolved
		}
	}
	r, err := p.Fetch.Get(p.requestContext(ctx, spec, spec.URL), target, true)
	if err != nil || r.Status < 200 || r.Status >= 300 {
		return "", false
	}
	article, err := extractArticle(r.Body, target, spec.HTMLCleanup)
	if err != nil || strings.TrimSpace(article.ContentHTML) == "" {
		return "", false
	}
	return article.ContentHTML, true
}

// requestContext carries the per-request header overrides a feed URL may set.
//
// The user agent applies to every fetch this request makes. The referer is only
// defaulted to the listing page for article fetches: a site that checks the
// referer wants to see that the request came from its own index, and the
// listing fetch itself has no such page to point at. An explicit referer in the
// URL wins for both, because the user asked for it by name.
func (p *Pipeline) requestContext(ctx context.Context, spec feedurl.Spec, defaultReferer string) context.Context {
	ref := spec.Fetch.Referer
	if ref == "" {
		ref = defaultReferer
	}
	return fetch.WithRequestOptions(ctx, fetch.RequestOptions{
		UserAgent: spec.Fetch.UserAgent,
		Referer:   ref,
	})
}

// parseHTML turns a response body into a document.
func parseHTML(body []byte) (*html.Node, error) {
	return html.Parse(bytes.NewReader(body))
}

// extractArticle runs the article extractor over a body.
//
// htmlCleanup=false turns off the allow-list pruning pass. A user who has
// written their own remove and strip_css selectors wants the content as the
// page wrote it, not rewritten a second time.
func extractArticle(body []byte, link string, htmlCleanup bool) (*extract.Article, error) {
	return extract.FromHTML(body, extract.Options{
		PageURL:     link,
		Prune:       htmlCleanup,
		MaxElements: defaultMaxElements,
	})
}

// defaultMaxElements caps the DOM size for a page whose size is unknown, since
// the size limit belongs to the HTTP layer and not to the extractor.
const defaultMaxElements = 100000

// normalize is the key used to match a fetched body back to its item.
func normalize(link string) string { return urlx.Normalize(link) }

func (p *Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pipeline) siteFor(rawURL string) *SiteConfig {
	if p.Sites == nil {
		return nil
	}
	return p.Sites.SiteFor(urlx.Host(rawURL))
}

// applySite fills empty selector lists from a site config, without overriding
// anything the request set explicitly.
func applySite(opts listpage.Options, site *SiteConfig) listpage.Options {
	if len(opts.Item) == 0 {
		opts.Item = site.Item
	}
	if len(opts.URL) == 0 {
		opts.URL = site.URL
	}
	if len(opts.Title) == 0 {
		opts.Title = site.Title
	}
	if len(opts.Date) == 0 {
		opts.Date = site.Date
	}
	if len(opts.Image) == 0 {
		opts.Image = site.Image
	}
	if len(opts.Summary) == 0 {
		opts.Summary = site.Summary
	}
	// A strip list is additive: the request may want to remove more than the
	// site config already removes, never less.
	opts.Exclude = append(append([]string{}, site.Strip...), opts.Exclude...)
	if opts.ForceHost == "" {
		opts.ForceHost = site.ForceHost
	}
	if site.AllowCrossHost {
		opts.AllowCrossHost = true
	}
	return opts
}

// titleFor names the feed when the user did not.
//
// The page's own title is the best answer: a site that calls its listing page
// "Portal Uji" means for that to be the feed's name. The host is the fallback
// for a page without a usable title, and "Feed" is the last resort, since a feed
// called "Feed" is indistinguishable from every other one in a reader's list.
func titleFor(spec feedurl.Spec, doc *html.Node) string {
	if doc != nil {
		for _, sel := range []string{"title", "h1"} {
			for _, n := range domx.SelectAll(doc, []string{sel}) {
				if t := domx.NormSpace(domx.TextOf(n)); t != "" && len([]rune(t)) <= maxDefaultTitleRunes {
					return t
				}
			}
		}
	}
	if host := urlx.Host(spec.URL); host != "" {
		return host
	}
	return "Feed"
}

// maxDefaultTitleRunes keeps a page whose <title> is a whole sentence from
// becoming a feed title that wraps over three lines in a reader's list.
const maxDefaultTitleRunes = 80

func defaultDescription(spec feedurl.Spec, count int) string {
	host := urlx.Host(spec.URL)
	if host == "" {
		host = "the source"
	}
	switch count {
	case 0:
		return "Feed built from " + host + "."
	case 1:
		return "1 item from " + host + "."
	default:
		return fmt.Sprintf("%d items from %s.", count, host)
	}
}
