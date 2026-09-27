package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"feedme/internal/feed"
	"feedme/internal/feedurl"
	"feedme/internal/fetch"
	"feedme/internal/filter"
	"feedme/internal/render"
)

// fakeFetcher serves canned pages keyed by URL, and records what was asked for.
type fakeFetcher struct {
	mu       sync.Mutex
	pages    map[string]*fetch.Response
	requests []string
	errs     map[string]error
	opts     map[string]fetch.RequestOptions
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		pages: map[string]*fetch.Response{},
		errs:  map[string]error{},
		opts:  map[string]fetch.RequestOptions{},
	}
}

func (f *fakeFetcher) add(url, body string) *fakeFetcher {
	f.pages[url] = &fetch.Response{
		URL:         url,
		Status:      200,
		Body:        []byte(body),
		ContentType: "text/html",
		FetchedAt:   time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC),
	}
	return f
}

func (f *fakeFetcher) Get(ctx context.Context, rawURL string, _ bool) (*fetch.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, rawURL)
	if f.opts == nil {
		f.opts = map[string]fetch.RequestOptions{}
	}
	f.opts[rawURL] = fetch.RequestOptionsFrom(ctx)
	f.mu.Unlock()
	if err, ok := f.errs[rawURL]; ok {
		return nil, err
	}
	if r, ok := f.pages[rawURL]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("fake: no page for %s", rawURL)
}

// optionsFor returns the header overrides the fetcher saw for a URL.
func (f *fakeFetcher) optionsFor(url string) fetch.RequestOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opts[url]
}

func (f *fakeFetcher) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.requests))
	copy(out, f.requests)
	return out
}

const listingHTML = `<html><head><title>Portal Berita</title></head><body>
	<nav class="navbar">
		<a href="/kategori/ekonomi">Ekonomi</a>
		<a href="/kategori/saham">Saham</a>
		<a href="/kategori/uang">Uang</a>
		<a href="/kategori/properti">Properti</a>
	</nav>
	<div class="grid">
		<article class="card">
			<a href="/2026/09/26/berita-satu"><h3>Berita Satu</h3></a>
			<time datetime="2026-09-26T08:00:00Z">26 September 2026</time>
			<p>Ringkasan berita satu.</p>
		</article>
		<article class="card">
			<a href="/2026/09/25/berita-dua"><h3>Berita Dua</h3></a>
			<time datetime="2026-09-25T08:00:00Z">25 September 2026</time>
			<p>Ringkasan berita dua.</p>
		</article>
		<article class="card">
			<a href="/2026/09/24/berita-tiga"><h3>Berita Tiga</h3></a>
			<time datetime="2026-09-24T08:00:00Z">24 September 2026</time>
			<p>Ringkasan berita tiga.</p>
		</article>
	</div>
	<footer><a href="/tentang">Tentang</a><a href="/privasi">Privasi</a>
		<a href="/kontak">Kontak</a><a href="/pedoman">Pedoman</a></footer>
</body></html>`

func specFor(t *testing.T, query string) feedurl.Spec {
	t.Helper()
	values, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := feedurl.Parse(values)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func newPipeline(f Fetcher) *Pipeline {
	return &Pipeline{
		Fetch: f,
		Now:   func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) },
	}
}

func TestRunDetectsListWithoutASelector(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&max=10")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("items = %d, want the three articles: %+v", len(res.Items), res.Items)
	}
	if res.Detection == nil {
		t.Fatal("expected the list to be detected, not configured")
	}
	if res.Detection.Confidence == "low" {
		t.Errorf("confidence = %q (%s)", res.Detection.Confidence, res.Detection.Reason)
	}
	// The navigation links must not be in the feed.
	for _, it := range res.Items {
		if strings.Contains(it.Link, "/kategori/") || strings.Contains(it.Link, "/tentang") {
			t.Errorf("navigation leaked into the feed: %+v", it)
		}
	}
	if res.PageStatus != 200 {
		t.Errorf("PageStatus = %d", res.PageStatus)
	}
}

