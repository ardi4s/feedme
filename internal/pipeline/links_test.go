package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A click-through link that names its destination in its own query string is
// opened before the feed is filtered, so a rule that names the publisher matches
// the article rather than the aggregator.
func TestClickThroughLinkIsOpenedBeforeFiltering(t *testing.T) {
	const rss = `<rss version="2.0"><channel><title>Bing News</title><link>https://news.example/</link>
	<item><title>Satu</title><link>http://www.bing.com/news/apiclick.aspx?ref=FexRss&amp;tid=1&amp;url=https%3a%2f%2fwww.liputan6.com%2ftekno%2fread%2f8301723</link>
	 <pubDate>Sat, 26 Sep 2026 09:00:00 +0000</pubDate><description>Satu</description></item>
	</channel></rss>`
	f := newFakeFetcher().add("https://bing.example/news", rss)
	p := newPipeline(f)
	spec := specFor(t,
		"feeds[]=https%3A%2F%2Fbing.example%2Fnews&filter=liputan6.com&max=1")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %d, want 1: a filter naming the publisher must see the publisher", len(res.Items))
	}
	if got := res.Items[0].Link; got != "https://www.liputan6.com/tekno/read/8301723" {
		t.Errorf("link = %q, want the publisher URL", got)
	}
	if got := res.Feed.Entries[0].Link; got != "https://www.liputan6.com/tekno/read/8301723" {
		t.Errorf("published link = %q, want the publisher URL", got)
	}
}

// Two feeds carrying the same story behind two different wrappers are the same
// item. Opening the links first is what makes the deduplication see that.
func TestClickThroughLinksAreOpenedBeforeDeduplication(t *testing.T) {
	const first = `<rss version="2.0"><channel><title>A</title><link>https://a.example/</link>
	<item><title>Shared</title><link>https://www.bing.com/news/apiclick.aspx?tid=1&amp;url=https%3a%2f%2fshared.example%2fx</link>
	 <pubDate>Sat, 26 Sep 2026 09:00:00 +0000</pubDate><description>A</description></item>
	</channel></rss>`
	const second = `<rss version="2.0"><channel><title>B</title><link>https://b.example/</link>
	<item><title>Shared</title><link>https://www.google.com/url?q=https%3A%2F%2Fshared.example%2Fx&amp;sa=t</link>
	 <pubDate>Sat, 26 Sep 2026 09:00:00 +0000</pubDate><description>B</description></item>
	</channel></rss>`
	f := newFakeFetcher().
		add("https://a.example/rss", first).
		add("https://b.example/rss", second)
	p := newPipeline(f)
	spec := specFor(t, "feeds[]=https%3A%2F%2Fa.example%2Frss&feeds[]=https%3A%2F%2Fb.example%2Frss")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %d, want 1: the same article behind two wrappers is one item", len(res.Items))
	}
}

// A Google News link cannot be opened without a request per item, and the link
// already works, so it stays in the feed. The publisher is fetched for the body
// only.
func TestGoogleNewsLinkStaysInTheFeedAndTheBodyComesFromThePublisher(t *testing.T) {
	const gnewsItem = "https://news.google.com/rss/articles/CBMigwFBVV95cUxNZUlxbVpreHZuNnl1UDVNeTJ1ZDF5UWZ2S2Z1Q1lUMjJiOXJWN0FxRE82VE0yYnBnZllVN0ZJS0VDamxJd2hLSmVIRnhLNzdqSFZpWXRvUk9qQ0pQSnZIdm9VTWx3Vi1lM3RZUFMxS1pZYXBLY0FhNVV3TDFhN0RFS0U4WQ?oc=5"
	const rss = `<rss version="2.0"><channel><title>Google News</title><link>https://news.google.com/</link>
	<item><title>Satu</title><link>` + gnewsItem + `</link>
	 <pubDate>Sat, 26 Sep 2026 09:00:00 +0000</pubDate><description>Teaser</description></item>
	</channel></rss>`

	f := newFakeFetcher().
		add("https://news.google.com/rss/search?q=ai", rss).
		add("https://pluang.com/news-feed/agen-ai", articleFor("agen-ai"))
	p := newPipeline(f)
	p.Links = fakeLinks{gnewsItem: "https://pluang.com/news-feed/agen-ai"}
	spec := specFor(t, "feeds[]=https%3A%2F%2Fnews.google.com%2Frss%2Fsearch%3Fq%3Dai&fulltext=1")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.FullTextFetched != 1 {
		t.Fatalf("FullTextFetched = %d, want 1", res.FullTextFetched)
	}
	if got := res.Feed.Entries[0].Link; got != gnewsItem {
		t.Errorf("published link = %q, want the Google News link the feed published", got)
	}
	if !strings.Contains(res.Feed.Entries[0].ContentHTML, "agen-ai") {
		t.Error("the body should have come from the publisher")
	}
	// The wrapper itself is never fetched: it is a shell, and fetching it would
	// only produce a body about being redirected.
	for _, asked := range f.asked() {
		if strings.HasPrefix(asked, "https://news.google.com/rss/articles/") {
			t.Errorf("the wrapper was fetched: %s", asked)
		}
	}
}

