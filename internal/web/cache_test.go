package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"feedme/internal/fetch"
	"feedme/internal/pipeline"
)

// memCache is an in-memory FeedCache that counts its calls, so a test can tell a
// cache hit from a rebuild.
type memCache struct {
	mu      sync.Mutex
	entries map[string]*CachedFeed
	items   map[string][]CachedItem
	gets    int
	puts    int
	// failPut makes storage fail, to prove the reader still gets their feed.
	failPut bool
	// lifetime records what the caller asked for, so the ttl wiring is tested.
	lifetime time.Duration
}

func newMemCache() *memCache {
	return &memCache{entries: map[string]*CachedFeed{}, items: map[string][]CachedItem{}}
}

func (m *memCache) Get(_ context.Context, key string) (*CachedFeed, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	c, ok := m.entries[key]
	return c, ok
}

func (m *memCache) Put(_ context.Context, key string, f *CachedFeed, items []CachedItem, lifetime time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts++
	m.lifetime = lifetime
	if m.failPut {
		return errors.New("storage is down")
	}
	m.entries[key] = f
	m.items[key] = items
	return nil
}

func (m *memCache) getCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gets
}

func (m *memCache) putCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.puts
}

// countingRecorder counts build records.
type countingRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (c *countingRecorder) Record(_ context.Context, key, sourceURL string, ok bool, itemCount int, selector, confidence, errMsg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	line := key
	if !ok {
		line += " FAILED: " + errMsg
	}
	c.calls = append(c.calls, line)
	return nil
}

func (c *countingRecorder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// cachedServer mirrors newTestServer but adds a cache and a recorder, either of
// which may be nil.
func cachedServer(f *stubFetcher, c FeedCache, rec BuildRecorder) *Server {
	pinned := func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }
	return New(Options{
		Pipeline: &pipeline.Pipeline{Fetch: f, Now: pinned},
		Now:      pinned,
		Cache:    c,
		Recorder: rec,
	})
}

// A second request for the same feed must not touch the source site again.
func TestCacheServesTheSecondRequest(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	s := cachedServer(f, c, nil)

	first := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d\n%s", first.Code, first.Body)
	}
	if got := first.Header().Get("X-Feedme-Feedcache"); got != "" {
		t.Errorf("the first response should be a build, got X-Feedme-Feedcache=%q", got)
	}
	if f.called() != 1 {
		t.Fatalf("fetches after the first request = %d, want 1", f.called())
	}

	second := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d", second.Code)
	}
	if got := second.Header().Get("X-Feedme-Feedcache"); got != "hit" {
		t.Errorf("X-Feedme-Feedcache = %q, want hit", got)
	}
	if f.called() != 1 {
		t.Errorf("fetches after the second request = %d, want 1: the cache did not answer", f.called())
	}
	if second.Body.String() != first.Body.String() {
		t.Error("the cached body differs from the built one")
	}
	if got := second.Header().Get("ETag"); got != first.Header().Get("ETag") {
		t.Errorf("ETag changed across a cache hit: %q then %q", first.Header().Get("ETag"), got)
	}
}

// A conditional request against the cache must be answered without a rebuild.
// This is the whole point of storing the rendered feed.
func TestConditionalRequestIsAnsweredFromCache(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := cachedServer(f, newMemCache(), nil)

	first := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	etag := first.Header().Get("ETag")

	req := httptest.NewRequest(http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Error("a 304 must not carry a body")
	}
	if f.called() != 1 {
		t.Errorf("fetches = %d, want 1: the cache should have answered the 304", f.called())
	}
}

// refresh=1 is the reader's way out of a stale feed, so it must bypass the
// cache and write a fresh entry.
func TestRefreshBypassesTheCache(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	s := cachedServer(f, c, nil)

	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&refresh=1")

	if f.called() != 2 {
		t.Errorf("fetches = %d, want 2: refresh must reach the source", f.called())
	}
	if c.putCalls() != 2 {
		t.Errorf("puts = %d, want 2: a refresh must replace the stored feed", c.putCalls())
	}
}

// fresh=1 rebuilds on every request and is never stored: it is a standing
// preference ("never hand me a cached document") rather than a one-off refresh.
func TestFreshRebuildsAndIsNotStored(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	s := cachedServer(f, c, nil)

	for i := 0; i < 2; i++ {
		rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&fresh=1")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
		if got := rec.Header().Get("X-Feedme-Feedcache"); got != "" {
			t.Errorf("a fresh request must not be served from the store, got %q", got)
		}
	}
	if f.called() != 2 {
		t.Errorf("fetches = %d, want 2: every fresh request must reach the source", f.called())
	}
	if c.getCalls() != 0 {
		t.Errorf("gets = %d, want 0: a fresh feed does not consult the store", c.getCalls())
	}
	if c.putCalls() != 0 {
		t.Errorf("puts = %d, want 0: a fresh feed is not stored", c.putCalls())
	}
}