func TestRunUsesConfiguredSelectors(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&id_or_class=card")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Detection != nil {
		t.Error("a configured selector should not report a detection")
	}
	if len(res.Items) != 3 {
		t.Errorf("items = %d", len(res.Items))
	}
}

func TestRunAppliesFilters(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&url_contains%5B%5D=berita-satu")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(res.Items))
	}
	if !strings.Contains(res.Items[0].Link, "berita-satu") {
		t.Errorf("wrong item kept: %+v", res.Items[0])
	}
	if res.Filtered.Dropped[filter.ReasonURLContains] == 0 {
		t.Errorf("drop not recorded: %v", res.Filtered.Dropped)
	}
}

func TestRunRendersFeed(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Feed.Entries) != 3 {
		t.Fatalf("entries = %d", len(res.Feed.Entries))
	}
	// Newest first.
	if !strings.Contains(res.Feed.Entries[0].Title, "Berita Satu") {
		t.Errorf("first entry = %q, want the newest", res.Feed.Entries[0].Title)
	}
	// The page's own title names the feed; a reader's list would be a column of
	// hostnames otherwise.
	if res.Feed.Title != "Portal Berita" {
		t.Errorf("Title = %q, want the page title", res.Feed.Title)
	}
	if res.Feed.Link != "https://e.example/" {
		t.Errorf("Link = %q, want the source page", res.Feed.Link)
	}
	out, err := feed.Bytes(res.Feed, res.Format)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Berita Satu") {
		t.Errorf("rendered feed is missing an item:\n%s", out)
	}
}

func TestRunWithFullText(t *testing.T) {
	f := newFakeFetcher()
	f.add("https://e.example/", listingHTML)
	// The article body must clear the extractor's 250 character minimum, or the
	// feed keeps the teaser instead of the article.
	f.add("https://e.example/2026/09/26/berita-satu", `<html><body><article>
		<h1>Berita Satu</h1>
		<p>Paragraf pertama artikel satu. Penulis menjelaskan secara rinci bahwa
		peristiwa ini terjadi pada pagi hari di Jakarta, dan bahwa sejumlah pihak
		yang disebut dalam laporan itu kemudian memberi tanggapan resmi.</p>
		<p>Paragraf kedua artikel satu, yang membahas dampak keputusan tersebut
		bagi para pembaca di seluruh negeri pada minggu pertama.</p>
		<p>Paragraf ketiga yang membuat isi artikel ini melampaui ambang panjang
		minimum yang ditetapkan oleh ekstraktor, sehingga isinya benar-benar
		dipakai dan bukan sekadar ringkasan.</p>
		</article></body></html>`)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1&max=1")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.FullTextFetched != 1 {
		t.Errorf("FullTextFetched = %d, want 1", res.FullTextFetched)
	}
	if res.Feed.Entries[0].ContentHTML == "" {
		t.Error("article body was not attached to the entry")
	}
}

// One unreachable article must not fail the whole request.
func TestFullTextFailureIsPartial(t *testing.T) {
	f := newFakeFetcher()
	f.add("https://e.example/", listingHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("a missing article should not fail the request: %v", err)
	}
	if res.FullTextFailed == 0 {
		t.Error("failures should be counted")
	}
	if len(res.Feed.Entries) != 3 {
		t.Errorf("entries = %d, want all three with teasers", len(res.Feed.Entries))
	}
	for _, e := range res.Feed.Entries {
		if e.ContentHTML != "" {
			t.Errorf("entry should have no body: %q", e.Title)
		}
	}
}

func TestSiteConfigFillsEmptySelectors(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	p := newPipeline(f)
	p.Sites = staticSites{"e.example": &SiteConfig{Item: []string{"article.card"}}}

	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F")
	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Detection != nil {
		t.Error("a site config should have supplied the selector, so no detection ran")
	}
	if len(res.Items) != 3 {
		t.Errorf("items = %d", len(res.Items))
	}
}

