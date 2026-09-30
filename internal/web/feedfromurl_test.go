package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const feedDoc = `<?xml version="1.0" encoding="UTF-8"?>
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

// A feed address in the url parameter used to fail with 500 and
// "listpage: no items found", because there is no HTML to find items in. It is
// the field a reader copies an address into, so it has to work.
func TestExtractServesAFeedGivenAsURL(t *testing.T) {
	f := newStub().add("https://news.example/rss", feedDoc)
	s := newTestServer(f)

	rec := do(t, s, http.MethodGet,
		"/extract?url="+url.QueryEscape("https://news.example/rss"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{"<rss", "Berita Satu", "Berita Dua", "Artikel Populer"} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q", want)
		}
	}
	if got := rec.Header().Get("X-Feedme-Items"); got != "2" {
		t.Errorf("X-Feedme-Items = %q, want 2", got)
	}
	if got := rec.Header().Get("X-Feedme-Detection"); got != "feed" {
		t.Errorf("X-Feedme-Detection = %q, want %q", got, "feed")
	}
	if got := rec.Header().Get("X-Feedme-Confidence"); got != "high" {
		t.Errorf("X-Feedme-Confidence = %q, want high", got)
	}
}

// The two parameters name the same document, so they have to answer the same.
func TestExtractURLAndFeedsGiveTheSameFeed(t *testing.T) {
	feed := "https://news.example/rss"

	byURL := newTestServer(newStub().add(feed, feedDoc))
	recURL := do(t, byURL, http.MethodGet, "/extract?url="+url.QueryEscape(feed))

	byFeeds := newTestServer(newStub().add(feed, feedDoc))
	recFeeds := do(t, byFeeds, http.MethodGet,
		"/extract?feeds%5B%5D="+url.QueryEscape(feed))

	if recURL.Code != http.StatusOK || recFeeds.Code != http.StatusOK {
		t.Fatalf("statuses = %d and %d, want both 200", recURL.Code, recFeeds.Code)
	}
	if got, want := recURL.Header().Get("X-Feedme-Items"), recFeeds.Header().Get("X-Feedme-Items"); got != want {
		t.Errorf("items = %q for url and %q for feeds", got, want)
	}
	for _, want := range []string{"Berita Satu", "Berita Dua"} {
		if !strings.Contains(recURL.Body.String(), want) {
			t.Errorf("url response is missing %q", want)
		}
		if !strings.Contains(recFeeds.Body.String(), want) {
			t.Errorf("feeds response is missing %q", want)
		}
	}
}

// A listing page must still be detected. The feed check runs first, so this is
// the case that could have broken.
func TestExtractStillServesAListingPage(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := newTestServer(f)

	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("X-Feedme-Items"); got != "3" {
		t.Errorf("X-Feedme-Items = %q, want 3", got)
	}
	if got := rec.Header().Get("X-Feedme-Detection"); got == "feed" {
		t.Error("a listing page was reported as a feed")
	}
}