// A fresh feed is not stored, but the build still ran and is still recorded.
// The build history is what lists feeds on /feeds, so a fresh feed has to appear
// there like any other.
func TestFreshIsNotStoredButIsRecorded(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	rec := &countingRecorder{}
	s := cachedServer(f, c, rec)

	got := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&fresh=1")
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", got.Code, got.Body)
	}
	if c.putCalls() != 0 {
		t.Errorf("puts = %d, want 0: a fresh feed is not stored", c.putCalls())
	}
	if rec.count() != 1 {
		t.Errorf("records = %d, want 1: a fresh build is still a build", rec.count())
	}
}

// fresh=1 and the plain URL are different feeds. A build made for one must not
// be handed to the other, or the preference would silently attach to every
// subscriber of the cached variant.
func TestFreshAndCachedAreDifferentFeeds(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	s := cachedServer(f, c, nil)

	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&fresh=1")
	plain := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if got := plain.Header().Get("X-Feedme-Feedcache"); got != "" {
		t.Errorf("a plain request was served from a fresh build: %q", got)
	}
	if c.putCalls() != 1 {
		t.Errorf("puts = %d, want 1 (only the plain build is stored)", c.putCalls())
	}
}

// Two spellings of the same feed must share one cache entry, or the cache fills
// with duplicates that readers can never notice.
func TestCacheKeyIsCanonical(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	s := cachedServer(f, c, nil)

	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	// format=rss is the default, so this is the same feed.
	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&format=rss")

	if f.called() != 1 {
		t.Errorf("fetches = %d, want 1: the two requests are one feed", f.called())
	}
	if c.putCalls() != 1 {
		t.Errorf("puts = %d, want 1", c.putCalls())
	}
}

// Different options are different feeds.
func TestDifferentOptionsAreDifferentFeeds(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	s := cachedServer(f, c, nil)

	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&max=1")

	if c.putCalls() != 2 {
		t.Errorf("puts = %d, want 2", c.putCalls())
	}
	if f.called() != 2 {
		t.Errorf("fetches = %d, want 2", f.called())
	}
}

// The stored feed is what the reader will get, so its identity must be the
// canonical URL, written into the document as rel="self".
func TestSelfLinkIsWrittenAndUsedAsTheKey(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	s := cachedServer(f, c, nil)

	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	body := rec.Body.String()
	if !strings.Contains(body, `rel="self"`) {
		t.Fatalf("the feed has no self link:\n%s", body)
	}
	c.mu.Lock()
	keys := make([]string, 0, len(c.entries))
	for k := range c.entries {
		keys = append(keys, k)
	}
	c.mu.Unlock()
	if len(keys) != 1 {
		t.Fatalf("stored keys = %v", keys)
	}
	if !strings.Contains(body, strings.TrimPrefix(keys[0], "http://example.com/extract")) {
		t.Errorf("the self link %q does not appear in the feed body", keys[0])
	}
	if !strings.HasPrefix(keys[0], "http://example.com/extract?") {
		t.Errorf("the key %q is not a feed URL", keys[0])
	}
}

// A reverse proxy terminating TLS must still see https in the self link.
func TestSelfLinkHonoursForwardedProto(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	s := cachedServer(f, c, nil)

	req := httptest.NewRequest(http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews", nil)
	req.Header.Set("X-Forwarded-Proto", "https, http")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if !strings.HasPrefix(k, "https://") {
			t.Errorf("key = %q, want an https URL", k)
		}
	}
}

// A cache that cannot be written is a slow server, not a broken one.
func TestUnwritableCacheStillServesTheFeed(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	c.failPut = true
	s := cachedServer(f, c, nil)

	rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: a storage failure must not fail the request", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Alpha") {
		t.Error("the feed is missing its items")
	}
}

// Without a cache the server still works; it just rebuilds every time.
func TestNoCacheStillServes(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := cachedServer(f, nil, nil)
	for i := 0; i < 2; i++ {
		if rec := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews"); rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	if f.called() != 2 {
		t.Errorf("fetches = %d, want 2 with no cache", f.called())
	}
}