// A request's own selectors must win over the site config: the user is
// overriding, not asking for the default.
func TestRequestSelectorsOverrideSiteConfig(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	p := newPipeline(f)
	p.Sites = staticSites{"e.example": &SiteConfig{Item: []string{".does-not-exist"}}}

	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&id_or_class=card")
	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Errorf("items = %d, the request's selector should have been used", len(res.Items))
	}
}

type staticSites map[string]*SiteConfig

func (s staticSites) SiteFor(host string) *SiteConfig { return s[host] }

func TestRunPropagatesFetchError(t *testing.T) {
	f := newFakeFetcher()
	f.errs["https://e.example/"] = errors.New("dial failed")
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F")

	if _, err := p.Run(context.Background(), spec); err == nil {
		t.Error("a fetch failure should be returned to the caller")
	}
}

func TestRunPropagatesStatusError(t *testing.T) {
	f := newFakeFetcher()
	f.pages["https://e.example/"] = &fetch.Response{URL: "https://e.example/", Status: 405}
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F")

	res, err := p.Run(context.Background(), spec)
	// A 405 stored in the response means the fetcher did not treat it as an
	// error, so the pipeline must still report the status to the caller.
	if err == nil && res.PageStatus != 405 {
		t.Errorf("PageStatus = %d", res.PageStatus)
	}
}

func TestMaxItemsCapsAfterFiltering(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&max=2")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Errorf("items = %d, want the cap applied", len(res.Items))
	}
}

func TestEmptyPageIsReported(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", `<html><body><p>Tidak ada daftar.</p></body></html>`)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F")

	if _, err := p.Run(context.Background(), spec); err == nil {
		t.Error("a page with no list should be reported")
	}
}

func TestDefaultTitleFallsBackToHost(t *testing.T) {
	// No <title> and no <h1>: the host is the next best name.
	f := newFakeFetcher().add("https://e.example/news",
		`<html><body><div><article><a href="/2026/09/26/a">A</a></article></div></body></html>`)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2Fnews&item=article")
	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Feed.Title != "e.example" {
		t.Errorf("Title = %q, want the host", res.Feed.Title)
	}
	if res.Feed.Link != "https://e.example/news" {
		t.Errorf("Link = %q, want the page the feed was built from", res.Feed.Link)
	}
}

// The feed's own link should point at the page it describes, so that a reader
// can offer "visit site" without the user configuring it.
func TestFeedLinkComesFromTheSourcePage(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/news", listingHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2Fnews")
	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Feed.Link != "https://e.example/news" {
		t.Errorf("Link = %q", res.Feed.Link)
	}
}

// An explicit title always wins, even when the page has a perfectly good one.
func TestExplicitTitleWins(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/news", listingHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2Fnews&title=My+Feed")
	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Feed.Title != "My Feed" {
		t.Errorf("Title = %q", res.Feed.Title)
	}
}

// articleHTML is a body long enough to clear the extractor's minimum, so a test
// can exercise a full-text fetch without caring about extraction details.
const articleHTML = `<html><body><article>
	<h1>Berita Satu</h1>
	<p>Paragraf pertama artikel satu. Penulis menjelaskan secara rinci bahwa
	peristiwa ini terjadi pada pagi hari di Jakarta, dan bahwa sejumlah pihak
	yang disebut dalam laporan itu kemudian memberi tanggapan resmi.</p>
	<p>Paragraf kedua artikel satu, yang membahas dampak keputusan tersebut
	bagi para pembaca di seluruh negeri pada minggu pertama.</p>
	<p>Paragraf ketiga yang membuat isi artikel ini melampaui ambang panjang
	minimum yang ditetapkan oleh ekstraktor, sehingga isinya benar-benar
	dipakai dan bukan sekadar ringkasan.</p>
	</article></body></html>`