// A wrapper that is understood but cannot be opened is a failure the item can be
// counted as, not a page to fetch: following it returns the same shell.
func TestUnopenableClickThroughLinkIsCountedAsAFailure(t *testing.T) {
	const gnewsItem = "https://news.google.com/rss/articles/OPAQUE?oc=5"
	const rss = `<rss version="2.0"><channel><title>Google News</title><link>https://news.google.com/</link>
	<item><title>Satu</title><link>` + gnewsItem + `</link>
	 <pubDate>Sat, 26 Sep 2026 09:00:00 +0000</pubDate><description>Teaser</description></item>
	</channel></rss>`

	f := newFakeFetcher().add("https://news.google.com/rss", rss)
	p := newPipeline(f)
	p.Links = fakeLinks{gnewsItem: "boom"}
	spec := specFor(t, "feeds[]=https%3A%2F%2Fnews.google.com%2Frss&fulltext=1")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("an unopenable link must not fail the build: %v", err)
	}
	if res.FullTextFailed != 1 {
		t.Errorf("FullTextFailed = %d, want 1", res.FullTextFailed)
	}
	if len(res.Feed.Entries) != 1 || res.Feed.Entries[0].ContentHTML != "" {
		t.Error("the item should have survived with its teaser")
	}
	if len(f.asked()) != 1 {
		t.Errorf("the pipeline made %d requests, want only the feed itself: %v",
			len(f.asked()), f.asked())
	}
}

// Without a resolver, every link is used as published and nothing else changes.
func TestNoResolverMeansLinksAreUsedAsTheyAre(t *testing.T) {
	const rss = `<rss version="2.0"><channel><title>Google News</title><link>https://news.google.com/</link>
	<item><title>Satu</title><link>https://news.google.com/rss/articles/OPAQUE?oc=5</link>
	 <pubDate>Sat, 26 Sep 2026 09:00:00 +0000</pubDate><description>Teaser</description></item>
	</channel></rss>`
	f := newFakeFetcher().
		add("https://news.google.com/rss", rss).
		add("https://news.google.com/rss/articles/OPAQUE?oc=5", articleFor("shell"))
	p := newPipeline(f)
	spec := specFor(t, "feeds[]=https%3A%2F%2Fnews.google.com%2Frss&fulltext=1")

	res, err := p.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.FullTextFetched != 1 {
		t.Errorf("FullTextFetched = %d, want 1: without a resolver the link is fetched as it stands",
			res.FullTextFetched)
	}
}

// fakeLinks resolves a fixed set of links. A target of "boom" is a link the
// resolver recognised and could not open.
type fakeLinks map[string]string

func (f fakeLinks) Resolve(_ context.Context, link string) (string, bool, error) {
	target, ok := f[link]
	if !ok {
		return "", false, nil
	}
	if target == "boom" {
		return "", true, errors.New("gnews: the decoder endpoint refused")
	}
	return target, true, nil
}
