package pipeline

import (
	"context"
	"net/url"
	"testing"
)

const rssFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
  <title>Artikel Populer</title>
  <link>https://news.example/</link>
  <item>
    <title>Berita Satu</title>
    <link>https://news.example/berita-satu</link>
    <pubDate>Sat, 26 Sep 2026 08:00:00 GMT</pubDate>
    <description>Ringkasan berita satu.</description>
  </item>
  <item>
    <title>Berita Dua</title>
    <link>https://news.example/berita-dua</link>
    <pubDate>Fri, 25 Sep 2026 08:00:00 GMT</pubDate>
    <description>Ringkasan berita dua.</description>
  </item>
</channel></rss>`

// A feed URL pasted into the url field has to work, because that is where a
// reader's "copy feed address" ends up.
func TestRunReadsAFeedGivenAsURL(t *testing.T) {
	f := newFakeFetcher().add("https://news.example/rss", rssFeed)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fnews.example%2Frss")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("items = %d, want the feed's two entries: %+v", len(res.Items), res.Items)
	}
	if got := res.Items[0].Title; got != "Berita Satu" {
		t.Errorf("first title = %q, want %q", got, "Berita Satu")
	}
	if got := res.Items[0].Link; got != "https://news.example/berita-satu" {
		t.Errorf("first link = %q, want the publisher's own URL", got)
	}
}

// The feed names the output, which beats falling back to the hostname.
func TestRunTakesTheTitleFromTheFeed(t *testing.T) {
	f := newFakeFetcher().add("https://news.example/rss", rssFeed)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fnews.example%2Frss")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Feed.Title; got != "Artikel Populer" {
		t.Errorf("title = %q, want the feed's own %q", got, "Artikel Populer")
	}
	// The link is the requested URL, not the feed's own, because feedurl.Parse
	// presets it from the url parameter for every request. That is the existing
	// behaviour for a listing page too, and changing it here would be a change to
	// the request parser rather than to this branch.
	if got := res.Feed.Link; got != "https://news.example/rss" {
		t.Errorf("link = %q, want the requested URL", got)
	}
}

// The detection is reported rather than left empty, so a reader of the headers
// and the preview page can see that no guessing happened.
func TestRunReportsAFeedAsTheDetection(t *testing.T) {
	f := newFakeFetcher().add("https://news.example/rss", rssFeed)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fnews.example%2Frss")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Detection == nil {
		t.Fatal("Detection is nil, want the feed reported as how the items were found")
	}
	if res.Detection.Selector != "feed" {
		t.Errorf("selector = %q, want %q", res.Detection.Selector, "feed")
	}
	if res.Detection.Confidence != "high" {
		t.Errorf("confidence = %q, want %q: a feed states its items", res.Detection.Confidence, "high")
	}
}

// A feed costs no request beyond the one the listing page would have made.
func TestRunReadsAFeedWithoutASecondRequest(t *testing.T) {
	f := newFakeFetcher().add("https://news.example/rss", rssFeed)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fnews.example%2Frss")

	if _, err := p.Run(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if got := len(f.asked()); got != 1 {
		t.Errorf("requests = %d, want 1: the body was already fetched", got)
	}
}

// An HTML page must still go through detection. This is the case the feed check
// could break, so it is pinned rather than assumed.
func TestRunStillDetectsAListingPage(t *testing.T) {
	f := newFakeFetcher().add("https://e.example/", listingHTML)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("items = %d, want the three articles: %+v", len(res.Items), res.Items)
	}
	if res.Detection == nil || res.Detection.Selector == "feed" {
		t.Errorf("detection = %+v, want the selector the page was detected with", res.Detection)
	}
	if got := res.Feed.Title; got != "Portal Berita" {
		t.Errorf("title = %q, want the page's own <title>", got)
	}
}

// An HTML page that happens to mention a feed URL in its markup must not be
// mistaken for one: the root element decides, not the text.
func TestRunDoesNotMistakeAnHTMLPageForAFeed(t *testing.T) {
	page := `<html><head><title>Beranda</title></head><body>
		<p>Langganan: https://news.example/rss</p>
		<article class="card"><a href="/2026/09/26/satu"><h3>Satu</h3></a></article>
		<article class="card"><a href="/2026/09/25/dua"><h3>Dua</h3></a></article>
		<article class="card"><a href="/2026/09/24/tiga"><h3>Tiga</h3></a></article>
	</body></html>`
	f := newFakeFetcher().add("https://e.example/", page)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fe.example%2F")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("items = %d, want the three articles: %+v", len(res.Items), res.Items)
	}
	if res.Detection != nil && res.Detection.Selector == "feed" {
		t.Error("an HTML page was reported as a feed")
	}
}

// A feed with no entries has nothing to publish, and reporting it as one would
// publish an empty feed silently. It falls through to the listing path, which
// fails with the error an empty page has always given.
func TestRunDoesNotAcceptAnEmptyFeed(t *testing.T) {
	empty := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
  <title>Kosong</title>
  <link>https://news.example/</link>
</channel></rss>`
	f := newFakeFetcher().add("https://news.example/rss", empty)
	p := newPipeline(f)
	spec := specFor(t, "url=https%3A%2F%2Fnews.example%2Frss")

	res, err := p.Run(context.Background(), spec)
	if err == nil {
		t.Fatalf("no error, want the empty document refused: %d items", len(res.Items))
	}
}