// A feed URL may name its own user agent, and it must reach both the listing
// fetch and every article fetch.
func TestUserAgentReachesEveryFetch(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	f.add("https://e.example/2026/09/26/berita-satu", articleHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1&max=1&user_agent=FeedReader%2F2.0")

	if _, err := p.Run(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://e.example/", "https://e.example/2026/09/26/berita-satu"} {
		if got := f.optionsFor(u).UserAgent; got != "FeedReader/2.0" {
			t.Errorf("user agent for %s = %q, want FeedReader/2.0", u, got)
		}
	}
}

// An article fetch is dressed as a reader that arrived from the listing page,
// which is what a referer check wants to see. The listing fetch itself has no
// such page above it, so it carries none.
func TestArticleFetchesUseTheListingPageAsReferer(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	f.add("https://e.example/2026/09/26/berita-satu", articleHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1&max=1")

	if _, err := p.Run(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if got := f.optionsFor("https://e.example/2026/09/26/berita-satu").Referer; got != "https://e.example/" {
		t.Errorf("article referer = %q, want the listing page", got)
	}
	if got := f.optionsFor("https://e.example/").Referer; got != "" {
		t.Errorf("listing referer = %q, want empty", got)
	}
}

// An explicit referer was asked for by name, so it wins for both fetches.
func TestExplicitRefererWins(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	f.add("https://e.example/2026/09/26/berita-satu", articleHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1&max=1&referer=https%3A%2F%2Fsocial.example%2F")

	if _, err := p.Run(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://e.example/", "https://e.example/2026/09/26/berita-satu"} {
		if got := f.optionsFor(u).Referer; got != "https://social.example/" {
			t.Errorf("referer for %s = %q, want https://social.example/", u, got)
		}
	}
}

// With nothing set, no override rides along, so the client's own default is the
// only user agent and no referer is invented.
func TestNoRequestOptionsWhenUnset(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F")

	if _, err := p.Run(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if got := f.optionsFor("https://e.example/"); got.UserAgent != "" || got.Referer != "" {
		t.Errorf("unexpected overrides: %+v", got)
	}
}

// shellHTML is what a client-rendered listing returns over plain HTTP: a 200
// with no list in it. Detection cannot find items, which is the signal to try
// the browser.
const shellHTML = `<html><head><title>App</title></head><body>
<div id="app">Loading…</div>
<script>fetch('/api/news')</script>
</body></html>`

// fakeRenderer stands in for the browser sidecar.
type fakeRenderer struct {
	body  string
	err   error
	calls int
}

func (r *fakeRenderer) Render(_ context.Context, _ string) ([]byte, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return []byte(r.body), nil
}

// When the plain fetch yields no list, render_js asks the browser and the
// rendered HTML is used as if it had been fetched.
func TestRenderJSFallsBackToTheBrowser(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", shellHTML)
	rend := &fakeRenderer{body: listingHTML}
	p := newPipeline(f)
	p.Renderer = rend
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&render_js=1&max=10")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) == 0 {
		t.Error("no items after rendering")
	}
	if rend.calls != 1 {
		t.Errorf("renderer calls = %d, want 1", rend.calls)
	}
	if res.PageStatus != 200 {
		t.Errorf("PageStatus = %d, want 200", res.PageStatus)
	}
}

// The browser is paid for only when needed: a listing that already parses is
// never rendered, even with render_js set.
func TestRendererNotUsedWhenPlainHTMLWorks(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	rend := &fakeRenderer{body: shellHTML}
	p := newPipeline(f)
	p.Renderer = rend
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&render_js=1&max=10")

	if _, err := p.Run(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if rend.calls != 0 {
		t.Errorf("renderer calls = %d, want 0", rend.calls)
	}
}

// Without render_js the browser is never consulted, even when the page has no
// list at all.
func TestRendererNotUsedWithoutRenderJS(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", shellHTML)
	rend := &fakeRenderer{body: listingHTML}
	p := newPipeline(f)
	p.Renderer = rend
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F")

	if _, err := p.Run(context.Background(), spec); err == nil {
		t.Fatal("the plain fetch should have failed to find items")
	}
	if rend.calls != 0 {
		t.Errorf("renderer calls = %d, want 0", rend.calls)
	}
}

// Asking for rendering on a server with no browser is a configuration problem,
// and it must say so rather than silently returning an empty feed.
func TestRenderJSWithoutBrowserFails(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", shellHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&render_js=1")

	_, err := p.Run(context.Background(), spec)
	if !errors.Is(err, render.ErrNotConfigured) {
		t.Fatalf("err = %v, want render.ErrNotConfigured", err)
	}
}

// A browser that fails is reported as a render failure, not mistaken for an
// empty page.
func TestRenderJSBrowserErrorIsReported(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", shellHTML)
	rend := &fakeRenderer{err: errors.New("browser is down")}
	p := newPipeline(f)
	p.Renderer = rend
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&render_js=1")

	_, err := p.Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "browser is down") {
		t.Fatalf("err = %v, want the browser's error", err)
	}
}

const rssA = `<rss version="2.0"><channel><title>Feed A</title><link>https://a.example/</link>
	<item><title>A1</title><link>https://a.example/1</link>
	 <pubDate>Sat, 26 Sep 2026 09:00:00 +0000</pubDate><description>Satu</description></item>
	<item><title>Shared A</title><link>https://shared.example/x</link>
	 <pubDate>Fri, 25 Sep 2026 09:00:00 +0000</pubDate><description>Shared A</description></item>
</channel></rss>`

const rssB = `<rss version="2.0"><channel><title>Feed B</title><link>https://b.example/</link>
	<item><title>B1</title><link>https://b.example/1</link>
	 <pubDate>Sun, 27 Sep 2026 09:00:00 +0000</pubDate><description>Satu B</description></item>
	<item><title>Shared B</title><link>https://shared.example/x</link>
	 <pubDate>Fri, 25 Sep 2026 09:00:00 +0000</pubDate><description>Shared B</description></item>
</channel></rss>`

// Two feeds merge into one, duplicates are dropped, and the result is ordered
// by date like every other feed.
func TestMergeFeeds(t *testing.T) {
	f := newFakeFetcher().add("https://a.example/rss", rssA).add("https://b.example/rss", rssB)
	p := newPipeline(f)
	spec := specFor(t, "feeds[]=https%3A%2F%2Fa.example%2Frss&feeds[]=https%3A%2F%2Fb.example%2Frss")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.FeedFetched != 2 {
		t.Errorf("FeedFetched = %d, want 2", res.FeedFetched)
	}
	if len(res.Items) != 3 {
		t.Errorf("items = %d, want 3: the shared link must be deduplicated", len(res.Items))
	}
	if res.Feed.Title != "Feed A" {
		t.Errorf("title = %q, want the first feed's title", res.Feed.Title)
	}
	if got := res.Feed.Entries[0].Title; got != "B1" {
		t.Errorf("first entry = %q, want B1 (newest)", got)
	}
	// The first occurrence wins, so the shared item keeps Feed A's description.
	for _, e := range res.Feed.Entries {
		if e.Title == "Shared A" {
			if !strings.Contains(e.Summary, "Shared A") {
				t.Errorf("shared summary = %q, want Feed A's copy", e.Summary)
			}
		}
	}
}

// Merging and full text compose: a feed's entries are fetched like scraped
// ones.
func TestMergeFeedsWithFullText(t *testing.T) {
	f := newFakeFetcher().
		add("https://a.example/rss", rssA).
		add("https://a.example/1", articleFor("a1")).
		add("https://shared.example/x", articleFor("shared"))
	p := newPipeline(f)
	spec := specFor(t, "feeds[]=https%3A%2F%2Fa.example%2Frss&fulltext=1")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.FullTextFetched != 2 {
		t.Errorf("FullTextFetched = %d, want 2", res.FullTextFetched)
	}
	found := false
	for _, e := range res.Feed.Entries {
		if e.Link == "https://a.example/1" && e.ContentHTML != "" {
			found = true
		}
	}
	if !found {
		t.Error("the merged entry has no fetched body")
	}
}

// One dead feed must not sink a merge.
func TestMergeSkipsADeadFeed(t *testing.T) {
	f := newFakeFetcher().add("https://a.example/rss", rssA)
	p := newPipeline(f)
	spec := specFor(t, "feeds[]=https%3A%2F%2Fa.example%2Frss&feeds[]=https%3A%2F%2Fb.example%2Frss")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("a dead feed should be skipped: %v", err)
	}
	if res.FeedFetched != 1 || res.FeedFailed != 1 {
		t.Errorf("fetched/failed = %d/%d, want 1/1", res.FeedFetched, res.FeedFailed)
	}
	if len(res.Items) == 0 {
		t.Error("the working feed's items are missing")
	}
}

// When every source fails there is nothing to merge, so the error surfaces.
func TestMergeAllFeedsFail(t *testing.T) {
	p := newPipeline(newFakeFetcher())
	spec := specFor(t, "feeds[]=https%3A%2F%2Fa.example%2Frss")
	if _, err := p.Run(context.Background(), spec); err == nil {
		t.Fatal("want an error when no feed can be read")
	}
}

// A listing page and existing feeds can be merged in one request.
func TestMergeListingAndFeeds(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML).add("https://a.example/rss", rssA)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&feeds[]=https%3A%2F%2Fa.example%2Frss&max=100")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 5 {
		t.Errorf("items = %d, want 3 from the listing + 2 from the feed", len(res.Items))
	}
}

