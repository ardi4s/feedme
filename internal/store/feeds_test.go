package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestFeedCacheRoundTrip(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	want := &FeedCache{
		Key:       "url=https%3A%2F%2Fe.example%2F&format=rss",
		SourceURL: "https://e.example/",
		Format:    "rss",
		Status:    200,
		ETag:      `"abc"`,
		ItemCount: 2,
		Detector:  "article.card",
		// The detector is worth keeping: if detection later breaks, this is the
		// record of what it used to choose.
		Confidence: "high",
		Body:       []byte("<rss>...</rss>"),
		FetchedAt:  now,
		ExpiresAt:  now.Add(time.Hour),
	}
	items := []FeedItem{
		{Position: 0, URL: "https://e.example/a", Title: "A", Author: "Someone",
			PublishedAt: now.Add(-time.Hour), Summary: "sum a", ContentHTML: "<p>a</p>"},
		{Position: 1, URL: "https://e.example/b", Title: "B"},
	}
	if err := st.FeedCachePut(ctx, want, items); err != nil {
		t.Fatal(err)
	}

	got, err := st.FeedCacheGet(ctx, want.Key)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("cache entry was not found")
	}
	if string(got.Body) != string(want.Body) {
		t.Errorf("Body = %q", got.Body)
	}
	if got.ETag != want.ETag || got.Detector != want.Detector || got.Confidence != want.Confidence {
		t.Errorf("entry = %+v", got)
	}
	if got.ItemCount != 2 {
		t.Errorf("ItemCount = %d", got.ItemCount)
	}
}

func TestFeedCacheMiss(t *testing.T) {
	st := testStore(t)
	got, err := st.FeedCacheGet(context.Background(), "url=https%3A%2F%2Fnever.example%2F")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected a miss, got %+v", got)
	}
}

// A stale entry must not be served. A reader that keeps a subscription to a
// broken feed stops noticing that it is broken.
func TestFeedCacheExpiry(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	key := "url=https%3A%2F%2Fe.example%2F"
	now := time.Now()

	if err := st.FeedCachePut(ctx, &FeedCache{
		Key: key, SourceURL: "https://e.example/", Format: "rss", Status: 200,
		Body: []byte("old"), FetchedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := st.FeedCacheGet(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("an expired entry was returned: %+v", got)
	}
}

// A rebuild replaces the item list outright, because a feed describes the page
// as it is now.
func TestFeedCachePutReplacesItems(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now()
	key := "url=https%3A%2F%2Fe.example%2F"

	first := []FeedItem{{Position: 0, URL: "https://e.example/a"}, {Position: 1, URL: "https://e.example/b"}}
	if err := st.FeedCachePut(ctx, &FeedCache{
		Key: key, SourceURL: "https://e.example/", Format: "rss", Status: 200,
		ItemCount: 2, Body: []byte("v1"), FetchedAt: now, ExpiresAt: now.Add(time.Hour),
	}, first); err != nil {
		t.Fatal(err)
	}
	second := []FeedItem{{Position: 0, URL: "https://e.example/a"}}
	if err := st.FeedCachePut(ctx, &FeedCache{
		Key: key, SourceURL: "https://e.example/", Format: "rss", Status: 200,
		ItemCount: 1, Body: []byte("v2"), FetchedAt: now, ExpiresAt: now.Add(time.Hour),
	}, second); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM feed_items WHERE feed_key = ?`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("item rows = %d, want 1", n)
	}
	got, err := st.FeedCacheGet(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != "v2" {
		t.Errorf("Body = %q, want the rebuild", got.Body)
	}
}

// The same page with different options is a different feed and must not share a
// cache entry.
func TestFeedCacheKeysAreIndependent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now()
	for _, key := range []string{"url=https%3A%2F%2Fe.example%2F", "url=https%3A%2F%2Fe.example%2F&format=atom"} {
		if err := st.FeedCachePut(ctx, &FeedCache{
			Key: key, SourceURL: "https://e.example/", Format: "rss", Status: 200,
			Body: []byte("x"), FetchedAt: now, ExpiresAt: now.Add(time.Hour),
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.DB().QueryContext(ctx, `SELECT COUNT(*) FROM feed_cache`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no count row returned")
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

func TestRecordBuildKeepsFailures(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	key := "url=https%3A%2F%2Fe.example%2F"

	if err := st.RecordBuild(ctx, key, "https://e.example/", true, 12, "article.card", "high", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordBuild(ctx, key, "https://e.example/", false, 0, "", "", "fetch failed"); err != nil {
		t.Fatal(err)
	}
	builds, err := st.FeedBuilds(ctx, key, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 2 {
		t.Fatalf("builds = %d, want 2", len(builds))
	}
	// Newest first, and the failure is on top because that is what is being
	// investigated.
	if builds[0].OK != 0 || builds[0].Error != "fetch failed" {
		t.Errorf("newest build = %+v", builds[0])
	}
	if builds[1].ItemCount != 12 || builds[1].Selector != "article.card" {
		t.Errorf("older build = %+v", builds[1])
	}
}

func TestFeedCachePrune(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := st.FeedCachePut(ctx, &FeedCache{
		Key: "old", SourceURL: "u", Format: "rss", Status: 200, Body: []byte("x"),
		FetchedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.FeedCachePut(ctx, &FeedCache{
		Key: "new", SourceURL: "u", Format: "rss", Status: 200, Body: []byte("x"),
		FetchedAt: now, ExpiresAt: now.Add(time.Hour),
	}, nil); err != nil {
		t.Fatal(err)
	}
	n, err := st.FeedCachePrune(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pruned = %d, want 1", n)
	}
	if got, _ := st.FeedCacheGet(ctx, "new"); got == nil {
		t.Error("the live entry was pruned")
	}
}

// Pruning an expired feed must take its items with it. Items are the body of a
// cached rendering, so leaving them behind grows feed_items while feed_cache
// stays small: rows nothing can read again.
func TestFeedCachePruneRemovesItems(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now()
	put := func(key, url string, expires time.Time) {
		t.Helper()
		if err := st.FeedCachePut(ctx, &FeedCache{
			Key: key, SourceURL: "https://e.example/", Format: "rss", Status: 200,
			ItemCount: 1, Body: []byte("x"), FetchedAt: now, ExpiresAt: expires,
		}, []FeedItem{{Position: 0, URL: url}}); err != nil {
			t.Fatal(err)
		}
	}
	put("old", "https://e.example/old", now.Add(-time.Hour))
	put("new", "https://e.example/new", now.Add(time.Hour))

	// An item list whose cache row was pruned in an earlier pass. No expiry
	// test can find it again, so the sweep has to be by orphan.
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO feed_items (feed_key, position, url) VALUES ('ghost', 0, 'https://e.example/ghost')`); err != nil {
		t.Fatal(err)
	}

	if _, err := st.FeedCachePrune(ctx); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM feed_items WHERE feed_key = 'old'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("orphan item rows = %d, want 0", n)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM feed_items WHERE feed_key = 'ghost'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("pre-existing orphan rows = %d, want 0", n)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM feed_items WHERE feed_key = 'new'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("live item rows = %d, want 1", n)
	}
}