// An Atom feed and a JSON Feed are read the same way, since the branch is on
// what feedread accepts rather than on RSS.
func TestRunReadsAtomAndJSONFeedsGivenAsURL(t *testing.T) {
	atom := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Feed</title>
  <link href="https://a.example/"/>
  <entry>
    <title>Entri Satu</title>
    <link href="https://a.example/satu"/>
    <updated>2026-09-26T08:00:00Z</updated>
  </entry>
</feed>`
	jsonFeed := `{"version":"https://jsonfeed.org/version/1.1","title":"JSON Feed",
	  "home_page_url":"https://j.example/",
	  "items":[{"title":"Item Satu","url":"https://j.example/satu","id":"1"}]}`

	for _, tc := range []struct{ name, url, body string }{
		{"atom", "https://a.example/atom", atom},
		{"json", "https://j.example/feed.json", jsonFeed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFetcher().add(tc.url, tc.body)
			p := newPipeline(f)
			spec := specFor(t, "url="+url.QueryEscape(tc.url))

			res, err := p.Run(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Items) != 1 {
				t.Fatalf("items = %d, want 1: %+v", len(res.Items), res.Items)
			}
		})
	}
}

// The two ways of naming a feed have to agree, or the same URL pasted into
// either field would give two different feeds.
func TestRunURLAndFeedsAgreeOnTheSameDocument(t *testing.T) {
	f := newFakeFetcher().
		add("https://news.example/rss", rssFeed).
		add("https://news.example/rss", rssFeed)
	p := newPipeline(f)

	viaURL := specFor(t, "url=https%3A%2F%2Fnews.example%2Frss")
	viaFeeds := specFor(t, "feeds%5B%5D=https%3A%2F%2Fnews.example%2Frss")

	fromURL, err := p.Run(context.Background(), viaURL)
	if err != nil {
		t.Fatal(err)
	}
	fromFeeds, err := p.Run(context.Background(), viaFeeds)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromURL.Items) != len(fromFeeds.Items) {
		t.Fatalf("url gave %d items, feeds gave %d", len(fromURL.Items), len(fromFeeds.Items))
	}
	for i := range fromURL.Items {
		if fromURL.Items[i].Title != fromFeeds.Items[i].Title {
			t.Errorf("item %d: url gave %q, feeds gave %q",
				i, fromURL.Items[i].Title, fromFeeds.Items[i].Title)
		}
	}
	if fromURL.Feed.Title != fromFeeds.Feed.Title {
		t.Errorf("title: url gave %q, feeds gave %q", fromURL.Feed.Title, fromFeeds.Feed.Title)
	}
}