// A kontan-like listing: the page is on www, but every news link points at a
// sibling subdomain.
const siblingListing = `<html><head><title>Kanal</title></head><body>
	<div class="grid">
		<a href="https://nasional.k.example/news/alpha"><h3>Alpha</h3></a>
		<a href="https://nasional.k.example/news/beta"><h3>Beta</h3></a>
		<a href="https://insight.k.example/news/gamma"><h3>Gamma</h3></a>
	</div></body></html>`

// force_host rewrites the sibling links onto the reachable host, so the items
// survive both the cross-host check and detection.
func TestForceHostKeepsSiblingLinks(t *testing.T) {
	f := newFakeFetcher().add("https://www.k.example/", siblingListing)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fwww.k.example%2F&force_host=www.k.example&url_contains=%2Fnews%2F&max=10")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(res.Items))
	}
	for _, e := range res.Feed.Entries {
		if !strings.HasPrefix(e.Link, "https://www.k.example/news/") {
			t.Errorf("entry link is not on the forced host: %q", e.Link)
		}
	}
}

// A site config can carry the rewrite, so the feed URL stays short.
func TestSiteConfigForceHost(t *testing.T) {
	f := newFakeFetcher().add("https://www.k.example/", siblingListing)
	p := newPipeline(f)
	p.Sites = staticSites{"www.k.example": &SiteConfig{ForceHost: "www.k.example"}}
	spec := specFor(t, "url=https%3A%2F%2Fwww.k.example%2F&url_contains=%2Fnews%2F&max=10")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Errorf("items = %d, want 3", len(res.Items))
	}
}