// Pruning the build history must never drop a feed's newest build: the
// management page lists feeds from this table, so that last row is what keeps a
// feed findable.
func TestFeedBuildsPruneKeepsTheNewest(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := st.RecordBuild(ctx, "many", "https://e.example/", true, 10+i, "article.card", "high", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RecordBuild(ctx, "once", "https://e.example/other", true, 3, "", "", ""); err != nil {
		t.Fatal(err)
	}

	n, err := st.FeedBuildsPrune(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("pruned = %d, want 3", n)
	}

	builds, err := st.FeedBuilds(ctx, "many", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 2 {
		t.Fatalf("builds kept = %d, want 2", len(builds))
	}
	if builds[0].ItemCount != 14 {
		t.Errorf("newest kept build = %d items, want the last one recorded", builds[0].ItemCount)
	}
	once, err := st.FeedBuilds(ctx, "once", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(once) != 1 {
		t.Errorf("a feed with a single build lost it: %d left", len(once))
	}
}

// The page reads one row per feed, from that feed's newest build, so a feed
// whose cached body is long gone is still reported.
func TestFeedBuildSummaryIsOneRowPerFeed(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	if err := st.RecordBuild(ctx, "a", "https://e.example/a", true, 5, "article.card", "high", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordBuild(ctx, "b", "https://e.example/b", true, 2, "", "", ""); err != nil {
		t.Fatal(err)
	}
	// A later, failed build of "a" is what the summary must report: it is the
	// one an operator needs to see.
	if err := st.RecordBuild(ctx, "a", "https://e.example/a", false, 0, "", "", "fetch failed"); err != nil {
		t.Fatal(err)
	}

	got, err := st.FeedBuildSummary(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("summaries = %d, want 2 (one per feed)", len(got))
	}
	byKey := map[string]FeedBuildSummary{}
	for _, s := range got {
		byKey[s.Key] = s
	}
	if a := byKey["a"]; a.OK || a.Error != "fetch failed" {
		t.Errorf("a = %+v, want the newest build's failure", a)
	}
	if b := byKey["b"]; !b.OK || b.ItemCount != 2 || b.SourceURL != "https://e.example/b" {
		t.Errorf("b = %+v", b)
	}
	if got[0].Key != "a" {
		t.Errorf("first = %q, want the most recently built feed", got[0].Key)
	}
}

// Forgetting a feed has to clear the history the page lists it from, or the
// feed would come straight back.
func TestFeedBuildsDeleteForgetsAFeed(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.RecordBuild(ctx, "gone", "https://e.example/", true, 1, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.FeedBuildsDelete(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	got, err := st.FeedBuildSummary(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("summaries = %d, want 0 after a forget", len(got))
	}
}