// The ttl a feed declares decides how long its rendered copy is reused.
func TestCacheLifetimeFollowsTTL(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	s := cachedServer(f, c, nil)

	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&ttl=45")
	c.mu.Lock()
	got := c.lifetime
	c.mu.Unlock()
	if got != 45*time.Minute {
		t.Errorf("lifetime = %s, want 45m", got)
	}

	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews&refresh=1")
	c.mu.Lock()
	got = c.lifetime
	c.mu.Unlock()
	if got != defaultMaxAgeSeconds*time.Second {
		t.Errorf("lifetime without a ttl = %s, want %ds", got, defaultMaxAgeSeconds)
	}
}

// Every build is noted, successful or not, so that a feed which quietly stopped
// producing items can be explained.
func TestBuildsAreRecorded(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	rec := &countingRecorder{}
	s := cachedServer(f, newMemCache(), rec)

	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if rec.count() != 1 {
		t.Errorf("records = %d, want 1", rec.count())
	}

	// A cache hit is not a build and must not be recorded as one.
	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if rec.count() != 1 {
		t.Errorf("records = %d, a cache hit is not a build", rec.count())
	}

	// A failure is recorded too, with its reason.
	broken := newStub()
	broken.errs["https://e.example/news"] = &fetch.StatusError{URL: "https://e.example/news", Code: 500}
	s2 := cachedServer(broken, newMemCache(), rec)
	do(t, s2, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if rec.count() != 2 {
		t.Errorf("records = %d, a failed build must be recorded", rec.count())
	}
	if !strings.Contains(rec.calls[1], "FAILED") {
		t.Errorf("the failure record is %q", rec.calls[1])
	}
}

// The item list is stored alongside the document, so a build can be inspected
// without parsing XML.
func TestCachedItemsAreStored(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	c := newMemCache()
	s := cachedServer(f, c, nil)
	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")

	c.mu.Lock()
	defer c.mu.Unlock()
	var items []CachedItem
	for _, v := range c.items {
		items = v
	}
	if len(items) != 3 {
		t.Fatalf("stored items = %d, want 3", len(items))
	}
	if items[0].Title == "" || items[0].URL == "" || items[0].PublishedAt.IsZero() {
		t.Errorf("first stored item is incomplete: %+v", items[0])
	}
}

// A feed-cache hit must not claim that the page cache did the work: nothing was
// fetched, so the two headers have to mean different things.
func TestFeedCacheHitDoesNotClaimAPageCacheHit(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := cachedServer(f, newMemCache(), nil)

	do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	second := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")

	if got := second.Header().Get("X-Feedme-Cache"); got != "" {
		t.Errorf("X-Feedme-Cache = %q, want empty: no page was fetched", got)
	}
	if got := second.Header().Get("X-Feedme-Feedcache"); got != "hit" {
		t.Errorf("X-Feedme-Feedcache = %q, want hit", got)
	}
}

// A reader that has the feed can revalidate with the date it was given, and a
// 304 costs no body and no rebuild.
func TestLastModifiedAndIfModifiedSince(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := cachedServer(f, newMemCache(), nil)

	first := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	lm := first.Header().Get("Last-Modified")
	if lm == "" {
		t.Fatal("the response carries no Last-Modified")
	}

	conditional := func(header string) int {
		req := httptest.NewRequest(http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews", nil)
		req.Header.Set("If-Modified-Since", header)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := conditional(lm); got != http.StatusNotModified {
		t.Errorf("same date: status = %d, want 304", got)
	}
	older := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := conditional(older); got != http.StatusOK {
		t.Errorf("older date: status = %d, want 200", got)
	}
}

// If-None-Match is the stronger validator, so a client that sends both gets the
// ETag answer even when the date alone would say 304.
func TestETagWinsOverIfModifiedSince(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := cachedServer(f, newMemCache(), nil)
	first := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")

	req := httptest.NewRequest(http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews", nil)
	req.Header.Set("If-None-Match", `"not-the-etag"`)
	req.Header.Set("If-Modified-Since", first.Header().Get("Last-Modified"))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: a non-matching ETag means modified", rec.Code)
	}
}

// The last-modified time survives a cache hit: the header a reader sees must not
// change just because the answer came from storage.
func TestLastModifiedSurvivesTheFeedCache(t *testing.T) {
	f := newStub().add("https://e.example/news", listPage)
	s := cachedServer(f, newMemCache(), nil)

	first := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	second := do(t, s, http.MethodGet, "/extract?url=https%3A%2F%2Fe.example%2Fnews")
	if second.Header().Get("X-Feedme-Feedcache") != "hit" {
		t.Fatal("the second request was not served from the feed cache")
	}
	if a, b := first.Header().Get("Last-Modified"), second.Header().Get("Last-Modified"); a != b || a == "" {
		t.Errorf("Last-Modified = %q then %q, want the same non-empty value", a, b)
	}
}