// Full text follows the rewritten host, which is the whole point for a blocked
// subdomain.
func TestForceHostFullText(t *testing.T) {
	f := newFakeFetcher().add("https://www.k.example/", siblingListing)
	for _, slug := range []string{"alpha", "beta", "gamma"} {
		f.add("https://www.k.example/news/"+slug, articleFor(slug))
	}
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fwww.k.example%2F&force_host=www.k.example&url_contains=%2Fnews%2F&fulltext=1&max=10")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.FullTextFetched != 3 {
		t.Errorf("FullTextFetched = %d, want 3", res.FullTextFetched)
	}
}

// articleFor builds a distinct, long-enough article so full text is kept.
func articleFor(marker string) string {
	return `<html><body><article><h1>Judul ` + marker + `</h1>` +
		`<p>Paragraf pertama ` + marker + ` yang menjelaskan secara rinci peristiwa pada pagi hari dan menyebut sejumlah pihak yang kemudian memberi tanggapan resmi kepada publik luas.</p>` +
		`<p>Paragraf kedua ` + marker + ` yang membahas dampak keputusan tersebut bagi para pembaca di seluruh negeri pada minggu pertama.</p>` +
		`<p>Paragraf ketiga yang membuat isi artikel ini melampaui ambang panjang minimum yang ditetapkan ekstraktor, sehingga isinya benar-benar dipakai.</p>` +
		`</article></body></html>`
}

// A site that serves the same block for every article (a widget, an ad, a
// paywall notice) must not have that block shipped as full text. The items fall
// back to their teaser and the run is reported as a failure.
func TestFullTextBoilerplateIsDropped(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	for _, u := range []string{
		"https://e.example/2026/09/26/berita-satu",
		"https://e.example/2026/09/25/berita-dua",
		"https://e.example/2026/09/24/berita-tiga",
	} {
		f.add(u, articleHTML) // identical for every article
	}
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1&max=10")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.FullTextFetched != 0 || res.FullTextFailed != 3 {
		t.Errorf("fetched/failed = %d/%d, want 0/3", res.FullTextFetched, res.FullTextFailed)
	}
	for _, e := range res.Feed.Entries {
		if e.ContentHTML != "" {
			t.Errorf("boilerplate body was kept for %q", e.Title)
		}
		if e.Summary == "" {
			t.Errorf("entry %q lost its teaser", e.Title)
		}
	}
}

// Distinct bodies are real article text and must be kept.
func TestFullTextDistinctBodiesAreKept(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	f.add("https://e.example/2026/09/26/berita-satu", articleFor("satu"))
	f.add("https://e.example/2026/09/25/berita-dua", articleFor("dua"))
	f.add("https://e.example/2026/09/24/berita-tiga", articleFor("tiga"))
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F&fulltext=1&max=10")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.FullTextFetched != 3 || res.FullTextFailed != 0 {
		t.Errorf("fetched/failed = %d/%d, want 3/0", res.FullTextFetched, res.FullTextFailed)
	}
	for _, e := range res.Feed.Entries {
		if e.ContentHTML == "" {
			t.Errorf("entry %q lost its full text", e.Title)
		}
	}
}

func TestDropBoilerplateBodies(t *testing.T) {
	// Three shared copies plus one distinct: only the shared ones go.
	content := map[string]string{"a": "same", "b": "same", "c": "same", "d": "other"}
	got, dropped := dropBoilerplateBodies(content)
	if dropped != 3 {
		t.Errorf("dropped = %d, want 3", dropped)
	}
	if _, ok := got["d"]; !ok {
		t.Error("the distinct body should have been kept")
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, ok := got[k]; ok {
			t.Errorf("%q should have been dropped", k)
		}
	}
}

// Two items that share a body are dropped too: two real articles are never
// byte-identical.
func TestDropBoilerplateBodiesPair(t *testing.T) {
	got, dropped := dropBoilerplateBodies(map[string]string{"a": "same", "b": "same"})
	if dropped != 2 || len(got) != 0 {
		t.Errorf("dropped = %d, remaining = %d, want 2/0", dropped, len(got))
	}
}

// A single item is never treated as boilerplate; there is nothing to compare.
func TestDropBoilerplateBodiesSingle(t *testing.T) {
	got, dropped := dropBoilerplateBodies(map[string]string{"a": "same"})
	if dropped != 0 || len(got) != 1 {
		t.Errorf("dropped = %d, remaining = %d, want 0/1", dropped, len(got))
	}
}
